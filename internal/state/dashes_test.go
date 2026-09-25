package state

import (
	"context"
	"strings"
	"testing"
)

const (
	enDash = "–"
	emDash = "—"
)

func TestRemoveDashes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		input string
		title bool
		want  string
	}{
		{"title from the screenshot", "Nerviger Tag " + enDash + " Cloud.md angepasst", true, "Nerviger Tag: Cloud.md angepasst"},
		{"second dash in a title", "Plan " + enDash + " Karla " + emDash + " Report", true, "Plan: Karla, Report"},
		{"prose", "Heute " + enDash + " wie immer " + enDash + " nervig", false, "Heute, wie immer, nervig"},
		{"number range", "10" + enDash + "12 Uhr", false, "10-12 Uhr"},
		{"list line", "## Einkauf\n" + enDash + " Milch\n" + emDash + " Brot", false, "## Einkauf\n- Milch\n- Brot"},
		{"trailing dash", "Ende " + emDash, false, "Ende"},
		{"unspaced between words", "Wort" + emDash + "Wort", false, "Wort, Wort"},
		{"dash before punctuation", "Fertig " + enDash + ".", false, "Fertig."},
		{"hyphens stay", "E-Mail an Karla, 2026-10-02", false, "E-Mail an Karla, 2026-10-02"},
		{"table stays", "| a | b |\n| --- | --- |\n| 1 | 2 |", false, "| a | b |\n| --- | --- |\n| 1 | 2 |"},
		{"no dash", "Nichts zu tun", true, "Nichts zu tun"},
	} {
		if got := RemoveDashes(test.input, test.title); got != test.want {
			t.Errorf("%s: RemoveDashes(%q) = %q, want %q", test.name, test.input, got, test.want)
		}
	}
}

func hasDash(text string) bool {
	return strings.Contains(text, enDash) || strings.Contains(text, emDash)
}

func TestApplyOutcomeRemovesDashes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, _, _ := newAINoteService(t, true)
	note := createCaptureNote(t, service, NoteCaptureAudio)
	other, err := service.CreateNote(ctx, noteDevice, CreateNoteInput{Document: "Karla Report", ClientRequestID: "other"})
	if err != nil {
		t.Fatal(err)
	}
	outcome := NoteAgentOutcome{
		Title:    "Nerviger Tag " + enDash + " Cloud.md angepasst",
		Summary:  "Der Tag war nervig " + emDash + " aber produktiv.",
		Document: "- Cloud.md angepasst " + enDash + " läuft besser\n" + enDash + " Report 10" + enDash + "12 Uhr",
		Model:    "m",
		Relations: []NoteRelation{
			{RelatedNoteID: other.ID, Reason: "Beide " + enDash + " Karla"},
		},
		Proposals: []ReminderProposal{{
			Title:       "Report " + enDash + " Karla",
			Description: "Zahlen " + emDash + " bis Freitag",
			Reason:      "Im Text " + enDash + " Freitag",
		}},
	}
	if err := service.ApplyNoteAgentOutcome(ctx, NoteJob{ID: "dash", NoteID: note.ID, Kind: NoteJobProcess}, note.Revision, outcome); err != nil {
		t.Fatalf("ApplyNoteAgentOutcome() error = %v", err)
	}
	view, err := service.GetNoteView(ctx, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.AI == nil || hasDash(view.AI.Title) || hasDash(view.AI.Summary) || hasDash(view.Document) {
		t.Fatalf("dash kept: ai=%#v document=%q", view.AI, view.Document)
	}
	if view.AI.Title != "Nerviger Tag: Cloud.md angepasst" {
		t.Fatalf("title = %q", view.AI.Title)
	}
	if len(view.Relations) != 1 || hasDash(view.Relations[0].Reason) {
		t.Fatalf("relations = %#v", view.Relations)
	}
	if len(view.Proposals) != 1 {
		t.Fatalf("proposals = %#v", view.Proposals)
	}
	proposal := view.Proposals[0]
	if hasDash(proposal.Title) || hasDash(proposal.Description) || hasDash(proposal.Reason) {
		t.Fatalf("proposal kept a dash: %#v", proposal)
	}
}

func TestApplyOutcomeRemovesDashesFromProposedDocument(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, _, _ := newAINoteService(t, true)
	note := createCaptureNote(t, service, NoteCaptureAudio)
	typed := "Eigener Text"
	if _, err := service.UpdateNote(ctx, noteDevice, note.ID, UpdateNoteInput{Document: &typed, ExpectedRevision: note.Revision, ClientRequestID: "edit"}); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyNoteAgentOutcome(ctx, NoteJob{ID: "j", NoteID: note.ID, Kind: NoteJobProcess}, note.Revision, NoteAgentOutcome{Title: "T", Document: "- A " + enDash + " B", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	view, _ := service.GetNoteView(ctx, note.ID)
	if view.AI == nil || view.AI.ProposedDocument != "- A, B" {
		t.Fatalf("proposed document = %#v", view.AI)
	}
}
