package store

import (
	"context"
	"testing"

	"github.com/nicremo/state/internal/state"
)

func TestPocketBaseRepositorySearchesContentAndAuditContext(t *testing.T) {
	t.Parallel()

	app := bootstrappedApp(t, t.TempDir())
	repository, err := NewPocketBaseRepository(app, deterministicSigningKey())
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() error = %v", err)
	}
	service := state.NewService(repository)
	actor := state.Actor{ID: "01989e0c-c3d8-7d48-9f77-6490461eaee0", Kind: state.ActorKindOwner, DisplayName: "Fabian"}
	first, err := service.CreateReminder(context.Background(), actor, state.CreateReminderInput{
		Title:           "Prepare quarterly metrics",
		Description:     "Use the latest dashboard.",
		ClientRequestID: "01989e0c-c3d8-7f82-8745-17af5f82b84a",
	})
	if err != nil {
		t.Fatalf("first CreateReminder() error = %v", err)
	}
	second, err := service.CreateReminder(context.Background(), actor, state.CreateReminderInput{
		Title:           "Book dentist",
		ClientRequestID: "01989e0c-c3d8-79fd-93ac-a1dad54e9c27",
	})
	if err != nil {
		t.Fatalf("second CreateReminder() error = %v", err)
	}
	newDescription := "Coordinate with the infrastructure team."
	_, err = service.UpdateReminder(context.Background(), actor, second.ID, state.UpdateReminderInput{
		Description:      &newDescription,
		ExpectedRevision: 1,
		ClientRequestID:  "01989e0c-c3d8-7b81-9784-3fffe5f98c64",
		SourceExcerpt:    "The rollout wording came from the planning call",
	})
	if err != nil {
		t.Fatalf("UpdateReminder() error = %v", err)
	}

	contentResults, err := repository.SearchReminders(context.Background(), "quarterly metrics", 20)
	if err != nil {
		t.Fatalf("SearchReminders(content) error = %v", err)
	}
	if len(contentResults) != 1 || contentResults[0].ID != first.ID {
		t.Fatalf("content search results = %#v", contentResults)
	}
	auditResults, err := repository.SearchReminders(context.Background(), "planning call", 20)
	if err != nil {
		t.Fatalf("SearchReminders(audit) error = %v", err)
	}
	if len(auditResults) != 1 || auditResults[0].ID != second.ID {
		t.Fatalf("audit search results = %#v", auditResults)
	}
	_, err = service.AddComment(context.Background(), actor, first.ID, state.AddCommentInput{
		Body:            "The finance appendix contains the operating margin.",
		ClientRequestID: "01989e64-678c-77e8-8164-80108cefa4a1",
	})
	if err != nil {
		t.Fatalf("AddComment() error = %v", err)
	}
	commentResults, err := repository.SearchReminders(context.Background(), "operating margin", 20)
	if err != nil {
		t.Fatalf("SearchReminders(comment) error = %v", err)
	}
	if len(commentResults) != 1 || commentResults[0].ID != first.ID {
		t.Fatalf("comment search results = %#v", commentResults)
	}
}

func TestPocketBaseRepositoryListsRemindersAndIncrementalChanges(t *testing.T) {
	t.Parallel()

	app := bootstrappedApp(t, t.TempDir())
	repository, err := NewPocketBaseRepository(app, deterministicSigningKey())
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() error = %v", err)
	}
	service := state.NewService(repository)
	actor := state.Actor{ID: "01989e0c-c3d8-7ee6-93c5-c95e3900de31", Kind: state.ActorKindOwner}
	first, err := service.CreateReminder(context.Background(), actor, state.CreateReminderInput{
		Title:           "First",
		ClientRequestID: "01989e0c-c3d8-7352-bc14-90d8e7f9d9d5",
	})
	if err != nil {
		t.Fatalf("first CreateReminder() error = %v", err)
	}
	_, err = service.CreateReminder(context.Background(), actor, state.CreateReminderInput{
		Title:           "Second",
		ClientRequestID: "01989e0c-c3d8-7830-a884-dc0e367f4d20",
	})
	if err != nil {
		t.Fatalf("second CreateReminder() error = %v", err)
	}
	updatedTitle := "First updated"
	_, err = service.UpdateReminder(context.Background(), actor, first.ID, state.UpdateReminderInput{
		Title:            &updatedTitle,
		ExpectedRevision: 1,
		ClientRequestID:  "01989e0c-c3d8-7f88-9a8e-adc39376d351",
	})
	if err != nil {
		t.Fatalf("UpdateReminder() error = %v", err)
	}

	reminders, err := repository.ListReminders(context.Background(), state.ReminderListOptions{Limit: 20})
	if err != nil {
		t.Fatalf("ListReminders() error = %v", err)
	}
	if len(reminders) != 2 {
		t.Fatalf("reminder count = %d, want 2", len(reminders))
	}
	changes, err := repository.ListChanges(context.Background(), 0, 20)
	if err != nil {
		t.Fatalf("ListChanges() error = %v", err)
	}
	if len(changes) != 3 {
		t.Fatalf("change count = %d, want 3", len(changes))
	}
	if changes[0].Cursor >= changes[1].Cursor || changes[1].Cursor >= changes[2].Cursor {
		t.Fatalf("change cursors are not increasing: %#v", changes)
	}
	incremental, err := repository.ListChanges(context.Background(), changes[1].Cursor, 20)
	if err != nil {
		t.Fatalf("incremental ListChanges() error = %v", err)
	}
	if len(incremental) != 1 || incremental[0].Event.Action != state.AuditActionUpdated {
		t.Fatalf("incremental changes = %#v", incremental)
	}
}
