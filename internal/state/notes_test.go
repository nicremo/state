package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var (
	noteOwner   = Actor{ID: "01989d45-0000-7000-8000-000000000001", Kind: ActorKindOwner, DisplayName: "Fabian"}
	noteHarness = Actor{ID: "01989d45-0000-7000-8000-000000000002", Kind: ActorKindHarness, DisplayName: "Codex", Harness: "codex"}
	noteRunner  = Actor{ID: "01989d45-0000-7000-8000-000000000003", Kind: ActorKindRunner, DisplayName: "MacBook Pro"}
)

func newNoteService(t *testing.T) (*Service, *MemoryRepository, *time.Time) {
	t.Helper()
	repository := NewMemoryRepository()
	now := time.Date(2026, time.September, 24, 10, 0, 0, 0, time.UTC)
	service := NewService(repository, WithClock(func() time.Time { return now }))
	return service, repository, &now
}

func TestCreateNoteDerivesTitleAndSummaryFromTheDocument(t *testing.T) {
	t.Parallel()
	service, repository, _ := newNoteService(t)

	note, err := service.CreateNote(context.Background(), noteHarness, CreateNoteInput{
		Document:        "# Einkauf\n\nMilch und **Brot** holen.\n- [ ] Eier",
		ClientRequestID: "01989d45-1111-7000-8000-000000000001",
		SourceExcerpt:   "Schreib mir eine Einkaufsnotiz",
	})
	if err != nil {
		t.Fatalf("CreateNote() error = %v", err)
	}
	if note.Title != "Einkauf" || note.TitleSource != NoteFieldSourceDerived {
		t.Fatalf("title = %q (%s), want derived Einkauf", note.Title, note.TitleSource)
	}
	if note.Summary != "Milch und Brot holen. Eier" || note.SummarySource != NoteFieldSourceDerived {
		t.Fatalf("summary = %q (%s)", note.Summary, note.SummarySource)
	}
	if strings.Contains(note.PlainText, "**") || !strings.Contains(note.PlainText, "Brot") {
		t.Fatalf("plain text = %q", note.PlainText)
	}
	if note.Revision != 1 || note.Archived {
		t.Fatalf("revision %d archived %v", note.Revision, note.Archived)
	}

	events, err := repository.ListNoteAuditEvents(context.Background(), note.ID)
	if err != nil {
		t.Fatalf("ListNoteAuditEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	event := events[0]
	if event.Action != AuditActionNoteCreated || event.NoteID != note.ID || event.ReminderID != "" {
		t.Fatalf("unexpected event %#v", event)
	}
	if event.SourceExcerpt != "Schreib mir eine Einkaufsnotiz" || event.Actor.ID != noteHarness.ID {
		t.Fatalf("event provenance = %#v", event)
	}
}

func TestCreateNoteKeepsAnExplicitTitle(t *testing.T) {
	t.Parallel()
	service, _, _ := newNoteService(t)

	note, err := service.CreateNote(context.Background(), noteOwner, CreateNoteInput{
		Title:           "  Mein Titel ",
		Document:        "Erste Zeile\nZweite Zeile",
		ClientRequestID: "01989d45-1111-7000-8000-000000000002",
	})
	if err != nil {
		t.Fatalf("CreateNote() error = %v", err)
	}
	if note.Title != "Mein Titel" || note.TitleSource != NoteFieldSourceUser {
		t.Fatalf("title = %q (%s)", note.Title, note.TitleSource)
	}
	if note.Summary != "Erste Zeile Zweite Zeile" {
		t.Fatalf("summary = %q, the whole body belongs to it when the title is explicit", note.Summary)
	}
}

func TestCreateNoteRejectsEmptyNotesAndOversizedDocuments(t *testing.T) {
	t.Parallel()
	service, _, _ := newNoteService(t)

	cases := []CreateNoteInput{
		{Document: "  \n ", ClientRequestID: "01989d45-1111-7000-8000-000000000003"},
		{Document: strings.Repeat("a", MaxNoteDocumentBytes+1), ClientRequestID: "01989d45-1111-7000-8000-000000000004"},
		{Title: strings.Repeat("t", MaxNoteTitleRunes+1), ClientRequestID: "01989d45-1111-7000-8000-000000000005"},
		{Document: "ok"},
	}
	for index, input := range cases {
		if _, err := service.CreateNote(context.Background(), noteOwner, input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("case %d error = %v, want ErrInvalidInput", index, err)
		}
	}
}

func TestCreateNoteIsIdempotentPerClientRequest(t *testing.T) {
	t.Parallel()
	service, repository, _ := newNoteService(t)
	input := CreateNoteInput{Document: "Hallo", ClientRequestID: "01989d45-1111-7000-8000-000000000006"}

	first, err := service.CreateNote(context.Background(), noteOwner, input)
	if err != nil {
		t.Fatalf("first CreateNote() error = %v", err)
	}
	second, err := service.CreateNote(context.Background(), noteOwner, input)
	if err != nil {
		t.Fatalf("second CreateNote() error = %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("retry created a second note: %s and %s", first.ID, second.ID)
	}
	events, _ := repository.ListNoteAuditEvents(context.Background(), first.ID)
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
}

func TestUpdateNoteRederivesOnlyDerivedFields(t *testing.T) {
	t.Parallel()
	service, repository, now := newNoteService(t)
	note, err := service.CreateNote(context.Background(), noteOwner, CreateNoteInput{
		Title:           "Fester Titel",
		Document:        "Alter Text",
		ClientRequestID: "01989d45-1111-7000-8000-000000000007",
	})
	if err != nil {
		t.Fatalf("CreateNote() error = %v", err)
	}

	*now = now.Add(time.Minute)
	document := "Neuer Text"
	updated, err := service.UpdateNote(context.Background(), noteHarness, note.ID, UpdateNoteInput{
		Document:         &document,
		ExpectedRevision: note.Revision,
		ClientRequestID:  "01989d45-1111-7000-8000-000000000008",
	})
	if err != nil {
		t.Fatalf("UpdateNote() error = %v", err)
	}
	if updated.Title != "Fester Titel" || updated.TitleSource != NoteFieldSourceUser {
		t.Fatalf("user title was touched: %q (%s)", updated.Title, updated.TitleSource)
	}
	if updated.Summary != "Neuer Text" || updated.Revision != 2 || !updated.UpdatedAt.Equal(*now) {
		t.Fatalf("unexpected update %#v", updated)
	}

	empty := ""
	reset, err := service.UpdateNote(context.Background(), noteOwner, note.ID, UpdateNoteInput{
		Title:            &empty,
		ExpectedRevision: updated.Revision,
		ClientRequestID:  "01989d45-1111-7000-8000-000000000009",
	})
	if err != nil {
		t.Fatalf("reset title error = %v", err)
	}
	if reset.Title != "Neuer Text" || reset.TitleSource != NoteFieldSourceDerived {
		t.Fatalf("empty title did not fall back to the document: %q (%s)", reset.Title, reset.TitleSource)
	}

	events, _ := repository.ListNoteAuditEvents(context.Background(), note.ID)
	if len(events) != 3 || events[1].Action != AuditActionNoteUpdated {
		t.Fatalf("events = %#v", events)
	}
	if strings.Join(events[1].ChangedFields, ",") != "document,plain_text,summary" {
		t.Fatalf("changed fields = %v", events[1].ChangedFields)
	}
}

func TestUpdateNoteRejectsStaleRevision(t *testing.T) {
	t.Parallel()
	service, _, _ := newNoteService(t)
	note, _ := service.CreateNote(context.Background(), noteOwner, CreateNoteInput{Document: "A", ClientRequestID: "01989d45-1111-7000-8000-000000000010"})
	document := "B"
	_, err := service.UpdateNote(context.Background(), noteOwner, note.ID, UpdateNoteInput{
		Document:         &document,
		ExpectedRevision: note.Revision + 1,
		ClientRequestID:  "01989d45-1111-7000-8000-000000000011",
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("error = %v, want ErrRevisionConflict", err)
	}
}

func TestHarnessCannotArchiveNotes(t *testing.T) {
	t.Parallel()
	service, repository, _ := newNoteService(t)
	note, _ := service.CreateNote(context.Background(), noteHarness, CreateNoteInput{Document: "A", ClientRequestID: "01989d45-1111-7000-8000-000000000012"})
	archive := true
	if _, err := service.UpdateNote(context.Background(), noteHarness, note.ID, UpdateNoteInput{
		Archived: &archive, ExpectedRevision: 1, ClientRequestID: "01989d45-1111-7000-8000-000000000013",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("harness archive error = %v, want ErrForbidden", err)
	}

	archived, err := service.UpdateNote(context.Background(), noteOwner, note.ID, UpdateNoteInput{
		Archived: &archive, ExpectedRevision: 1, ClientRequestID: "01989d45-1111-7000-8000-000000000014",
	})
	if err != nil || !archived.Archived {
		t.Fatalf("owner archive = %#v, %v", archived, err)
	}
	restore := false
	restored, err := service.UpdateNote(context.Background(), noteOwner, note.ID, UpdateNoteInput{
		Archived: &restore, ExpectedRevision: archived.Revision, ClientRequestID: "01989d45-1111-7000-8000-000000000015",
	})
	if err != nil || restored.Archived {
		t.Fatalf("owner restore = %#v, %v", restored, err)
	}
	events, _ := repository.ListNoteAuditEvents(context.Background(), note.ID)
	if events[1].Action != AuditActionNoteArchived || events[2].Action != AuditActionNoteRestored {
		t.Fatalf("actions = %s, %s", events[1].Action, events[2].Action)
	}
}

func TestRunnerCannotTouchNotes(t *testing.T) {
	t.Parallel()
	service, _, _ := newNoteService(t)
	if _, err := service.CreateNote(context.Background(), noteRunner, CreateNoteInput{Document: "A", ClientRequestID: "01989d45-1111-7000-8000-000000000016"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("runner create error = %v", err)
	}
	note, _ := service.CreateNote(context.Background(), noteOwner, CreateNoteInput{Document: "A", ClientRequestID: "01989d45-1111-7000-8000-000000000017"})
	document := "B"
	if _, err := service.UpdateNote(context.Background(), noteRunner, note.ID, UpdateNoteInput{
		Document: &document, ExpectedRevision: 1, ClientRequestID: "01989d45-1111-7000-8000-000000000018",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("runner update error = %v", err)
	}
}

func TestListNotesOrdersByUpdateAndHidesArchived(t *testing.T) {
	t.Parallel()
	service, _, now := newNoteService(t)
	older, _ := service.CreateNote(context.Background(), noteOwner, CreateNoteInput{Document: "Alt", ClientRequestID: "01989d45-1111-7000-8000-000000000019"})
	*now = now.Add(time.Minute)
	newer, _ := service.CreateNote(context.Background(), noteOwner, CreateNoteInput{Document: "Neu", ClientRequestID: "01989d45-1111-7000-8000-000000000020"})
	*now = now.Add(time.Minute)
	gone, _ := service.CreateNote(context.Background(), noteOwner, CreateNoteInput{Document: "Weg", ClientRequestID: "01989d45-1111-7000-8000-000000000021"})
	archive := true
	if _, err := service.UpdateNote(context.Background(), noteOwner, gone.ID, UpdateNoteInput{Archived: &archive, ExpectedRevision: 1, ClientRequestID: "01989d45-1111-7000-8000-000000000022"}); err != nil {
		t.Fatalf("archive error = %v", err)
	}

	notes, err := service.ListNotes(context.Background(), NoteListOptions{})
	if err != nil {
		t.Fatalf("ListNotes() error = %v", err)
	}
	if len(notes) != 2 || notes[0].ID != newer.ID || notes[1].ID != older.ID {
		t.Fatalf("notes = %#v", notes)
	}
	all, _ := service.ListNotes(context.Background(), NoteListOptions{IncludeArchived: true})
	if len(all) != 3 || all[0].ID != gone.ID {
		t.Fatalf("with archive = %#v", all)
	}
}

func TestListNotesSearchesTitleAndPlainText(t *testing.T) {
	t.Parallel()
	service, _, _ := newNoteService(t)
	note, _ := service.CreateNote(context.Background(), noteOwner, CreateNoteInput{Title: "Einkauf", Document: "Milch und **Brot**", ClientRequestID: "01989d45-1111-7000-8000-000000000023"})

	found, err := service.ListNotes(context.Background(), NoteListOptions{Query: "brot"})
	if err != nil || len(found) != 1 || found[0].ID != note.ID {
		t.Fatalf("search brot = %#v, %v", found, err)
	}
	missing, _ := service.ListNotes(context.Background(), NoteListOptions{Query: "xyz"})
	if len(missing) != 0 {
		t.Fatalf("search xyz = %#v", missing)
	}
}

func TestNotePlainTextDropsMarkdownMarkers(t *testing.T) {
	t.Parallel()
	got := NotePlainText("## Plan\n1. `go test` *jetzt*\n- [x] erledigt\n---\n> Zitat\n```\n# code\n```")
	want := "Plan\ngo test jetzt\nerledigt\nZitat\n# code"
	if got != want {
		t.Fatalf("NotePlainText() = %q, want %q", got, want)
	}
}

func TestDeriveNoteSummaryIsBounded(t *testing.T) {
	t.Parallel()
	summary := DeriveNoteSummary("Titel\n" + strings.Repeat("wort ", 100))
	if len([]rune(summary)) > derivedSummaryRunes || !strings.HasSuffix(summary, "…") {
		t.Fatalf("summary length %d: %q", len([]rune(summary)), summary)
	}
}

func TestUpdateNoteRetryReturnsTheStoredResult(t *testing.T) {
	t.Parallel()
	service, repository, _ := newNoteService(t)
	note, _ := service.CreateNote(context.Background(), noteOwner, CreateNoteInput{Document: "A", ClientRequestID: "01989d45-2222-7000-8000-000000000001"})
	document := "B"
	input := UpdateNoteInput{Document: &document, ExpectedRevision: 1, ClientRequestID: "01989d45-2222-7000-8000-000000000002"}
	first, err := service.UpdateNote(context.Background(), noteOwner, note.ID, input)
	if err != nil {
		t.Fatalf("first update error = %v", err)
	}
	retried, err := service.UpdateNote(context.Background(), noteOwner, note.ID, input)
	if err != nil || retried.Revision != first.Revision || retried.Document != "B" {
		t.Fatalf("retry = %#v, %v; want the stored revision 2", retried, err)
	}
	events, _ := repository.ListNoteAuditEvents(context.Background(), note.ID)
	if len(events) != 2 {
		t.Fatalf("retry wrote another event: %d", len(events))
	}
}

func TestNoteAuditSnapshotsStayBounded(t *testing.T) {
	t.Parallel()
	service, repository, _ := newNoteService(t)
	big := "# Gross\n" + strings.Repeat("wort ", 20000)
	note, _ := service.CreateNote(context.Background(), noteOwner, CreateNoteInput{Document: big, ClientRequestID: "01989d45-2222-7000-8000-000000000003"})
	changed := big + "\nneu"
	if _, err := service.UpdateNote(context.Background(), noteOwner, note.ID, UpdateNoteInput{Document: &changed, ExpectedRevision: 1, ClientRequestID: "01989d45-2222-7000-8000-000000000004"}); err != nil {
		t.Fatalf("update error = %v", err)
	}
	events, _ := repository.ListNoteAuditEvents(context.Background(), note.ID)
	for _, event := range events {
		if strings.Contains(string(event.AfterSnapshot), "plain_text") || strings.Contains(string(event.BeforeSnapshot), "plain_text") {
			t.Fatalf("snapshot carries the derived plain text")
		}
		if len(event.BeforeSnapshot) > 2048 {
			t.Fatalf("before snapshot holds the document again: %d bytes", len(event.BeforeSnapshot))
		}
		if !strings.Contains(string(event.AfterSnapshot), "neu") && event.Action == AuditActionNoteUpdated {
			t.Fatalf("after snapshot lost the new document")
		}
	}
}

func TestNotesNeedAReadableTitle(t *testing.T) {
	t.Parallel()
	service, _, _ := newNoteService(t)
	for index, document := range []string{"---", "```\n```", "***", "#"} {
		_, err := service.CreateNote(context.Background(), noteOwner, CreateNoteInput{Document: document, ClientRequestID: "01989d45-2222-7000-8000-00000000010" + string(rune('0'+index))})
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("document %q error = %v, want ErrInvalidInput", document, err)
		}
	}
}

func TestNoteTitlesDropControlCharacters(t *testing.T) {
	t.Parallel()
	service, _, _ := newNoteService(t)
	note, err := service.CreateNote(context.Background(), noteOwner, CreateNoteInput{
		Title: "line1\nline2\x1b[31m", Summary: "a\tb\x07", Document: "x", ClientRequestID: "01989d45-2222-7000-8000-000000000020",
	})
	if err != nil {
		t.Fatalf("CreateNote() error = %v", err)
	}
	if note.Title != "line1 line2[31m" || note.Summary != "a b" {
		t.Fatalf("title %q summary %q", note.Title, note.Summary)
	}
}

func TestNoteUpdateNeedsARevision(t *testing.T) {
	t.Parallel()
	service, _, _ := newNoteService(t)
	note, _ := service.CreateNote(context.Background(), noteOwner, CreateNoteInput{Document: "A", ClientRequestID: "01989d45-2222-7000-8000-000000000030"})
	document := "B"
	if _, err := service.UpdateNote(context.Background(), noteOwner, note.ID, UpdateNoteInput{Document: &document, ClientRequestID: "01989d45-2222-7000-8000-000000000031"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing revision error = %v, want ErrInvalidInput", err)
	}
}

func TestNotePlainTextKeepsOrdinaryCharacters(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"2 * 3 = 6 and a*b":             "2 * 3 = 6 and a*b",
		"snake_case_name":               "snake_case_name",
		"*kursiv* und **fett**":         "kursiv und fett",
		"_kursiv_ und __fett__":         "kursiv und fett",
		"~~alt~~ und `code`":            "alt und code",
		"~~~\n# keine Überschrift\n~~~": "# keine Überschrift",
		"#":                             "",
	}
	for input, want := range cases {
		if got := NotePlainText(input); got != want {
			t.Errorf("NotePlainText(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestArchivingALegacyNoteWithoutTitleStillWorks(t *testing.T) {
	t.Parallel()
	service, repository, now := newNoteService(t)
	legacy := Note{ID: "01989d45-3333-7000-8000-000000000001", Document: "---", Revision: 1, CreatedAt: *now, UpdatedAt: *now,
		TitleSource: NoteFieldSourceDerived, SummarySource: NoteFieldSourceDerived}
	if _, err := repository.CreateNote(context.Background(), legacy, AuditEvent{ID: "e", NoteID: legacy.ID, Action: AuditActionNoteCreated, Actor: noteOwner}, "01989d45-3333-7000-8000-000000000002"); err != nil {
		t.Fatal(err)
	}
	archive := true
	if _, err := service.UpdateNote(context.Background(), noteOwner, legacy.ID, UpdateNoteInput{Archived: &archive, ExpectedRevision: 1, ClientRequestID: "01989d45-3333-7000-8000-000000000003"}); err != nil {
		t.Fatalf("archive legacy note error = %v", err)
	}
}

func TestARequestIDBelongsToOneNote(t *testing.T) {
	t.Parallel()
	service, _, _ := newNoteService(t)
	first, _ := service.CreateNote(context.Background(), noteOwner, CreateNoteInput{Document: "A", ClientRequestID: "01989d45-3333-7000-8000-000000000010"})
	second, _ := service.CreateNote(context.Background(), noteOwner, CreateNoteInput{Document: "B", ClientRequestID: "01989d45-3333-7000-8000-000000000011"})
	document := "A2"
	shared := "01989d45-3333-7000-8000-000000000012"
	if _, err := service.UpdateNote(context.Background(), noteOwner, first.ID, UpdateNoteInput{Document: &document, ExpectedRevision: 1, ClientRequestID: shared}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateNote(context.Background(), noteOwner, second.ID, UpdateNoteInput{Document: &document, ExpectedRevision: 1, ClientRequestID: shared}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("reusing a request ID on another note error = %v, want ErrInvalidInput", err)
	}
}

func TestDerivedTitlesCarryNoControlCharacters(t *testing.T) {
	t.Parallel()
	if title := DeriveNoteTitle("Titel\x1b[31m rot\x07"); title != "Titel[31m rot" {
		t.Fatalf("derived title = %q", title)
	}
}

func TestBriefingHidesNoteEventsFromRunners(t *testing.T) {
	t.Parallel()
	service, _, _ := newNoteService(t)
	if _, err := service.CreateNote(context.Background(), noteOwner, CreateNoteInput{Document: "Geheim", ClientRequestID: "01989d45-3333-7000-8000-000000000020"}); err != nil {
		t.Fatal(err)
	}
	briefing, err := service.GetBriefing(context.Background(), BriefingOptions{Viewer: noteRunner})
	if err != nil || len(briefing.Changes) != 0 || strings.Contains(briefing.Summary, "1 changes") {
		t.Fatalf("runner briefing = %#v, %v", briefing, err)
	}
	ownerBriefing, _ := service.GetBriefing(context.Background(), BriefingOptions{Viewer: noteOwner})
	if len(ownerBriefing.Changes) != 1 {
		t.Fatalf("owner briefing changes = %d, want 1", len(ownerBriefing.Changes))
	}
}

func TestNotePlainTextFormats(t *testing.T) {
	cases := map[string]string{
		"++unter++ ==hell== ~~weg~~":                      "unter hell weg",
		"| A | B |\n|---|:-:|\n| 1 | **2** |":             "A B\n1 2",
		"a ++ b == c":                                     "a ++ b == c",
		"| nur eine Zelle |":                              "nur eine Zelle",
		"Text | mit Strich":                               "Text | mit Strich",
		"| Spalte 1 | Spalte 2 |\n| --- | --- |\n|  |  |": "Spalte 1 Spalte 2",
	}
	for document, want := range cases {
		if got := NotePlainText(document); got != want {
			t.Errorf("NotePlainText(%q) = %q, want %q", document, got, want)
		}
	}
}
