package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nicremo/state/internal/state"
)

var storeNoteOwner = state.Actor{ID: "01989d9b-0000-7000-8000-00000000a001", Kind: state.ActorKindOwner, DisplayName: "Fabian"}

func newStoreNoteService(t *testing.T) (*state.Service, *PocketBaseRepository, string) {
	t.Helper()
	dataDirectory := t.TempDir()
	repository, err := NewPocketBaseRepository(bootstrappedApp(t, dataDirectory), deterministicSigningKey())
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() error = %v", err)
	}
	clock := time.Date(2026, time.September, 24, 10, 0, 0, 0, time.UTC)
	service := state.NewService(repository, state.WithClock(func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}))
	return service, repository, dataDirectory
}

func TestNotesPersistSearchAndKeepTheAuditChainValid(t *testing.T) {
	t.Parallel()
	service, repository, _ := newStoreNoteService(t)
	ctx := context.Background()

	note, err := service.CreateNote(ctx, storeNoteOwner, state.CreateNoteInput{
		Document:        "# Einkauf\nMilch und **Brot** holen",
		ClientRequestID: "01989d9b-0000-7000-8000-00000000b001",
	})
	if err != nil {
		t.Fatalf("CreateNote() error = %v", err)
	}
	replayed, err := service.CreateNote(ctx, storeNoteOwner, state.CreateNoteInput{
		Document:        "# Einkauf\nMilch und **Brot** holen",
		ClientRequestID: "01989d9b-0000-7000-8000-00000000b001",
	})
	if err != nil || replayed.ID != note.ID {
		t.Fatalf("idempotent replay = %s, %v", replayed.ID, err)
	}

	loaded, err := service.GetNote(ctx, note.ID)
	if err != nil || loaded.Title != "Einkauf" || loaded.Summary != "Milch und Brot holen" {
		t.Fatalf("GetNote() = %#v, %v", loaded, err)
	}

	document := "# Einkauf\nMilch, Brot und Käse"
	updated, err := service.UpdateNote(ctx, storeNoteOwner, note.ID, state.UpdateNoteInput{
		Document: &document, ExpectedRevision: 1, ClientRequestID: "01989d9b-0000-7000-8000-00000000b002",
	})
	if err != nil || updated.Revision != 2 {
		t.Fatalf("UpdateNote() = %#v, %v", updated, err)
	}
	if _, err := service.UpdateNote(ctx, storeNoteOwner, note.ID, state.UpdateNoteInput{
		Document: &document, ExpectedRevision: 1, ClientRequestID: "01989d9b-0000-7000-8000-00000000b003",
	}); !errors.Is(err, state.ErrRevisionConflict) {
		t.Fatalf("stale update error = %v", err)
	}

	found, err := service.ListNotes(ctx, state.NoteListOptions{Query: "kase"})
	if err != nil || len(found) != 1 || found[0].ID != note.ID {
		t.Fatalf("search kase = %#v, %v", found, err)
	}
	if stale, _ := service.ListNotes(ctx, state.NoteListOptions{Query: "Brot holen"}); len(stale) != 0 {
		t.Fatalf("search index kept the old document: %#v", stale)
	}
	reminders, err := service.SearchReminders(ctx, "Käse", 10)
	if err != nil || len(reminders) != 0 {
		t.Fatalf("reminder search saw a note: %#v, %v", reminders, err)
	}

	history, err := service.ListNoteHistory(ctx, note.ID)
	if err != nil || len(history) != 2 || history[0].Action != state.AuditActionNoteCreated || history[1].NoteID != note.ID {
		t.Fatalf("history = %#v, %v", history, err)
	}
	if err := repository.VerifyAuditChain(ctx); err != nil {
		t.Fatalf("VerifyAuditChain() error = %v", err)
	}
	changes, err := service.ListChanges(ctx, 0, 10)
	if err != nil || len(changes) != 2 || changes[0].Event.NoteID != note.ID {
		t.Fatalf("changes = %#v, %v", changes, err)
	}
}

func TestNotesListNewestFirstAndHideArchived(t *testing.T) {
	t.Parallel()
	service, _, _ := newStoreNoteService(t)
	ctx := context.Background()
	older, _ := service.CreateNote(ctx, storeNoteOwner, state.CreateNoteInput{Document: "Alt", ClientRequestID: "01989d9b-0000-7000-8000-00000000c001"})
	newer, _ := service.CreateNote(ctx, storeNoteOwner, state.CreateNoteInput{Document: "Neu", ClientRequestID: "01989d9b-0000-7000-8000-00000000c002"})
	gone, _ := service.CreateNote(ctx, storeNoteOwner, state.CreateNoteInput{Document: "Weg", ClientRequestID: "01989d9b-0000-7000-8000-00000000c003"})
	archive := true
	if _, err := service.UpdateNote(ctx, storeNoteOwner, gone.ID, state.UpdateNoteInput{Archived: &archive, ExpectedRevision: 1, ClientRequestID: "01989d9b-0000-7000-8000-00000000c004"}); err != nil {
		t.Fatalf("archive error = %v", err)
	}

	notes, err := service.ListNotes(ctx, state.NoteListOptions{})
	if err != nil || len(notes) != 2 || notes[0].ID != newer.ID || notes[1].ID != older.ID {
		t.Fatalf("notes = %#v, %v", notes, err)
	}
	all, _ := service.ListNotes(ctx, state.NoteListOptions{IncludeArchived: true})
	if len(all) != 3 || all[0].ID != gone.ID {
		t.Fatalf("all = %#v", all)
	}
	if archived, _ := service.ListNotes(ctx, state.NoteListOptions{Query: "Weg"}); len(archived) != 0 {
		t.Fatalf("search returned an archived note: %#v", archived)
	}
}

func TestNotesSurviveARestart(t *testing.T) {
	t.Parallel()
	service, _, dataDirectory := newStoreNoteService(t)
	note, err := service.CreateNote(context.Background(), storeNoteOwner, state.CreateNoteInput{Document: "Bleibt", ClientRequestID: "01989d9b-0000-7000-8000-00000000d001"})
	if err != nil {
		t.Fatalf("CreateNote() error = %v", err)
	}
	restarted, err := NewPocketBaseRepository(bootstrappedApp(t, dataDirectory), deterministicSigningKey())
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	loaded, err := restarted.GetNote(context.Background(), note.ID)
	if err != nil || loaded.Document != "Bleibt" {
		t.Fatalf("GetNote() after restart = %#v, %v", loaded, err)
	}
}
