package state

import (
	"context"
	"testing"
	"time"
)

func TestGetBriefingReturnsBoundedCurrentStateAndCursor(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 11, 20, 30, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	service := NewService(repository, WithClock(func() time.Time { return now }))
	actor := Actor{ID: "01989e2a-e980-7fbb-9513-dd55e5eb322b", Kind: ActorKindHarness, DisplayName: "Codex"}
	_, err := service.CreateReminder(context.Background(), actor, CreateReminderInput{
		Title:           "Later reminder",
		ClientRequestID: "01989e2a-e980-7cdf-982f-2a6c57c1be11",
		Schedule: &Schedule{
			LocalDate: "2026-08-20",
			LocalTime: "12:00",
			TimeZone:  "Europe/Copenhagen",
			Mode:      TimeZoneModeFloating,
		},
	})
	if err != nil {
		t.Fatalf("first CreateReminder() error = %v", err)
	}
	_, err = service.CreateReminder(context.Background(), actor, CreateReminderInput{
		Title:           "Sooner reminder",
		ClientRequestID: "01989e2a-e980-753f-8c22-826d4c720e6a",
		Schedule: &Schedule{
			LocalDate: "2026-08-17",
			LocalTime: "09:00",
			TimeZone:  "Europe/Copenhagen",
			Mode:      TimeZoneModeFloating,
		},
	})
	if err != nil {
		t.Fatalf("second CreateReminder() error = %v", err)
	}

	briefing, err := service.GetBriefing(context.Background(), BriefingOptions{AfterCursor: 0, Limit: 10})
	if err != nil {
		t.Fatalf("GetBriefing() error = %v", err)
	}
	if briefing.GeneratedAt != now {
		t.Fatalf("generated at = %v, want %v", briefing.GeneratedAt, now)
	}
	if len(briefing.Reminders) != 2 || briefing.Reminders[0].Title != "Sooner reminder" {
		t.Fatalf("briefing reminders = %#v", briefing.Reminders)
	}
	if len(briefing.Changes) != 2 || briefing.Cursor != 2 {
		t.Fatalf("briefing changes = %#v, cursor = %d", briefing.Changes, briefing.Cursor)
	}
	if briefing.Summary == "" {
		t.Fatal("briefing summary is empty")
	}
}
