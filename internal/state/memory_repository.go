package state

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
