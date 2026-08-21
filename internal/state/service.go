package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Repository interface {
	CreateReminder(context.Context, Reminder, AuditEvent, string) (Reminder, error)
	UpdateReminder(context.Context, Reminder, int64, AuditEvent, string) (Reminder, error)
	GetReminder(context.Context, string) (Reminder, error)
	ListAuditEvents(context.Context, string) ([]AuditEvent, error)
	ListReminders(context.Context, ReminderListOptions) ([]Reminder, error)
	SearchReminders(context.Context, string, int) ([]Reminder, error)
	ListChanges(context.Context, int64, int) ([]Change, error)
	AddComment(context.Context, Comment, AuditEvent, string) (Comment, error)
	ListComments(context.Context, string) ([]Comment, error)
	GetOccurrence(context.Context, string) (Occurrence, error)
	ListOccurrences(context.Context, string, OccurrenceListOptions) ([]Occurrence, error)
	UpdateOccurrence(context.Context, Occurrence, int64, AuditEvent, string) (Occurrence, error)

	CreateProject(context.Context, Project, AuditEvent, string) (Project, error)
	UpdateProject(context.Context, Project, int64, AuditEvent, string) (Project, error)
	GetProject(context.Context, string) (Project, error)
	ListProjects(context.Context) ([]Project, error)

	CreatePolicy(context.Context, ExecutionPolicy, AuditEvent, string) (ExecutionPolicy, error)
	UpdatePolicy(context.Context, ExecutionPolicy, int64, AuditEvent, string) (ExecutionPolicy, error)
	GetPolicy(context.Context, string) (ExecutionPolicy, error)
	ListPolicies(context.Context) ([]ExecutionPolicy, error)

	UpsertRunner(context.Context, Runner, AuditEvent, string) (Runner, error)
	UpdateRunner(context.Context, Runner, int64, AuditEvent, string) (Runner, error)
	GetRunner(context.Context, string) (Runner, error)
	ListRunners(context.Context) ([]Runner, error)
	TouchRunnerSeen(context.Context, string, time.Time) error

	CreateAgentRun(context.Context, AgentRun, AuditEvent, string) (AgentRun, bool, error)
	ClaimAgentRun(context.Context, AgentRun, int64, AuditEvent) (AgentRun, error)
	GetAgentRun(context.Context, string) (AgentRun, error)
	ListAgentRuns(context.Context, AgentRunListFilter) ([]AgentRun, error)
	ListClaimableRuns(context.Context, Runner, time.Time) ([]AgentRun, error)
	UpdateAgentRunTransition(context.Context, AgentRun, int64, *AuditEvent, *Occurrence, *AuditEvent, string) (AgentRun, bool, error)
	RequeueExpiredLeases(context.Context, time.Time) ([]AgentRun, error)
	ExpireStaleRuns(context.Context, time.Time) ([]AgentRun, error)
	ListDueOccurrences(context.Context, time.Time) ([]DueOccurrence, error)
	LatestChangeCursor(context.Context) (int64, error)
}

type Service struct {
	repository  Repository
	clock       func() time.Time
	newID       func() (string, error)
	runNotifier RunNotifier
}

// RunNotifier is invoked after a run reaches a terminal state. It is wired to
// the push pipeline and must be treated as best-effort: errors are swallowed
// by the service.
type RunNotifier func(ctx context.Context, run AgentRun, reminderTitle string) error

type ServiceOption func(*Service)

func WithClock(clock func() time.Time) ServiceOption {
	return func(service *Service) {
		service.clock = clock
	}
}

func WithIDGenerator(generator func() (string, error)) ServiceOption {
	return func(service *Service) {
		service.newID = generator
	}
}

// WithRunNotifier installs the terminal-state run notifier. A nil notifier is
// allowed and disables notifications.
func WithRunNotifier(notifier RunNotifier) ServiceOption {
	return func(service *Service) {
		service.runNotifier = notifier
	}
}

