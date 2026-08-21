package state

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type MemoryRepository struct {
	mu                 sync.RWMutex
	reminders          map[string]Reminder
	auditEvents        map[string][]AuditEvent
	requestResults     map[string]Reminder
	comments           map[string][]Comment
	requestComments    map[string]Comment
	occurrences        map[string]Occurrence
	requestOccurrences map[string]Occurrence
	projects           map[string]Project
	requestProjects    map[string]Project
	policies           map[string]ExecutionPolicy
	requestPolicies    map[string]ExecutionPolicy
	runners            map[string]Runner
	requestRunners     map[string]Runner
	runs               map[string]AgentRun
	requestRuns        map[string]AgentRun
	auditChain         []AuditEvent
	lastAuditHash      string
	signingKey         ed25519.PrivateKey
}

func NewMemoryRepository() *MemoryRepository {
	seed := sha256.Sum256([]byte("state-memory-repository-signing-key"))
	return &MemoryRepository{
		reminders:          make(map[string]Reminder),
		auditEvents:        make(map[string][]AuditEvent),
		requestResults:     make(map[string]Reminder),
		comments:           make(map[string][]Comment),
		requestComments:    make(map[string]Comment),
		occurrences:        make(map[string]Occurrence),
		requestOccurrences: make(map[string]Occurrence),
		projects:           make(map[string]Project),
		requestProjects:    make(map[string]Project),
		policies:           make(map[string]ExecutionPolicy),
		requestPolicies:    make(map[string]ExecutionPolicy),
		runners:            make(map[string]Runner),
		requestRunners:     make(map[string]Runner),
		runs:               make(map[string]AgentRun),
		requestRuns:        make(map[string]AgentRun),
		signingKey:         ed25519.NewKeyFromSeed(seed[:]),
	}
}

func (repository *MemoryRepository) CreateReminder(_ context.Context, reminder Reminder, event AuditEvent, clientRequestID string) (Reminder, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if existing, ok := repository.requestResults[clientRequestID]; ok {
		return cloneReminder(existing), nil
	}
	event = repository.sealAuditEvent(event)
	repository.reminders[reminder.ID] = cloneReminder(reminder)
	repository.auditEvents[reminder.ID] = append(repository.auditEvents[reminder.ID], cloneAuditEvent(event))
	repository.auditChain = append(repository.auditChain, cloneAuditEvent(event))
	repository.requestResults[clientRequestID] = cloneReminder(reminder)
	repository.reconcileOccurrences(reminder)
	return cloneReminder(reminder), nil
}

func (repository *MemoryRepository) UpdateReminder(_ context.Context, reminder Reminder, expectedRevision int64, event AuditEvent, clientRequestID string) (Reminder, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if existing, ok := repository.requestResults[clientRequestID]; ok {
		return cloneReminder(existing), nil
	}
	current, ok := repository.reminders[reminder.ID]
	if !ok {
		return Reminder{}, ErrNotFound
	}
	if current.Revision != expectedRevision {
		return Reminder{}, ErrRevisionConflict
	}
	event = repository.sealAuditEvent(event)
	repository.reminders[reminder.ID] = cloneReminder(reminder)
	repository.auditEvents[reminder.ID] = append(repository.auditEvents[reminder.ID], cloneAuditEvent(event))
	repository.auditChain = append(repository.auditChain, cloneAuditEvent(event))
	repository.requestResults[clientRequestID] = cloneReminder(reminder)
	repository.reconcileOccurrences(reminder)
	return cloneReminder(reminder), nil
}

func (repository *MemoryRepository) GetReminder(_ context.Context, reminderID string) (Reminder, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	reminder, ok := repository.reminders[reminderID]
	if !ok {
		return Reminder{}, ErrNotFound
	}
	return cloneReminder(reminder), nil
}

func (repository *MemoryRepository) ListAuditEvents(_ context.Context, reminderID string) ([]AuditEvent, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	events := repository.auditEvents[reminderID]
	result := make([]AuditEvent, len(events))
	for index, event := range events {
		result[index] = cloneAuditEvent(event)
	}
	return result, nil
}

