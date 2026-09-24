package state

import (
	"context"
	"testing"
)

// A first line made only of control characters must not become an empty
// title; the next line with text names the note.

func TestAControlOnlyFirstLineDoesNotHideTheTitle(t *testing.T) {
	service, _, _ := newNoteService(t)
	_, err := service.CreateNote(context.Background(), noteOwner, CreateNoteInput{Document: "\x1b\nEinkaufsliste Milch", ClientRequestID: "01989d45-3333-7000-8000-0000000000f1"})
	if err != nil {
		t.Fatalf("note with text on line 2 rejected: %v (title derived = %q)", err, DeriveNoteTitle("\x1b\nEinkaufsliste Milch"))
	}
}