func NewService(repository Repository, options ...ServiceOption) *Service {
	service := &Service{
		repository: repository,
		clock:      func() time.Time { return time.Now().UTC() },
		newID: func() (string, error) {
			id, err := uuid.NewV7()
			return id.String(), err
		},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (service *Service) CreateReminder(ctx context.Context, actor Actor, input CreateReminderInput) (Reminder, error) {
	if err := validateMutation(actor, input.Title, input.ClientRequestID); err != nil {
		return Reminder{}, err
	}
	if actor.Kind == ActorKindRunner {
		return Reminder{}, ErrForbidden
	}
	if input.ExecutionPolicyID != nil && *input.ExecutionPolicyID != "" {
		if _, err := service.repository.GetPolicy(ctx, *input.ExecutionPolicyID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return Reminder{}, ErrInvalidInput
			}
			return Reminder{}, err
		}
	}

	reminderID, err := service.newID()
	if err != nil {
		return Reminder{}, fmt.Errorf("generate reminder ID: %w", err)
	}
	eventID, err := service.newID()
	if err != nil {
		return Reminder{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	now := service.clock().UTC()
	reminder := Reminder{
		ID:                reminderID,
		Title:             strings.TrimSpace(input.Title),
		Description:       input.Description,
		Status:            ReminderStatusActive,
		Schedule:          cloneSchedule(input.Schedule),
		Recurrence:        cloneRecurrence(input.Recurrence),
		ExecutionPolicyID: cloneStringPointer(input.ExecutionPolicyID),
		Revision:          1,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	after, err := json.Marshal(reminder)
	if err != nil {
		return Reminder{}, fmt.Errorf("encode reminder snapshot: %w", err)
	}
	event := AuditEvent{
		ID:              eventID,
		ReminderID:      reminder.ID,
		Action:          AuditActionCreated,
		Actor:           actor,
		ServerTime:      now,
		ClientTime:      input.ClientTime,
		Source:          input.Source,
		SourceExcerpt:   input.SourceExcerpt,
		AfterSnapshot:   after,
		ChangedFields:   []string{"archived", "description", "execution_policy_id", "recurrence", "schedule", "status", "title"},
		Revision:        reminder.Revision,
		CorrelationID:   correlationID(input.CorrelationID, input.ClientRequestID),
		ClientRequestID: input.ClientRequestID,
	}

	return service.repository.CreateReminder(ctx, reminder, event, input.ClientRequestID)
}

func (service *Service) UpdateReminder(ctx context.Context, actor Actor, reminderID string, input UpdateReminderInput) (Reminder, error) {
	if actor.ID == "" || actor.Kind == "" || input.ClientRequestID == "" || reminderID == "" {
		return Reminder{}, ErrInvalidInput
	}
	if actor.Kind == ActorKindRunner {
		return Reminder{}, ErrForbidden
	}
	if input.Archived != nil && actor.Kind != ActorKindOwner && actor.Kind != ActorKindDevice {
		return Reminder{}, ErrForbidden
	}
	if input.ExecutionPolicyID != nil && *input.ExecutionPolicyID != nil && **input.ExecutionPolicyID != "" {
		if _, err := service.repository.GetPolicy(ctx, **input.ExecutionPolicyID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return Reminder{}, ErrInvalidInput
			}
			return Reminder{}, err
		}
	}

	current, err := service.repository.GetReminder(ctx, reminderID)
	if err != nil {
		return Reminder{}, err
	}
	if current.Revision != input.ExpectedRevision {
		return Reminder{}, ErrRevisionConflict
	}

	updated := cloneReminder(current)
	changed := make([]string, 0, 7)
	if input.Title != nil {
		title := strings.TrimSpace(*input.Title)
		if title == "" {
			return Reminder{}, ErrInvalidInput
		}
		if updated.Title != title {
			updated.Title = title
			changed = append(changed, "title")
		}
	}
	if input.Description != nil && updated.Description != *input.Description {
		updated.Description = *input.Description
		changed = append(changed, "description")
	}
	if input.Status != nil && updated.Status != *input.Status {
		updated.Status = *input.Status
		changed = append(changed, "status")
	}
	if input.Schedule != nil && !reflect.DeepEqual(updated.Schedule, *input.Schedule) {
		updated.Schedule = cloneSchedule(*input.Schedule)
		changed = append(changed, "schedule")
	}
	if input.Recurrence != nil && !reflect.DeepEqual(updated.Recurrence, *input.Recurrence) {
		updated.Recurrence = cloneRecurrence(*input.Recurrence)
		changed = append(changed, "recurrence")
	}
	if input.ExecutionPolicyID != nil && !reflect.DeepEqual(updated.ExecutionPolicyID, *input.ExecutionPolicyID) {
		updated.ExecutionPolicyID = cloneStringPointer(*input.ExecutionPolicyID)
		changed = append(changed, "execution_policy_id")
	}
	if input.Archived != nil && updated.Archived != *input.Archived {
		updated.Archived = *input.Archived
		changed = append(changed, "archived")
	}
	if len(changed) == 0 {
		return current, nil
	}
	sort.Strings(changed)
	updated.Revision++
	updated.UpdatedAt = service.clock().UTC()

	beforeSnapshot, err := json.Marshal(current)
	if err != nil {
		return Reminder{}, fmt.Errorf("encode previous reminder snapshot: %w", err)
	}
	afterSnapshot, err := json.Marshal(updated)
	if err != nil {
		return Reminder{}, fmt.Errorf("encode updated reminder snapshot: %w", err)
	}
	eventID, err := service.newID()
	if err != nil {
		return Reminder{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	action := AuditActionUpdated
	if contains(changed, "archived") {
		if updated.Archived {
			action = AuditActionArchived
		} else {
			action = AuditActionRestored
		}
	}
	event := AuditEvent{
		ID:              eventID,
		ReminderID:      reminderID,
		Action:          action,
		Actor:           actor,
		ServerTime:      updated.UpdatedAt,
		ClientTime:      input.ClientTime,
		Source:          input.Source,
		SourceExcerpt:   input.SourceExcerpt,
		BeforeSnapshot:  beforeSnapshot,
		AfterSnapshot:   afterSnapshot,
		ChangedFields:   changed,
		Revision:        updated.Revision,
		CorrelationID:   correlationID(input.CorrelationID, input.ClientRequestID),
		ClientRequestID: input.ClientRequestID,
	}

	return service.repository.UpdateReminder(ctx, updated, input.ExpectedRevision, event, input.ClientRequestID)
}

func (service *Service) GetReminder(ctx context.Context, reminderID string) (Reminder, error) {
	if reminderID == "" {
		return Reminder{}, ErrInvalidInput
	}
	return service.repository.GetReminder(ctx, reminderID)
}

func (service *Service) ListAuditEvents(ctx context.Context, reminderID string) ([]AuditEvent, error) {
	if reminderID == "" {
		return nil, ErrInvalidInput
	}
	return service.repository.ListAuditEvents(ctx, reminderID)
}

func (service *Service) ListReminders(ctx context.Context, options ReminderListOptions) ([]Reminder, error) {
	return service.repository.ListReminders(ctx, options)
}

func (service *Service) SearchReminders(ctx context.Context, query string, limit int) ([]Reminder, error) {
	if strings.TrimSpace(query) == "" {
		return service.repository.ListReminders(ctx, ReminderListOptions{Limit: limit})
	}
	return service.repository.SearchReminders(ctx, query, limit)
}

func (service *Service) ListChanges(ctx context.Context, afterCursor int64, limit int) ([]Change, error) {
	if afterCursor < 0 {
		return nil, ErrInvalidInput
	}
	return service.repository.ListChanges(ctx, afterCursor, limit)
}

func (service *Service) GetBriefing(ctx context.Context, options BriefingOptions) (Briefing, error) {
	if options.AfterCursor < 0 {
		return Briefing{}, ErrInvalidInput
	}
	limit := options.Limit
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	active := ReminderStatusActive
	reminders, err := service.repository.ListReminders(ctx, ReminderListOptions{
		Status: &active,
		Limit:  limit,
	})
	if err != nil {
		return Briefing{}, err
	}
	sort.SliceStable(reminders, func(left int, right int) bool {
		leftSchedule := reminders[left].Schedule
		rightSchedule := reminders[right].Schedule
		if leftSchedule == nil {
			return false
		}
		if rightSchedule == nil {
			return true
		}
		leftKey := leftSchedule.LocalDate + "T" + leftSchedule.LocalTime
		rightKey := rightSchedule.LocalDate + "T" + rightSchedule.LocalTime
		return leftKey < rightKey
	})
	changes, err := service.repository.ListChanges(ctx, options.AfterCursor, limit)
	if err != nil {
		return Briefing{}, err
	}
	cursor := options.AfterCursor
	if len(changes) > 0 {
		cursor = changes[len(changes)-1].Cursor
	}
	return Briefing{
		GeneratedAt: service.clock().UTC(),
		Cursor:      cursor,
		Summary:     fmt.Sprintf("%d active reminders. %d changes since cursor %d.", len(reminders), len(changes), options.AfterCursor),
		Reminders:   reminders,
		Changes:     changes,
	}, nil
}

func (service *Service) AddComment(ctx context.Context, actor Actor, reminderID string, input AddCommentInput) (Comment, error) {
	if actor.ID == "" || actor.Kind == "" || reminderID == "" || strings.TrimSpace(input.Body) == "" || input.ClientRequestID == "" {
		return Comment{}, ErrInvalidInput
	}
	if actor.Kind == ActorKindRunner {
		return Comment{}, ErrForbidden
	}
	reminder, err := service.repository.GetReminder(ctx, reminderID)
	if err != nil {
		return Comment{}, err
	}
	commentID, err := service.newID()
	if err != nil {
		return Comment{}, fmt.Errorf("generate comment ID: %w", err)
	}
	eventID, err := service.newID()
	if err != nil {
		return Comment{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	now := service.clock().UTC()
	comment := Comment{
		ID:         commentID,
		ReminderID: reminderID,
		Body:       strings.TrimSpace(input.Body),
		Actor:      actor,
		Revision:   1,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	afterSnapshot, err := json.Marshal(comment)
	if err != nil {
		return Comment{}, fmt.Errorf("encode comment snapshot: %w", err)
	}
	event := AuditEvent{
		ID:              eventID,
		ReminderID:      reminderID,
		Action:          AuditActionCommentAdded,
		Actor:           actor,
		ServerTime:      now,
		ClientTime:      input.ClientTime,
		Source:          input.Source,
		SourceExcerpt:   input.SourceExcerpt,
		AfterSnapshot:   afterSnapshot,
		ChangedFields:   []string{"comments"},
		Revision:        reminder.Revision,
		CorrelationID:   correlationID(input.CorrelationID, input.ClientRequestID),
		ClientRequestID: input.ClientRequestID,
	}
	return service.repository.AddComment(ctx, comment, event, input.ClientRequestID)
}

func (service *Service) ListComments(ctx context.Context, reminderID string) ([]Comment, error) {
	if reminderID == "" {
		return nil, ErrInvalidInput
	}
	return service.repository.ListComments(ctx, reminderID)
}

func (service *Service) ListOccurrences(ctx context.Context, reminderID string, options OccurrenceListOptions) ([]Occurrence, error) {
	if reminderID == "" {
		return nil, ErrInvalidInput
	}
	return service.repository.ListOccurrences(ctx, reminderID, options)
}

func (service *Service) CompleteOccurrence(ctx context.Context, actor Actor, occurrenceID string, input CompleteOccurrenceInput) (Occurrence, error) {
	if actor.ID == "" || actor.Kind == "" || occurrenceID == "" || input.ClientRequestID == "" {
		return Occurrence{}, ErrInvalidInput
	}
	if actor.Kind == ActorKindRunner {
		return Occurrence{}, ErrForbidden
	}
	current, err := service.repository.GetOccurrence(ctx, occurrenceID)
	if err != nil {
		return Occurrence{}, err
	}
	updated := cloneOccurrence(current)
	now := service.clock().UTC()
	updated.Status = OccurrenceStatusCompleted
	updated.CompletedAt = &now
	updated.SnoozedUntil = nil
	updated.Revision++
	updated.UpdatedAt = now
	return service.updateOccurrence(ctx, actor, current, updated, input.ExpectedRevision, input.ClientTime, input.Source, input.SourceExcerpt, input.ClientRequestID, input.CorrelationID, AuditActionOccurrenceDone, []string{"occurrence.completed_at", "occurrence.status"})
}

func (service *Service) SnoozeOccurrence(ctx context.Context, actor Actor, occurrenceID string, input SnoozeOccurrenceInput) (Occurrence, error) {
	if actor.ID == "" || actor.Kind == "" || occurrenceID == "" || input.ClientRequestID == "" || !input.Until.After(service.clock()) {
		return Occurrence{}, ErrInvalidInput
	}
	if actor.Kind == ActorKindRunner {
		return Occurrence{}, ErrForbidden
	}
	current, err := service.repository.GetOccurrence(ctx, occurrenceID)
	if err != nil {
		return Occurrence{}, err
	}
	updated := cloneOccurrence(current)
	until := input.Until.UTC()
	now := service.clock().UTC()
	updated.Status = OccurrenceStatusSnoozed
	updated.SnoozedUntil = &until
	updated.CompletedAt = nil
	updated.Revision++
	updated.UpdatedAt = now
	return service.updateOccurrence(ctx, actor, current, updated, input.ExpectedRevision, input.ClientTime, input.Source, input.SourceExcerpt, input.ClientRequestID, input.CorrelationID, AuditActionOccurrenceSnoozed, []string{"occurrence.snoozed_until", "occurrence.status"})
}

func (service *Service) updateOccurrence(
	ctx context.Context,
	actor Actor,
	current Occurrence,
	updated Occurrence,
	expectedRevision int64,
	clientTime *time.Time,
	source string,
	sourceExcerpt string,
	clientRequestID string,
	requestedCorrelationID string,
	action AuditAction,
	changedFields []string,
) (Occurrence, error) {
	beforeSnapshot, err := json.Marshal(current)
	if err != nil {
		return Occurrence{}, fmt.Errorf("encode previous occurrence snapshot: %w", err)
	}
	afterSnapshot, err := json.Marshal(updated)
	if err != nil {
		return Occurrence{}, fmt.Errorf("encode updated occurrence snapshot: %w", err)
	}
	eventID, err := service.newID()
	if err != nil {
		return Occurrence{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	event := AuditEvent{
		ID:              eventID,
		ReminderID:      current.ReminderID,
		Action:          action,
		Actor:           actor,
		ServerTime:      updated.UpdatedAt,
		ClientTime:      clientTime,
		Source:          source,
		SourceExcerpt:   sourceExcerpt,
		BeforeSnapshot:  beforeSnapshot,
		AfterSnapshot:   afterSnapshot,
		ChangedFields:   changedFields,
		Revision:        updated.Revision,
		CorrelationID:   correlationID(requestedCorrelationID, clientRequestID),
		ClientRequestID: clientRequestID,
	}
	return service.repository.UpdateOccurrence(ctx, updated, expectedRevision, event, clientRequestID)
}

func validateMutation(actor Actor, title string, clientRequestID string) error {
	if actor.ID == "" || actor.Kind == "" || strings.TrimSpace(title) == "" || clientRequestID == "" {
		return ErrInvalidInput
	}
	return nil
}

func correlationID(explicit string, clientRequestID string) string {
	if explicit != "" {
		return explicit
	}
	return clientRequestID
}

func cloneReminder(reminder Reminder) Reminder {
	reminder.Schedule = cloneSchedule(reminder.Schedule)
	reminder.Recurrence = cloneRecurrence(reminder.Recurrence)
	reminder.ExecutionPolicyID = cloneStringPointer(reminder.ExecutionPolicyID)
	return reminder
}

func cloneSchedule(schedule *Schedule) *Schedule {
	if schedule == nil {
		return nil
	}
	copy := *schedule
	return &copy
}

func cloneRecurrence(recurrence *RecurrenceRule) *RecurrenceRule {
	if recurrence == nil {
		return nil
	}
	copy := *recurrence
	return &copy
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneOccurrence(occurrence Occurrence) Occurrence {
	if occurrence.ScheduledAt != nil {
		value := *occurrence.ScheduledAt
		occurrence.ScheduledAt = &value
	}
	if occurrence.CompletedAt != nil {
		value := *occurrence.CompletedAt
		occurrence.CompletedAt = &value
	}
	if occurrence.SnoozedUntil != nil {
		value := *occurrence.SnoozedUntil
		occurrence.SnoozedUntil = &value
	}
	return occurrence
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
