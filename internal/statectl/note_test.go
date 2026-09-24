package statectl

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestNoteServiceAgainstTheRealServer(t *testing.T) {
	t.Parallel()

	session := newReminderTestSession(t)
	counter := 0
	service := NewNoteService(session, func() (string, error) {
		counter++
		return "01989f08-3333-7000-8000-" + strings.Repeat("0", 11) + string(rune('0'+counter)), nil
	})
	ctx := context.Background()

	stored, _, err := service.Create(ctx, "", "# Terminal\nNotiz aus der CLI", "Leg die Notiz an", "")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if stored.Title != "Terminal" || stored.Revision != 1 || stored.ID == "" {
		t.Fatalf("stored = %#v", stored)
	}

	raw, err := service.List(ctx, "cli", 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	var list struct {
		Notes []StoredNote `json:"notes"`
	}
	if err := json.Unmarshal(raw, &list); err != nil || len(list.Notes) != 1 || list.Notes[0].ID != stored.ID {
		t.Fatalf("List() = %s, %v", raw, err)
	}

	title := "Aus dem Terminal"
	updated, _, err := service.Update(ctx, UpdateNoteOptions{NoteID: stored.ID, Title: &title, SourceText: "Benenn sie um"})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Title != title || updated.Revision != 2 {
		t.Fatalf("updated = %#v", updated)
	}

	detail, err := service.Show(ctx, stored.ID)
	if err != nil || !strings.Contains(string(detail), "Notiz aus der CLI") {
		t.Fatalf("Show() = %s, %v", detail, err)
	}
}

func TestNoteServiceValidatesBeforeCalling(t *testing.T) {
	t.Parallel()

	service := NewNoteService(nil, func() (string, error) { return "01989f08-3333-7000-8000-000000000099", nil })
	if _, _, err := service.Create(context.Background(), "", " ", "x", ""); err == nil {
		t.Fatal("empty note was accepted")
	}
	if _, _, err := service.Create(context.Background(), "t", "", "", ""); err == nil {
		t.Fatal("missing source text was accepted")
	}
	if _, _, err := service.Update(context.Background(), UpdateNoteOptions{NoteID: "n", SourceText: "x"}); err == nil || !strings.Contains(err.Error(), "nothing") {
		t.Fatalf("empty update error = %v", err)
	}
}
