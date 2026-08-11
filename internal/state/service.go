package state

import (
	"context"
	"encoding/json"
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
}

type Service struct {
	repository Repository
	clock      func() time.Time
	newID      func() (string, error)
}

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
		ID:          reminderID,
		Title:       strings.TrimSpace(input.Title),
		Description: input.Description,
		Status:      ReminderStatusActive,
		Schedule:    cloneSchedule(input.Schedule),
		Recurrence:  cloneRecurrence(input.Recurrence),
		Revision:    1,
		CreatedAt:   now,
		UpdatedAt:   now,
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
		ChangedFields:   []string{"archived", "description", "recurrence", "schedule", "status", "title"},
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
	if input.Archived != nil && actor.Kind != ActorKindOwner && actor.Kind != ActorKindDevice {
		return Reminder{}, ErrForbidden
	}

	current, err := service.repository.GetReminder(ctx, reminderID)
	if err != nil {
		return Reminder{}, err
	}
	if current.Revision != input.ExpectedRevision {
		return Reminder{}, ErrRevisionConflict
	}

	updated := cloneReminder(current)
	changed := make([]string, 0, 6)
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

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
