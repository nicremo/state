package state

import (
	"context"
	"testing"
	"time"
)

func TestAddCommentIsIdempotentAndAudited(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 11, 21, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	service := NewService(repository, WithClock(func() time.Time { return now }))
	owner := Actor{ID: "01989e64-678c-74ca-926d-7051f023a144", Kind: ActorKindOwner, DisplayName: "Fabian"}
	harness := Actor{ID: "01989e64-678c-738e-b97e-3d23ef2a9204", Kind: ActorKindHarness, DisplayName: "Claude Code"}
	reminder, err := service.CreateReminder(context.Background(), owner, CreateReminderInput{
		Title:           "Discuss architecture",
		ClientRequestID: "01989e64-678c-75f0-b3d9-daa61bda8fb9",
	})
	if err != nil {
		t.Fatalf("CreateReminder() error = %v", err)
	}
	input := AddCommentInput{
		Body:            "The relay must never receive plaintext.",
		ClientRequestID: "01989e64-678c-7ff0-a68a-e0f4ce9b16dc",
		SourceExcerpt:   "Add that the relay is plaintext blind",
	}
	first, err := service.AddComment(context.Background(), harness, reminder.ID, input)
	if err != nil {
		t.Fatalf("AddComment() error = %v", err)
	}
	second, err := service.AddComment(context.Background(), harness, reminder.ID, input)
	if err != nil {
		t.Fatalf("replayed AddComment() error = %v", err)
	}
	if first.ID != second.ID || first.Revision != 1 || first.CreatedAt != now {
		t.Fatalf("unexpected idempotent comment results: %#v and %#v", first, second)
	}
	comments, err := service.ListComments(context.Background(), reminder.ID)
	if err != nil {
		t.Fatalf("ListComments() error = %v", err)
	}
	if len(comments) != 1 || comments[0].Actor.ID != harness.ID {
		t.Fatalf("comments = %#v", comments)
	}
	events, err := service.ListAuditEvents(context.Background(), reminder.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	if len(events) != 2 || events[1].Action != AuditActionCommentAdded {
		t.Fatalf("audit events = %#v", events)
	}
}