func (repository *MemoryRepository) ListReminders(_ context.Context, options ReminderListOptions) ([]Reminder, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	result := make([]Reminder, 0, len(repository.reminders))
	for _, reminder := range repository.reminders {
		if !options.IncludeArchived && reminder.Archived {
			continue
		}
		if options.Status != nil && reminder.Status != *options.Status {
			continue
		}
		result = append(result, cloneReminder(reminder))
	}
	sort.Slice(result, func(left int, right int) bool {
		return result[left].UpdatedAt.After(result[right].UpdatedAt)
	})
	limit := normalizeLimit(options.Limit)
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (repository *MemoryRepository) SearchReminders(_ context.Context, query string, limit int) ([]Reminder, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	query = strings.ToLower(strings.TrimSpace(query))
	result := make([]Reminder, 0)
	for _, reminder := range repository.reminders {
		haystack := strings.ToLower(reminder.Title + "\n" + reminder.Description)
		for _, event := range repository.auditEvents[reminder.ID] {
			haystack += "\n" + strings.ToLower(event.SourceExcerpt)
		}
		for _, comment := range repository.comments[reminder.ID] {
			haystack += "\n" + strings.ToLower(comment.Body)
		}
		if strings.Contains(haystack, query) {
			result = append(result, cloneReminder(reminder))
		}
	}
	sort.Slice(result, func(left int, right int) bool {
		return result[left].UpdatedAt.After(result[right].UpdatedAt)
	})
	normalizedLimit := normalizeLimit(limit)
	if len(result) > normalizedLimit {
		result = result[:normalizedLimit]
	}
	return result, nil
}

func (repository *MemoryRepository) AddComment(_ context.Context, comment Comment, event AuditEvent, clientRequestID string) (Comment, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if existing, ok := repository.requestComments[clientRequestID]; ok {
		return cloneComment(existing), nil
	}
	if _, collision := repository.requestResults[clientRequestID]; collision {
		return Comment{}, ErrInvalidInput
	}
	if _, exists := repository.reminders[comment.ReminderID]; !exists {
		return Comment{}, ErrNotFound
	}
	event = repository.sealAuditEvent(event)
	repository.comments[comment.ReminderID] = append(repository.comments[comment.ReminderID], cloneComment(comment))
	repository.requestComments[clientRequestID] = cloneComment(comment)
	repository.auditEvents[comment.ReminderID] = append(repository.auditEvents[comment.ReminderID], cloneAuditEvent(event))
	repository.auditChain = append(repository.auditChain, cloneAuditEvent(event))
	return cloneComment(comment), nil
}

func (repository *MemoryRepository) ListComments(_ context.Context, reminderID string) ([]Comment, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	comments := repository.comments[reminderID]
	result := make([]Comment, len(comments))
	for index, comment := range comments {
		result[index] = cloneComment(comment)
	}
	return result, nil
}

func (repository *MemoryRepository) GetOccurrence(_ context.Context, occurrenceID string) (Occurrence, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	occurrence, ok := repository.occurrences[occurrenceID]
	if !ok {
		return Occurrence{}, ErrNotFound
	}
	return cloneOccurrence(occurrence), nil
}

func (repository *MemoryRepository) ListOccurrences(_ context.Context, reminderID string, options OccurrenceListOptions) ([]Occurrence, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	result := make([]Occurrence, 0)
	for _, occurrence := range repository.occurrences {
		if occurrence.ReminderID != reminderID {
			continue
		}
		if options.Status != nil && occurrence.Status != *options.Status {
			continue
		}
		result = append(result, cloneOccurrence(occurrence))
	}
	sort.Slice(result, func(left int, right int) bool {
		return occurrenceSortKey(result[left]) < occurrenceSortKey(result[right])
	})
	limit := normalizeLimit(options.Limit)
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (repository *MemoryRepository) UpdateOccurrence(_ context.Context, occurrence Occurrence, expectedRevision int64, event AuditEvent, clientRequestID string) (Occurrence, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if existing, ok := repository.requestOccurrences[clientRequestID]; ok {
		return cloneOccurrence(existing), nil
	}
	current, ok := repository.occurrences[occurrence.ID]
	if !ok {
		return Occurrence{}, ErrNotFound
	}
	if current.Revision != expectedRevision {
		return Occurrence{}, ErrRevisionConflict
	}
	event = repository.sealAuditEvent(event)
	repository.occurrences[occurrence.ID] = cloneOccurrence(occurrence)
	repository.requestOccurrences[clientRequestID] = cloneOccurrence(occurrence)
	repository.auditEvents[occurrence.ReminderID] = append(repository.auditEvents[occurrence.ReminderID], cloneAuditEvent(event))
	repository.auditChain = append(repository.auditChain, cloneAuditEvent(event))
	return cloneOccurrence(occurrence), nil
}

func (repository *MemoryRepository) ListChanges(_ context.Context, afterCursor int64, limit int) ([]Change, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	result := make([]Change, 0)
	for index, event := range repository.auditChain {
		cursor := int64(index + 1)
		if cursor <= afterCursor {
			continue
		}
		result = append(result, Change{Cursor: cursor, Event: cloneAuditEvent(event)})
		if len(result) == normalizeLimit(limit) {
			break
		}
	}
	return result, nil
}

func (repository *MemoryRepository) CreateProject(_ context.Context, project Project, event AuditEvent, clientRequestID string) (Project, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if existing, ok := repository.requestProjects[clientRequestID]; ok {
		return existing, nil
	}
	for _, existing := range repository.projects {
		if existing.Name == project.Name {
			return Project{}, fmt.Errorf("insert project: duplicate name %q", project.Name)
		}
	}
	repository.appendAuditEvent(event)
	repository.projects[project.ID] = project
	repository.requestProjects[clientRequestID] = project
	return project, nil
}

func (repository *MemoryRepository) UpdateProject(_ context.Context, project Project, expectedRevision int64, event AuditEvent, clientRequestID string) (Project, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if existing, ok := repository.requestProjects[clientRequestID]; ok {
		return existing, nil
	}
	current, ok := repository.projects[project.ID]
	if !ok {
		return Project{}, ErrNotFound
	}
	if current.Revision != expectedRevision {
		return Project{}, ErrRevisionConflict
	}
	repository.appendAuditEvent(event)
	repository.projects[project.ID] = project
	repository.requestProjects[clientRequestID] = project
	return project, nil
}

func (repository *MemoryRepository) GetProject(_ context.Context, projectID string) (Project, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	project, ok := repository.projects[projectID]
	if !ok {
		return Project{}, ErrNotFound
	}
	return project, nil
}

func (repository *MemoryRepository) ListProjects(_ context.Context) ([]Project, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	result := make([]Project, 0, len(repository.projects))
	for _, project := range repository.projects {
		result = append(result, project)
	}
	sort.Slice(result, func(left int, right int) bool {
		if result[left].Name != result[right].Name {
			return result[left].Name < result[right].Name
		}
		return result[left].ID < result[right].ID
	})
	return result, nil
}

func (repository *MemoryRepository) CreatePolicy(_ context.Context, policy ExecutionPolicy, event AuditEvent, clientRequestID string) (ExecutionPolicy, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if existing, ok := repository.requestPolicies[clientRequestID]; ok {
		return clonePolicy(existing), nil
	}
	if _, ok := repository.projects[policy.ProjectID]; !ok {
		return ExecutionPolicy{}, ErrNotFound
	}
	for _, existing := range repository.policies {
		if existing.Name == policy.Name {
			return ExecutionPolicy{}, fmt.Errorf("insert policy: duplicate name %q", policy.Name)
		}
	}
	repository.appendAuditEvent(event)
	repository.policies[policy.ID] = clonePolicy(policy)
	repository.requestPolicies[clientRequestID] = clonePolicy(policy)
	return clonePolicy(policy), nil
}

func (repository *MemoryRepository) UpdatePolicy(_ context.Context, policy ExecutionPolicy, expectedRevision int64, event AuditEvent, clientRequestID string) (ExecutionPolicy, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if existing, ok := repository.requestPolicies[clientRequestID]; ok {
		return clonePolicy(existing), nil
	}
	current, ok := repository.policies[policy.ID]
	if !ok {
		return ExecutionPolicy{}, ErrNotFound
	}
	if current.Revision != expectedRevision {
		return ExecutionPolicy{}, ErrRevisionConflict
	}
	repository.appendAuditEvent(event)
	repository.policies[policy.ID] = clonePolicy(policy)
	repository.requestPolicies[clientRequestID] = clonePolicy(policy)
	return clonePolicy(policy), nil
}

func (repository *MemoryRepository) GetPolicy(_ context.Context, policyID string) (ExecutionPolicy, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	policy, ok := repository.policies[policyID]
	if !ok {
		return ExecutionPolicy{}, ErrNotFound
	}
	return clonePolicy(policy), nil
}

func (repository *MemoryRepository) ListPolicies(_ context.Context) ([]ExecutionPolicy, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	result := make([]ExecutionPolicy, 0, len(repository.policies))
	for _, policy := range repository.policies {
		result = append(result, clonePolicy(policy))
	}
	sort.Slice(result, func(left int, right int) bool {
		if result[left].Name != result[right].Name {
			return result[left].Name < result[right].Name
		}
		return result[left].ID < result[right].ID
	})
	return result, nil
}

func (repository *MemoryRepository) UpsertRunner(_ context.Context, runner Runner, event AuditEvent, clientRequestID string) (Runner, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if existing, ok := repository.requestRunners[clientRequestID]; ok {
		return cloneRunner(existing), nil
	}
	if current, ok := repository.runners[runner.ID]; ok && current.Revision != runner.Revision-1 {
		return Runner{}, ErrRevisionConflict
	}
	repository.appendAuditEvent(event)
	repository.runners[runner.ID] = cloneRunner(runner)
	repository.requestRunners[clientRequestID] = cloneRunner(runner)
	return cloneRunner(runner), nil
}

func (repository *MemoryRepository) UpdateRunner(_ context.Context, runner Runner, expectedRevision int64, event AuditEvent, clientRequestID string) (Runner, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if existing, ok := repository.requestRunners[clientRequestID]; ok {
		return cloneRunner(existing), nil
	}
	current, ok := repository.runners[runner.ID]
	if !ok {
		return Runner{}, ErrNotFound
	}
	if current.Revision != expectedRevision {
		return Runner{}, ErrRevisionConflict
	}
	repository.appendAuditEvent(event)
	repository.runners[runner.ID] = cloneRunner(runner)
	repository.requestRunners[clientRequestID] = cloneRunner(runner)
	return cloneRunner(runner), nil
}

func (repository *MemoryRepository) GetRunner(_ context.Context, runnerID string) (Runner, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	runner, ok := repository.runners[runnerID]
	if !ok {
		return Runner{}, ErrNotFound
	}
	return cloneRunner(runner), nil
}

func (repository *MemoryRepository) ListRunners(_ context.Context) ([]Runner, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	result := make([]Runner, 0, len(repository.runners))
	for _, runner := range repository.runners {
		result = append(result, cloneRunner(runner))
	}
	sort.Slice(result, func(left int, right int) bool {
		if !result[left].RegisteredAt.Equal(result[right].RegisteredAt) {
			return result[left].RegisteredAt.Before(result[right].RegisteredAt)
		}
		return result[left].ID < result[right].ID
	})
	return result, nil
}

func (repository *MemoryRepository) TouchRunnerSeen(_ context.Context, runnerID string, seenAt time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	runner, ok := repository.runners[runnerID]
	if !ok {
		return ErrNotFound
	}
	runner.LastSeenAt = seenAt
	repository.runners[runnerID] = runner
	return nil
}

func (repository *MemoryRepository) CreateAgentRun(_ context.Context, run AgentRun, event AuditEvent, clientRequestID string) (AgentRun, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if clientRequestID != "" {
		if existing, ok := repository.requestRuns[clientRequestID]; ok {
			return cloneAgentRun(existing), false, nil
		}
	}
	if _, ok := repository.reminders[run.ReminderID]; !ok {
		return AgentRun{}, false, ErrNotFound
	}
	if run.OccurrenceID != nil {
		for _, existing := range repository.runs {
			if existing.OccurrenceID != nil && *existing.OccurrenceID == *run.OccurrenceID && existing.PolicyRevision == run.PolicyRevision {
				return cloneAgentRun(existing), false, nil
			}
		}
	}
	repository.appendAuditEvent(event)
	repository.runs[run.ID] = cloneAgentRun(run)
	if clientRequestID != "" {
		repository.requestRuns[clientRequestID] = cloneAgentRun(run)
	}
	return cloneAgentRun(run), true, nil
}

func (repository *MemoryRepository) ClaimAgentRun(_ context.Context, run AgentRun, expectedRevision int64, event AuditEvent) (AgentRun, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	current, ok := repository.runs[run.ID]
	if !ok {
		return AgentRun{}, ErrNotFound
	}
	if current.Status != AgentRunStatusEligible || current.Revision != expectedRevision {
		return AgentRun{}, ErrNotClaimable
	}
	repository.appendAuditEvent(event)
	repository.runs[run.ID] = cloneAgentRun(run)
	return cloneAgentRun(run), nil
}

func (repository *MemoryRepository) GetAgentRun(_ context.Context, runID string) (AgentRun, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	run, ok := repository.runs[runID]
	if !ok {
		return AgentRun{}, ErrNotFound
	}
	return cloneAgentRun(run), nil
}

func (repository *MemoryRepository) ListAgentRuns(_ context.Context, filter AgentRunListFilter) ([]AgentRun, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	result := make([]AgentRun, 0)
	for _, run := range repository.runs {
		if filter.ReminderID != "" && run.ReminderID != filter.ReminderID {
			continue
		}
		if filter.Status != nil && run.Status != *filter.Status {
			continue
		}
		if filter.RunnerID != "" && (run.RunnerID == nil || *run.RunnerID != filter.RunnerID) {
			continue
		}
		result = append(result, cloneAgentRun(run))
	}
	sort.Slice(result, func(left int, right int) bool {
		if !requestedAtEqual(result[left], result[right]) {
			return requestedAtBefore(result[right], result[left])
		}
		return result[left].ID > result[right].ID
	})
	limit := normalizeLimit(filter.Limit)
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (repository *MemoryRepository) ListClaimableRuns(_ context.Context, runner Runner, _ time.Time) ([]AgentRun, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	result := make([]AgentRun, 0)
	for _, run := range repository.runs {
		if run.Status != AgentRunStatusEligible {
			continue
		}
		if !contains(runner.Adapters, run.Adapter) || !contains(runner.Projects, run.ProjectID) {
			continue
		}
		result = append(result, cloneAgentRun(run))
	}
	sort.Slice(result, func(left int, right int) bool {
		if !requestedAtEqual(result[left], result[right]) {
			return requestedAtBefore(result[left], result[right])
		}
		return result[left].ID < result[right].ID
	})
	return result, nil
}

func (repository *MemoryRepository) UpdateAgentRunTransition(_ context.Context, run AgentRun, expectedRevision int64, event *AuditEvent, occurrence *Occurrence, occurrenceEvent *AuditEvent, clientRequestID string) (AgentRun, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if clientRequestID != "" {
		if existing, ok := repository.requestRuns[clientRequestID]; ok {
			return cloneAgentRun(existing), false, nil
		}
	}
	current, ok := repository.runs[run.ID]
	if !ok {
		return AgentRun{}, false, ErrNotFound
	}
	if current.Revision != expectedRevision {
		return AgentRun{}, false, ErrRunStateConflict
	}
	if event != nil {
		repository.appendAuditEvent(*event)
	}
	repository.runs[run.ID] = cloneAgentRun(run)
	if occurrence != nil {
		currentOccurrence, ok := repository.occurrences[occurrence.ID]
		if ok {
			if currentOccurrence.Revision != occurrence.Revision-1 {
				return AgentRun{}, false, ErrRevisionConflict
			}
			if occurrenceEvent != nil {
				repository.appendAuditEvent(*occurrenceEvent)
			}
			repository.occurrences[occurrence.ID] = cloneOccurrence(*occurrence)
		}
	}
	if clientRequestID != "" {
		repository.requestRuns[clientRequestID] = cloneAgentRun(run)
	}
	return cloneAgentRun(run), true, nil
}

func (repository *MemoryRepository) RequeueExpiredLeases(_ context.Context, now time.Time) ([]AgentRun, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	expired := make([]AgentRun, 0)
	for _, run := range repository.runs {
		if run.Status == AgentRunStatusClaimed && run.LeaseExpiresAt != nil && run.LeaseExpiresAt.Before(now) {
			expired = append(expired, cloneAgentRun(run))
		}
	}
	sort.Slice(expired, func(left int, right int) bool {
		if !requestedAtEqual(expired[left], expired[right]) {
			return requestedAtBefore(expired[left], expired[right])
		}
		return expired[left].ID < expired[right].ID
	})
	requeued := make([]AgentRun, 0, len(expired))
	for _, run := range expired {
		updated := cloneAgentRun(run)
		updated.Status = AgentRunStatusEligible
		updated.RunnerID = nil
		updated.ClaimedAt = nil
		updated.LeaseExpiresAt = nil
		updated.Revision++
		updated.UpdatedAt = now
		event, err := sweepAuditEvent(run, updated, AuditActionRunRequeued, []string{"claimed_at", "lease_expires_at", "runner_id", "status"}, fmt.Sprintf("requeue:%s:%d", run.ID, updated.Revision), now)
		if err != nil {
			return nil, err
		}
		repository.appendAuditEvent(event)
		repository.runs[run.ID] = cloneAgentRun(updated)
		requeued = append(requeued, cloneAgentRun(updated))
	}
	return requeued, nil
}

func (repository *MemoryRepository) ExpireStaleRuns(_ context.Context, now time.Time) ([]AgentRun, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	cutoff := now.Add(-StaleRunMaxAge)
	stale := make([]AgentRun, 0)
	for _, run := range repository.runs {
		if run.Status != AgentRunStatusPlanned && run.Status != AgentRunStatusEligible {
			continue
		}
		if run.RequestedAt != nil && run.RequestedAt.Before(cutoff) {
			stale = append(stale, cloneAgentRun(run))
			continue
		}
		if run.OccurrenceID != nil {
			if _, ok := repository.occurrences[*run.OccurrenceID]; !ok {
				stale = append(stale, cloneAgentRun(run))
				continue
			}
		}
		policy, ok := repository.policies[run.PolicyID]
		if !ok || !policy.Enabled {
			stale = append(stale, cloneAgentRun(run))
		}
	}
	sort.Slice(stale, func(left int, right int) bool {
		if !requestedAtEqual(stale[left], stale[right]) {
			return requestedAtBefore(stale[left], stale[right])
		}
		return stale[left].ID < stale[right].ID
	})
	expired := make([]AgentRun, 0, len(stale))
	for _, run := range stale {
		updated := cloneAgentRun(run)
		updated.Status = AgentRunStatusExpired
		updated.FinishedAt = &now
		updated.Revision++
		updated.UpdatedAt = now
		event, err := sweepAuditEvent(run, updated, AuditActionRunExpired, []string{"finished_at", "status"}, fmt.Sprintf("expire:%s:%d", run.ID, updated.Revision), now)
		if err != nil {
			return nil, err
		}
		repository.appendAuditEvent(event)
		repository.runs[run.ID] = cloneAgentRun(updated)
		expired = append(expired, cloneAgentRun(updated))
	}
	return expired, nil
}

func (repository *MemoryRepository) ListDueOccurrences(_ context.Context, now time.Time) ([]DueOccurrence, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	result := make([]DueOccurrence, 0)
	for _, occurrence := range repository.occurrences {
		if occurrence.Status != OccurrenceStatusPending && occurrence.Status != OccurrenceStatusSnoozed {
			continue
		}
		reminder, ok := repository.reminders[occurrence.ReminderID]
		if !ok || reminder.Archived {
			continue
		}
		if !occurrenceDue(occurrence, now) {
			continue
		}
		result = append(result, DueOccurrence{Reminder: cloneReminder(reminder), Occurrence: cloneOccurrence(occurrence)})
	}
	sort.Slice(result, func(left int, right int) bool {
		return occurrenceSortKey(result[left].Occurrence) < occurrenceSortKey(result[right].Occurrence)
	})
	return result, nil
}

func (repository *MemoryRepository) LatestChangeCursor(_ context.Context) (int64, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	return int64(len(repository.auditChain)), nil
}

// sweepAuditEvent builds the system-actor audit event for a scheduler-driven
// run transition.
func sweepAuditEvent(before AgentRun, after AgentRun, action AuditAction, changedFields []string, clientRequestID string, now time.Time) (AuditEvent, error) {
	eventID, err := uuid.NewV7()
	if err != nil {
		return AuditEvent{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	beforeSnapshot, err := json.Marshal(before)
	if err != nil {
		return AuditEvent{}, fmt.Errorf("encode previous run snapshot: %w", err)
	}
	afterSnapshot, err := json.Marshal(after)
	if err != nil {
		return AuditEvent{}, fmt.Errorf("encode updated run snapshot: %w", err)
	}
	return AuditEvent{
		ID:              eventID.String(),
		ReminderID:      before.ReminderID,
		Action:          action,
		Actor:           SystemActor(),
		ServerTime:      now,
		BeforeSnapshot:  beforeSnapshot,
		AfterSnapshot:   afterSnapshot,
		ChangedFields:   changedFields,
		Revision:        after.Revision,
		CorrelationID:   before.TaskContract.CorrelationID,
		ClientRequestID: clientRequestID,
	}, nil
}

func (repository *MemoryRepository) appendAuditEvent(event AuditEvent) {
	sealed := repository.sealAuditEvent(event)
	repository.auditEvents[sealed.ReminderID] = append(repository.auditEvents[sealed.ReminderID], cloneAuditEvent(sealed))
	repository.auditChain = append(repository.auditChain, cloneAuditEvent(sealed))
}

// occurrenceDue mirrors the push due-window semantics: a snoozed occurrence
// fires at its snoozed-until time, any other at scheduled minus prewarning.
func occurrenceDue(occurrence Occurrence, now time.Time) bool {
	var dueAt time.Time
	if occurrence.Status == OccurrenceStatusSnoozed && occurrence.SnoozedUntil != nil {
		dueAt = occurrence.SnoozedUntil.UTC()
	} else if occurrence.ScheduledAt != nil {
		dueAt = occurrence.ScheduledAt.Add(-time.Duration(occurrence.PrewarningMinutes) * time.Minute).UTC()
	} else {
		return false
	}
	return !dueAt.After(now)
}

func requestedAtBefore(left AgentRun, right AgentRun) bool {
	if left.RequestedAt == nil {
		return right.RequestedAt != nil
	}
	if right.RequestedAt == nil {
		return false
	}
	return left.RequestedAt.Before(*right.RequestedAt)
}

func requestedAtEqual(left AgentRun, right AgentRun) bool {
	if left.RequestedAt == nil || right.RequestedAt == nil {
		return left.RequestedAt == nil && right.RequestedAt == nil
	}
	return left.RequestedAt.Equal(*right.RequestedAt)
}

func cloneRunner(runner Runner) Runner {
	runner.Projects = append([]string(nil), runner.Projects...)
	runner.Adapters = append([]string(nil), runner.Adapters...)
	return runner
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func (repository *MemoryRepository) sealAuditEvent(event AuditEvent) AuditEvent {
	event.PreviousHash = repository.lastAuditHash
	event.Hash = ""
	event.Signature = ""
	encoded, err := json.Marshal(event)
	if err != nil {
		panic(err)
	}
	hash := sha256.Sum256(encoded)
	event.Hash = hex.EncodeToString(hash[:])
	event.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(repository.signingKey, hash[:]))
	repository.lastAuditHash = event.Hash
	return event
}

func cloneAuditEvent(event AuditEvent) AuditEvent {
	event.BeforeSnapshot = append(json.RawMessage(nil), event.BeforeSnapshot...)
	event.AfterSnapshot = append(json.RawMessage(nil), event.AfterSnapshot...)
	event.ChangedFields = append([]string(nil), event.ChangedFields...)
	return event
}

func cloneComment(comment Comment) Comment {
	return comment
}

func (repository *MemoryRepository) reconcileOccurrences(reminder Reminder) {
	for id, occurrence := range repository.occurrences {
		if occurrence.ReminderID == reminder.ID && occurrence.Status == OccurrenceStatusPending {
			delete(repository.occurrences, id)
		}
	}
	if reminder.Schedule == nil || reminder.Archived {
		return
	}
	fromDate := reminder.Schedule.LocalDate
	if reminder.Recurrence != nil {
		location, err := time.LoadLocation(reminder.Schedule.TimeZone)
		if err == nil {
			fromDate = reminder.UpdatedAt.In(location).Format(localDateLayout)
		}
	}
	throughDate := reminder.UpdatedAt.AddDate(1, 1, 0).Format(localDateLayout)
	seeds, err := ExpandOccurrenceSeeds(reminder, fromDate, throughDate)
	if err != nil {
		return
	}
	for _, seed := range seeds {
		id, err := uuid.NewV7()
		if err != nil {
			continue
		}
		repository.occurrences[id.String()] = occurrenceFromSeed(id.String(), reminder.ID, seed, reminder.UpdatedAt)
	}
}

func occurrenceFromSeed(id string, reminderID string, seed OccurrenceSeed, createdAt time.Time) Occurrence {
	return Occurrence{
		ID:                id,
		ReminderID:        reminderID,
		LocalDate:         seed.LocalDate,
		LocalTime:         seed.LocalTime,
		TimeZone:          seed.TimeZone,
		TimeZoneMode:      seed.TimeZoneMode,
		PrewarningMinutes: seed.PrewarningMinutes,
		ScheduledAt:       seed.ScheduledAt,
		Status:            OccurrenceStatusPending,
		Revision:          1,
		CreatedAt:         createdAt,
		UpdatedAt:         createdAt,
	}
}

func occurrenceSortKey(occurrence Occurrence) string {
	return occurrence.LocalDate + "T" + occurrence.LocalTime + occurrence.ID
}
