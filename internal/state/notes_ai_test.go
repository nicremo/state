package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var noteDevice = Actor{ID: "01989d45-0000-7000-8000-000000000004", Kind: ActorKindDevice, DisplayName: "iPhone"}

func newAINoteService(t *testing.T, available bool) (*Service, *MemoryRepository, *time.Time) {
	t.Helper()
	repository := NewMemoryRepository()
	now := time.Date(2026, time.September, 25, 10, 0, 0, 0, time.UTC)
	service := NewService(repository,
		WithClock(func() time.Time { return now }),
		WithNoteAI(func() bool { return available }, func() NoteMediaPolicy { return DefaultNoteMediaPolicy() }),
	)
	return service, repository, &now
}

func giveConsent(t *testing.T, service *Service) {
	t.Helper()
	consent := true
	if _, err := service.UpdateNoteAISettings(context.Background(), noteOwner, UpdateNoteAISettingsInput{Consent: &consent}); err != nil {
		t.Fatalf("UpdateNoteAISettings() error = %v", err)
	}
}

func createCaptureNote(t *testing.T, service *Service, capture NoteCapture) Note {
	t.Helper()
	note, err := service.CreateNote(context.Background(), noteDevice, CreateNoteInput{Capture: capture, ClientRequestID: "capture-" + string(capture)})
	if err != nil {
		t.Fatalf("CreateNote(%s) error = %v", capture, err)
	}
	return note
}

func attachImage(t *testing.T, service *Service, noteID string, ordinal int) (NoteAttachment, error) {
	t.Helper()
	return service.AddNoteAttachment(context.Background(), noteDevice, noteID, AddNoteAttachmentInput{
		Kind:            NoteAttachmentImage,
		MimeType:        "image/jpeg",
		ByteSize:        1000,
		SHA256:          strings.Repeat("a", 63) + string(rune('0'+ordinal%10)),
		Ordinal:         ordinal,
		ClientRequestID: "attach-" + noteID + "-" + string(rune('a'+ordinal)),
	})
}

func TestCaptureNoteWithoutTextIsAllowed(t *testing.T) {
	t.Parallel()
	service, _, _ := newAINoteService(t, true)
	note := createCaptureNote(t, service, NoteCaptureImage)
	if note.Title != "" || note.Capture != NoteCaptureImage || note.TitleSource != NoteFieldSourceDerived {
		t.Fatalf("capture note = %#v", note)
	}
	if _, err := service.CreateNote(context.Background(), noteDevice, CreateNoteInput{Capture: "video", ClientRequestID: "bad"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown capture error = %v", err)
	}
}

func TestTextNoteStillNeedsTitle(t *testing.T) {
	t.Parallel()
	service, _, _ := newAINoteService(t, true)
	if _, err := service.CreateNote(context.Background(), noteDevice, CreateNoteInput{Document: "---", ClientRequestID: "x"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want invalid input", err)
	}
}

func TestAttachmentIdempotentAndRunnerForbidden(t *testing.T) {
	t.Parallel()
	service, repository, _ := newAINoteService(t, true)
	note := createCaptureNote(t, service, NoteCaptureImage)
	first, err := attachImage(t, service, note.ID, 0)
	if err != nil {
		t.Fatalf("AddNoteAttachment() error = %v", err)
	}
	again, err := attachImage(t, service, note.ID, 0)
	if err != nil || again.ID != first.ID {
		t.Fatalf("retry = %#v, %v; want %s", again, err, first.ID)
	}
	view, _ := service.GetNoteView(context.Background(), note.ID)
	if len(view.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(view.Attachments))
	}
	if _, err := service.AddNoteAttachment(context.Background(), noteRunner, note.ID, AddNoteAttachmentInput{Kind: NoteAttachmentImage, MimeType: "image/jpeg", ByteSize: 1, SHA256: strings.Repeat("b", 64), ClientRequestID: "r"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("runner error = %v", err)
	}
	events, _ := repository.ListNoteAuditEvents(context.Background(), note.ID)
	if last := events[len(events)-1]; last.Action != AuditActionNoteAttachmentAdded || strings.Contains(string(last.AfterSnapshot), "path") {
		t.Fatalf("attachment event = %s %s", last.Action, last.AfterSnapshot)
	}
}

func TestImageLimitEnforcedByService(t *testing.T) {
	t.Parallel()
	service, _, _ := newAINoteService(t, true)
	note := createCaptureNote(t, service, NoteCaptureImage)
	limit := DefaultNoteMediaPolicy().MaxImages
	for ordinal := 0; ordinal < limit; ordinal++ {
		if _, err := attachImage(t, service, note.ID, ordinal); err != nil {
			t.Fatalf("image %d error = %v", ordinal, err)
		}
	}
	if _, err := attachImage(t, service, note.ID, limit); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("image over limit error = %v", err)
	}
}

func TestProcessingNeedsKeyAndConsent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	unavailable, _, _ := newAINoteService(t, false)
	note := createCaptureNote(t, unavailable, NoteCaptureAudio)
	processing, err := unavailable.RequestNoteProcessing(ctx, noteDevice, note.ID, "p1")
	if err != nil || processing.Status != NoteProcessingNotConfigured {
		t.Fatalf("without key = %#v, %v", processing, err)
	}

	service, _, _ := newAINoteService(t, true)
	note = createCaptureNote(t, service, NoteCaptureAudio)
	processing, err = service.RequestNoteProcessing(ctx, noteDevice, note.ID, "p1")
	if err != nil || processing.Status != NoteProcessingConsentRequired {
		t.Fatalf("without consent = %#v, %v", processing, err)
	}
	giveConsent(t, service)
	processing, err = service.RequestNoteProcessing(ctx, noteDevice, note.ID, "p2")
	if err != nil || processing.Status != NoteProcessingQueued {
		t.Fatalf("with consent = %#v, %v", processing, err)
	}
	job, found, err := service.ClaimNoteJob(ctx)
	if err != nil || !found || job.NoteID != note.ID || job.Kind != NoteJobProcess {
		t.Fatalf("claim = %#v %v %v", job, found, err)
	}
	if _, found, _ := service.ClaimNoteJob(ctx); found {
		t.Fatal("a job was handed out twice")
	}
	if again, _ := service.RequestNoteProcessing(ctx, noteDevice, note.ID, "p2"); again.Status != NoteProcessingQueued {
		t.Fatalf("retry of the same request = %#v", again)
	}
}

