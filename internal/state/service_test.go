package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCreateReminderWritesRevisionAndAuditEvent(t *testing.T) {
	t.Parallel()

	repository := NewMemoryRepository()
	now := time.Date(2026, time.August, 11, 18, 30, 0, 0, time.UTC)
	service := NewService(repository, WithClock(func() time.Time { return now }))
	actor := Actor{
		ID:          "01989d45-c5c0-7d09-b52a-306dbaa9e17f",
		Kind:        ActorKindHarness,
		DisplayName: "Claude Code",
		Harness:     "claude-code",
		DeviceName:  "MacBook",
	}

	reminder, err := service.CreateReminder(context.Background(), actor, CreateReminderInput{
		Title:           "Prepare the quarterly review",
		Description:     "Bring the latest metrics.",
		ClientRequestID: "01989d45-c5c0-7259-a730-5b652d20e46b",
		SourceExcerpt:   "Remind me on 17 August at 9",
		Schedule: &Schedule{
			LocalDate: "2026-08-17",
			LocalTime: "09:00",
			TimeZone:  "Europe/Copenhagen",
			Mode:      TimeZoneModeFloating,
		},
	})
	if err != nil {
		t.Fatalf("CreateReminder() error = %v", err)
	}
	if reminder.Revision != 1 {
		t.Fatalf("revision = %d, want 1", reminder.Revision)
	}
	if reminder.ID == "" {
		t.Fatal("reminder ID is empty")
	}
	if reminder.CreatedAt != now || reminder.UpdatedAt != now {
		t.Fatalf("timestamps = %v and %v, want %v", reminder.CreatedAt, reminder.UpdatedAt, now)
	}

	events, err := repository.ListAuditEvents(context.Background(), reminder.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit event count = %d, want 1", len(events))
	}
	event := events[0]
	if event.Action != AuditActionCreated || event.Actor.ID != actor.ID {
		t.Fatalf("unexpected audit event: %#v", event)
	}
	if event.SourceExcerpt != "Remind me on 17 August at 9" {
		t.Fatalf("source excerpt = %q", event.SourceExcerpt)
	}
	if event.PreviousHash != "" || event.Hash == "" {
		t.Fatalf("invalid first hash link: previous=%q hash=%q", event.PreviousHash, event.Hash)
	}
}

func TestCreateReminderIsIdempotent(t *testing.T) {
	t.Parallel()

	repository := NewMemoryRepository()
	service := NewService(repository)
	actor := Actor{ID: "01989d45-c5c0-7a49-a00e-83bb43d926e2", Kind: ActorKindHarness}
	input := CreateReminderInput{
		Title:           "Book dentist appointment",
		ClientRequestID: "01989d45-c5c0-7f79-a1ed-591e422fb81e",
	}

	first, err := service.CreateReminder(context.Background(), actor, input)
	if err != nil {
		t.Fatalf("first CreateReminder() error = %v", err)
	}
	second, err := service.CreateReminder(context.Background(), actor, input)
	if err != nil {
		t.Fatalf("second CreateReminder() error = %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("IDs differ: %q and %q", first.ID, second.ID)
	}

	events, err := repository.ListAuditEvents(context.Background(), first.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit event count = %d, want 1", len(events))
	}
}

func TestUpdateReminderRejectsStaleRevision(t *testing.T) {
	t.Parallel()

	repository := NewMemoryRepository()
	service := NewService(repository)
	actor := Actor{ID: "01989d45-c5c0-7cf9-8709-a6caec3d8f13", Kind: ActorKindOwner}
	created, err := service.CreateReminder(context.Background(), actor, CreateReminderInput{
		Title:           "Initial title",
		ClientRequestID: "01989d45-c5c0-7bf9-b303-035f5837104a",
	})
	if err != nil {
		t.Fatalf("CreateReminder() error = %v", err)
	}

	_, err = service.UpdateReminder(context.Background(), actor, created.ID, UpdateReminderInput{
		Title:            pointer("Changed title"),
		ExpectedRevision: 0,
		ClientRequestID:  "01989d45-c5c0-7229-ad10-e299bc87bf2f",
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("UpdateReminder() error = %v, want ErrRevisionConflict", err)
	}

	current, err := repository.GetReminder(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetReminder() error = %v", err)
	}
	if current.Title != "Initial title" || current.Revision != 1 {
		t.Fatalf("stale update changed reminder: %#v", current)
	}
}

func TestHarnessCannotArchiveReminder(t *testing.T) {
	t.Parallel()

	repository := NewMemoryRepository()
	service := NewService(repository)
	owner := Actor{ID: "01989d45-c5c0-7aa9-a48d-a31e76535cd7", Kind: ActorKindOwner}
	harness := Actor{ID: "01989d45-c5c0-7ba9-b499-5659888080b9", Kind: ActorKindHarness}
	created, err := service.CreateReminder(context.Background(), owner, CreateReminderInput{
		Title:           "Protected reminder",
		ClientRequestID: "01989d45-c5c0-73c9-b4a5-5902dd7f983c",
	})
	if err != nil {
		t.Fatalf("CreateReminder() error = %v", err)
	}

	_, err = service.UpdateReminder(context.Background(), harness, created.ID, UpdateReminderInput{
		Archived:         pointer(true),
		ExpectedRevision: 1,
		ClientRequestID:  "01989d45-c5c0-74f9-a16a-57d5f4d24f09",
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("UpdateReminder() error = %v, want ErrForbidden", err)
	}
}

func pointer[T any](value T) *T {
	return &value
}
