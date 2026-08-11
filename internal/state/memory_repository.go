package state

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sync"
)

type MemoryRepository struct {
	mu             sync.RWMutex
	reminders      map[string]Reminder
	auditEvents    map[string][]AuditEvent
	requestResults map[string]Reminder
	lastAuditHash  string
	signingKey     ed25519.PrivateKey
}

func NewMemoryRepository() *MemoryRepository {
	seed := sha256.Sum256([]byte("state-memory-repository-signing-key"))
	return &MemoryRepository{
		reminders:      make(map[string]Reminder),
		auditEvents:    make(map[string][]AuditEvent),
		requestResults: make(map[string]Reminder),
		signingKey:     ed25519.NewKeyFromSeed(seed[:]),
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
	repository.requestResults[clientRequestID] = cloneReminder(reminder)
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
	repository.requestResults[clientRequestID] = cloneReminder(reminder)
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