func TestAITitleShownOnlyForDerivedTitle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, _, _ := newAINoteService(t, true)
	derived, _ := service.CreateNote(ctx, noteDevice, CreateNoteInput{Document: "einkauf milch brot", ClientRequestID: "d"})
	written, _ := service.CreateNote(ctx, noteDevice, CreateNoteInput{Title: "Mein Titel", Document: "text", ClientRequestID: "w"})
	for _, note := range []Note{derived, written} {
		job := NoteJob{ID: "job-" + note.ID, NoteID: note.ID, Kind: NoteJobOrganize}
		if err := service.ApplyNoteAgentOutcome(ctx, job, note.Revision, NoteAgentOutcome{Title: "KI Titel", Summary: "KI Zusammenfassung", Model: "deepseek/deepseek-v4.1-flash"}); err != nil {
			t.Fatalf("ApplyNoteAgentOutcome() error = %v", err)
		}
	}
	view, _ := service.GetNoteView(ctx, derived.ID)
	if view.Title != "KI Titel" || view.TitleSource != NoteFieldSourceAI || view.Summary != "KI Zusammenfassung" || view.SummarySource != NoteFieldSourceAI {
		t.Fatalf("derived note view = %q (%s) %q (%s)", view.Title, view.TitleSource, view.Summary, view.SummarySource)
	}
	if view.AI == nil || view.AI.Model != "deepseek/deepseek-v4.1-flash" || view.Processing.Status != NoteProcessingReady {
		t.Fatalf("provenance = %#v %#v", view.AI, view.Processing)
	}
	view, _ = service.GetNoteView(ctx, written.ID)
	if view.Title != "Mein Titel" || view.TitleSource != NoteFieldSourceUser {
		t.Fatalf("written title replaced: %q (%s)", view.Title, view.TitleSource)
	}
	found, _ := service.ListNoteViews(ctx, NoteListOptions{Query: "Zusammenfassung"})
	if len(found) != 2 {
		t.Fatalf("search over AI summary found %d notes, want 2", len(found))
	}
}

