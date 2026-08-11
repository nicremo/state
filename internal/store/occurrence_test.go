package store

import (
	"context"
	"testing"
	"time"

	"github.com/nicremo/state/internal/state"
)

func TestScheduledReminderCreatesCompletableOccurrence(t *testing.T) {
	t.Parallel()

	app := bootstrappedApp(t, t.TempDir())
	repository, err := NewPocketBaseRepository(app, deterministicSigningKey())
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() error = %v", err)
	}
	now := time.Date(2026, time.August, 11, 19, 0, 0, 0, time.UTC)
	service := state.NewService(repository, state.WithClock(func() time.Time { return now }))
	actor := state.Actor{ID: "01989ec9-91ad-7548-90f9-2b3e38d459b7", Kind: state.ActorKindHarness, DisplayName: "Codex"}
	reminder, err := service.CreateReminder(context.Background(), actor, state.CreateReminderInput{
		Title:           "Prepare review",
		ClientRequestID: "01989ec9-91ad-7d2a-aac5-c95f717fa10d",
		Schedule: &state.Schedule{
			LocalDate: "2026-08-17",
			LocalTime: "09:00",
			TimeZone:  "Europe/Copenhagen",
			Mode:      state.TimeZoneModeFloating,
		},
	})
	if err != nil {
		t.Fatalf("CreateReminder() error = %v", err)
	}
	occurrences, err := service.ListOccurrences(context.Background(), reminder.ID, state.OccurrenceListOptions{})
	if err != nil {
		t.Fatalf("ListOccurrences() error = %v", err)
	}
	if len(occurrences) != 1 {
		t.Fatalf("occurrence count = %d, want 1: %#v", len(occurrences), occurrences)
	}
	occurrence := occurrences[0]
	if occurrence.Status != state.OccurrenceStatusPending || occurrence.Revision != 1 || occurrence.ScheduledAt == nil {
		t.Fatalf("unexpected occurrence: %#v", occurrence)
	}

	completed, err := service.CompleteOccurrence(context.Background(), actor, occurrence.ID, state.CompleteOccurrenceInput{
		ExpectedRevision: 1,
		ClientRequestID:  "01989ec9-91ad-7ba6-922b-149061ee00b8",
		SourceExcerpt:    "Mark the review reminder as done",
	})
	if err != nil {
		t.Fatalf("CompleteOccurrence() error = %v", err)
	}
	replayed, err := service.CompleteOccurrence(context.Background(), actor, occurrence.ID, state.CompleteOccurrenceInput{
		ExpectedRevision: 1,
		ClientRequestID:  "01989ec9-91ad-7ba6-922b-149061ee00b8",
		SourceExcerpt:    "Mark the review reminder as done",
	})
	if err != nil {
		t.Fatalf("replayed CompleteOccurrence() error = %v", err)
	}
	if completed.ID != replayed.ID || completed.Status != state.OccurrenceStatusCompleted || completed.Revision != 2 {
		t.Fatalf("unexpected completion results: %#v and %#v", completed, replayed)
	}
	events, err := service.ListAuditEvents(context.Background(), reminder.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	if len(events) != 2 || events[1].Action != state.AuditActionOccurrenceDone {
		t.Fatalf("audit events = %#v", events)
	}
}

func TestOccurrenceCanBeSnoozedWithRevisionCheck(t *testing.T) {
	t.Parallel()

	app := bootstrappedApp(t, t.TempDir())
	repository, err := NewPocketBaseRepository(app, deterministicSigningKey())
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() error = %v", err)
	}
	now := time.Date(2026, time.August, 11, 19, 0, 0, 0, time.UTC)
	service := state.NewService(repository, state.WithClock(func() time.Time { return now }))
	actor := state.Actor{ID: "01989ec9-91ad-7973-a3d9-f5ec987b6874", Kind: state.ActorKindOwner}
	reminder, err := service.CreateReminder(context.Background(), actor, state.CreateReminderInput{
		Title:           "Call customer",
		ClientRequestID: "01989ec9-91ad-76fd-b8bc-29feea546874",
		Schedule: &state.Schedule{
			LocalDate: "2026-08-12",
			LocalTime: "09:00",
			TimeZone:  "UTC",
			Mode:      state.TimeZoneModeFixed,
		},
	})
	if err != nil {
		t.Fatalf("CreateReminder() error = %v", err)
	}
	occurrences, err := service.ListOccurrences(context.Background(), reminder.ID, state.OccurrenceListOptions{})
	if err != nil || len(occurrences) != 1 {
		t.Fatalf("ListOccurrences() = %#v, %v", occurrences, err)
	}
	snoozedUntil := now.Add(time.Hour)
	snoozed, err := service.SnoozeOccurrence(context.Background(), actor, occurrences[0].ID, state.SnoozeOccurrenceInput{
		Until:            snoozedUntil,
		ExpectedRevision: 1,
		ClientRequestID:  "01989ec9-91ad-78dc-8282-a098320d26f9",
	})
	if err != nil {
		t.Fatalf("SnoozeOccurrence() error = %v", err)
	}
	if snoozed.Status != state.OccurrenceStatusSnoozed || snoozed.SnoozedUntil == nil || !snoozed.SnoozedUntil.Equal(snoozedUntil) {
		t.Fatalf("snoozed occurrence = %#v", snoozed)
	}
	_, err = service.SnoozeOccurrence(context.Background(), actor, occurrences[0].ID, state.SnoozeOccurrenceInput{
		Until:            now.Add(2 * time.Hour),
		ExpectedRevision: 1,
		ClientRequestID:  "01989ec9-91ad-70be-97c8-3ac156bf81f1",
	})
	if err != state.ErrRevisionConflict {
		t.Fatalf("stale SnoozeOccurrence() error = %v, want ErrRevisionConflict", err)
	}
}