func TestApplyFillsEmptyCaptureDocument(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, repository, _ := newAINoteService(t, true)
	note := createCaptureNote(t, service, NoteCaptureImage)
	attachment, _ := attachImage(t, service, note.ID, 0)
	outcome := NoteAgentOutcome{
		Title: "Meeting mit Karla", Summary: "Notizen aus dem Buch", Document: "# Meeting mit Karla\n- [ ] Report schicken",
		Model:           "deepseek/deepseek-v4.1-flash",
		AttachmentTexts: map[string]NoteAttachmentText{attachment.ID: {Text: "Meeting mit Karla, Report schicken", Kind: "ocr", Model: "deepseek/deepseek-v4.1-flash"}},
	}
	if err := service.ApplyNoteAgentOutcome(ctx, NoteJob{ID: "j1", NoteID: note.ID, Kind: NoteJobProcess}, note.Revision, outcome); err != nil {
		t.Fatalf("ApplyNoteAgentOutcome() error = %v", err)
	}
	view, _ := service.GetNoteView(ctx, note.ID)
	if !strings.Contains(view.Document, "Report schicken") || view.Revision != 2 {
		t.Fatalf("document not filled: rev %d %q", view.Revision, view.Document)
	}
	if view.Attachments[0].DerivedText == "" || view.Attachments[0].DerivedKind != "ocr" {
		t.Fatalf("attachment text = %#v", view.Attachments[0])
	}
	events, _ := repository.ListNoteAuditEvents(ctx, note.ID)
	var sawAgent bool
	for _, event := range events {
		if event.Actor.ID == NotesAgentActor().ID {
			sawAgent = true
		}
	}
	if !sawAgent {
		t.Fatal("no audit event by the notes agent")
	}
}

func TestApplyKeepsEditedDocumentAsProposal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, _, _ := newAINoteService(t, true)
	note := createCaptureNote(t, service, NoteCaptureImage)
	typed := "Eigener Text"
	edited, err := service.UpdateNote(ctx, noteDevice, note.ID, UpdateNoteInput{Document: &typed, ExpectedRevision: note.Revision, ClientRequestID: "edit"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyNoteAgentOutcome(ctx, NoteJob{ID: "j", NoteID: note.ID, Kind: NoteJobProcess}, note.Revision, NoteAgentOutcome{Title: "T", Document: "KI Text", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	view, _ := service.GetNoteView(ctx, note.ID)
	if view.Document != typed || view.Revision != edited.Revision {
		t.Fatalf("owner text overwritten: %q rev %d", view.Document, view.Revision)
	}
	if view.AI == nil || view.AI.ProposedDocument != "KI Text" {
		t.Fatalf("proposal missing: %#v", view.AI)
	}
}

func TestAgentRelationsAndProposalsAreBounded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, _, _ := newAINoteService(t, true)
	note, _ := service.CreateNote(ctx, noteDevice, CreateNoteInput{Document: "Karla Report", ClientRequestID: "a"})
	other, _ := service.CreateNote(ctx, noteDevice, CreateNoteInput{Document: "Karla Zahlen", ClientRequestID: "b"})
	outcome := NoteAgentOutcome{
		Title: "Report", Model: "m",
		Relations: []NoteRelation{
			{RelatedNoteID: other.ID, Reason: "Beide über Karla", Confidence: 0.8},
			{RelatedNoteID: other.ID, Reason: "doppelt"},
			{RelatedNoteID: note.ID, Reason: "sich selbst"},
			{RelatedNoteID: "missing", Reason: "gibt es nicht"},
		},
		Proposals: []ReminderProposal{{Title: "Report schicken", LocalDate: "2026-10-02", LocalTime: "09:00", Reason: "Im Text steht: am 2. schicken"}, {Title: "", LocalDate: "x"}},
	}
	if err := service.ApplyNoteAgentOutcome(ctx, NoteJob{ID: "j", NoteID: note.ID, Kind: NoteJobOrganize}, note.Revision, outcome); err != nil {
		t.Fatal(err)
	}
	view, _ := service.GetNoteView(ctx, note.ID)
	if len(view.Relations) != 1 || view.Relations[0].RelatedNoteID != other.ID || view.Relations[0].RelatedTitle == "" {
		t.Fatalf("relations = %#v", view.Relations)
	}
	otherView, _ := service.GetNoteView(ctx, other.ID)
	if len(otherView.Relations) != 1 || otherView.Relations[0].RelatedNoteID != note.ID {
		t.Fatalf("relation not visible from the other note: %#v", otherView.Relations)
	}
	if otherView.Relations[0].RelatedTitle != "Report" {
		t.Fatalf("related note shows %q, not the AI title the owner sees", otherView.Relations[0].RelatedTitle)
	}
	if len(view.Proposals) != 1 || view.Proposals[0].Status != ReminderProposalPending {
		t.Fatalf("proposals = %#v", view.Proposals)
	}
	reminders, _ := service.ListReminders(ctx, ReminderListOptions{})
	if len(reminders) != 0 {
		t.Fatal("the agent created a reminder without confirmation")
	}
}

func TestAcceptProposalCreatesReminderOnlyForOwnerOrDevice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, _, _ := newAINoteService(t, true)
	note, _ := service.CreateNote(ctx, noteDevice, CreateNoteInput{Document: "Karla Report", ClientRequestID: "a"})
	_ = service.ApplyNoteAgentOutcome(ctx, NoteJob{ID: "j", NoteID: note.ID}, note.Revision, NoteAgentOutcome{Title: "R", Model: "m", Proposals: []ReminderProposal{{Title: "Report schicken", LocalDate: "2026-10-02", LocalTime: "09:00", Reason: "steht im Text"}}})
	view, _ := service.GetNoteView(ctx, note.ID)
	proposal := view.Proposals[0]
	input := AcceptReminderProposalInput{TimeZone: "Europe/Berlin", ClientRequestID: "accept-1"}
	if _, err := service.AcceptReminderProposal(ctx, noteHarness, note.ID, proposal.ID, input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("harness accept error = %v", err)
	}
	reminder, err := service.AcceptReminderProposal(ctx, noteDevice, note.ID, proposal.ID, input)
	if err != nil || reminder.Title != "Report schicken" {
		t.Fatalf("accept = %#v, %v", reminder, err)
	}
	if !strings.Contains(reminder.Description, "📝 R") {
		t.Fatalf("reminder does not name its note by the title the owner sees: %q", reminder.Description)
	}
	again, err := service.AcceptReminderProposal(ctx, noteDevice, note.ID, proposal.ID, input)
	if err != nil || again.ID != reminder.ID {
		t.Fatalf("second accept = %#v, %v", again, err)
	}
	view, _ = service.GetNoteView(ctx, note.ID)
	if view.Proposals[0].Status != ReminderProposalAccepted || view.Proposals[0].ReminderID != reminder.ID {
		t.Fatalf("proposal after accept = %#v", view.Proposals[0])
	}
}

func TestHarnessCannotChangeAISettings(t *testing.T) {
	t.Parallel()
	service, _, _ := newAINoteService(t, true)
	limit := 25.0
	if _, err := service.UpdateNoteAISettings(context.Background(), noteHarness, UpdateNoteAISettingsInput{MonthlyLimitUSD: &limit}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("harness error = %v", err)
	}
	bad := -1.0
	if _, err := service.UpdateNoteAISettings(context.Background(), noteOwner, UpdateNoteAISettingsInput{MonthlyLimitUSD: &bad}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("negative limit error = %v", err)
	}
	settings, err := service.GetNoteAISettings(context.Background())
	if err != nil || settings.MonthlyLimitUSD != 10 || settings.Consent || settings.AgentModel != DefaultNotesAgentModel || settings.TranscriptionModel != DefaultTranscriptionModel {
		t.Fatalf("defaults = %#v, %v", settings, err)
	}
}

func TestOrganizeJobQueuedOnPlainTextChangeOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, _, now := newAINoteService(t, true)
	giveConsent(t, service)
	note, _ := service.CreateNote(ctx, noteDevice, CreateNoteInput{Document: "Liste\n- [ ] Milch", ClientRequestID: "c"})
	*now = now.Add(time.Minute)
	job, found, _ := service.ClaimNoteJob(ctx)
	if !found || job.Kind != NoteJobOrganize {
		t.Fatalf("no organize job after create: %#v", job)
	}
	_ = service.FinishNoteJob(ctx, job, NoteJobDone, nil)

	ticked := "Liste\n- [x] Milch"
	if _, err := service.UpdateNote(ctx, noteDevice, note.ID, UpdateNoteInput{Document: &ticked, ExpectedRevision: 1, ClientRequestID: "tick"}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	if _, found, _ := service.ClaimNoteJob(ctx); found {
		t.Fatal("ticking a checklist item queued a paid AI job")
	}
	changed := "Liste\n- [x] Milch\n- [ ] Brot"
	if _, err := service.UpdateNote(ctx, noteDevice, note.ID, UpdateNoteInput{Document: &changed, ExpectedRevision: 2, ClientRequestID: "brot"}); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := service.ClaimNoteJob(ctx); found {
		t.Fatal("organize job ran before the quiet period")
	}
	*now = now.Add(time.Minute)
	if _, found, _ := service.ClaimNoteJob(ctx); !found {
		t.Fatal("no organize job after the text changed")
	}
}

func TestRunnerNeverSeesAIEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, repository, _ := newAINoteService(t, true)
	note := createCaptureNote(t, service, NoteCaptureImage)
	_, _ = attachImage(t, service, note.ID, 0)
	_ = service.ApplyNoteAgentOutcome(ctx, NoteJob{ID: "j", NoteID: note.ID}, note.Revision, NoteAgentOutcome{Title: "T", Model: "m"})
	changes, _ := repository.ListChanges(ctx, 0, 100)
	for _, change := range VisibleChanges(noteRunner, changes) {
		if strings.HasPrefix(string(change.Event.Action), "note") || strings.HasPrefix(string(change.Event.Action), "notes_ai") {
			t.Fatalf("runner sees %s", change.Event.Action)
		}
	}
}
