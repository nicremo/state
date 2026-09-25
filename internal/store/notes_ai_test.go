package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nicremo/state/internal/state"
)

func newStoreAIService(t *testing.T) (*state.Service, *PocketBaseRepository, string, *time.Time) {
	t.Helper()
	dataDirectory := t.TempDir()
	repository, err := NewPocketBaseRepository(bootstrappedApp(t, dataDirectory), deterministicSigningKey())
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() error = %v", err)
	}
	clock := time.Date(2026, time.September, 25, 10, 0, 0, 0, time.UTC)
	service := state.NewService(repository,
		state.WithClock(func() time.Time { return clock }),
		state.WithNoteAI(func() bool { return true }, state.DefaultNoteMediaPolicy),
	)
	return service, repository, dataDirectory, &clock
}

func TestNoteAIDataPersistsIsSearchableAndKeepsTheChainValid(t *testing.T) {
	t.Parallel()
	service, repository, dataDirectory, _ := newStoreAIService(t)
	ctx := context.Background()

	note, err := service.CreateNote(ctx, storeNoteOwner, state.CreateNoteInput{Capture: state.NoteCaptureImage, ClientRequestID: "c1"})
	if err != nil {
		t.Fatalf("CreateNote() error = %v", err)
	}
	other, _ := service.CreateNote(ctx, storeNoteOwner, state.CreateNoteInput{Document: "Karla Zahlen", ClientRequestID: "c2"})
	attachment, err := service.AddNoteAttachment(ctx, storeNoteOwner, note.ID, state.AddNoteAttachmentInput{
		Kind: "image", MimeType: "image/jpeg", ByteSize: 10, SHA256: strings.Repeat("c", 64), ClientRequestID: "a1",
	})
	if err != nil {
		t.Fatalf("AddNoteAttachment() error = %v", err)
	}
	replay, err := service.AddNoteAttachment(ctx, storeNoteOwner, note.ID, state.AddNoteAttachmentInput{
		Kind: "image", MimeType: "image/jpeg", ByteSize: 10, SHA256: strings.Repeat("c", 64), ClientRequestID: "a1",
	})
	if err != nil || replay.ID != attachment.ID {
		t.Fatalf("replay = %v %v", replay.ID, err)
	}
	outcome := state.NoteAgentOutcome{
		Title: "Handschrift Seite", Summary: "Seite aus dem Notizbuch", Document: "# Seite\nZauberwort Quittenbrot", Model: "deepseek/deepseek-v4.1-flash",
		AttachmentTexts: map[string]state.NoteAttachmentText{attachment.ID: {Text: "Zauberwort Quittenbrot handschriftlich", Kind: "ocr", Model: "m"}},
		Relations:       []state.NoteRelation{{RelatedNoteID: other.ID, Reason: "Karla", Confidence: 0.7}},
		Proposals:       []state.ReminderProposal{{Title: "Quittenbrot backen", LocalDate: "2026-10-01"}},
	}
	if err := service.ApplyNoteAgentOutcome(ctx, state.NoteJob{ID: "j1", NoteID: note.ID}, note.Revision, outcome); err != nil {
		t.Fatalf("ApplyNoteAgentOutcome() error = %v", err)
	}
	if err := service.AddNoteAIUsage(ctx, 0.25); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"handschriftlich", "Notizbuch", "Quittenbrot"} {
		found, err := service.ListNoteViews(ctx, state.NoteListOptions{Query: query})
		if err != nil || len(found) != 1 || found[0].ID != note.ID {
			t.Fatalf("search %q = %d notes, %v", query, len(found), err)
		}
	}
	if err := repository.VerifyAuditChain(ctx); err != nil {
		t.Fatalf("audit chain: %v", err)
	}

	// Everything survives a restart.
	reopened, err := NewPocketBaseRepository(bootstrappedApp(t, dataDirectory), deterministicSigningKey())
	if err != nil {
		t.Fatal(err)
	}
	restarted := state.NewService(reopened, state.WithNoteAI(func() bool { return true }, state.DefaultNoteMediaPolicy))
	view, err := restarted.GetNoteView(ctx, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.TitleSource != state.NoteFieldSourceAI || view.Attachments[0].DerivedKind != "ocr" || len(view.Relations) != 1 || len(view.Proposals) != 1 {
		t.Fatalf("after restart = %+v", view)
	}
	otherView, _ := restarted.GetNoteView(ctx, other.ID)
	if len(otherView.Relations) != 1 || otherView.Relations[0].RelatedNoteID != note.ID {
		t.Fatalf("relation from the other side = %+v", otherView.Relations)
	}
	settings, _ := restarted.GetNoteAISettings(ctx)
	if settings.SpentThisMonthUSD != 0.25 {
		t.Fatalf("spent = %v, want 0.25", settings.SpentThisMonthUSD)
	}
}

func TestNoteJobsAreClaimedOnceAndDebounced(t *testing.T) {
	t.Parallel()
	service, _, _, clock := newStoreAIService(t)
	ctx := context.Background()
	consent := true
	if _, err := service.UpdateNoteAISettings(ctx, storeNoteOwner, state.UpdateNoteAISettingsInput{Consent: &consent}); err != nil {
		t.Fatal(err)
	}
	note, _ := service.CreateNote(ctx, storeNoteOwner, state.CreateNoteInput{Document: "Erste Fassung", ClientRequestID: "n"})
	if _, found, _ := service.ClaimNoteJob(ctx); found {
		t.Fatal("organize job ran before the quiet period")
	}
	*clock = clock.Add(30 * time.Second)
	document := "Zweite Fassung"
	if _, err := service.UpdateNote(ctx, storeNoteOwner, note.ID, state.UpdateNoteInput{Document: &document, ExpectedRevision: 1, ClientRequestID: "u"}); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(30 * time.Second)
	if _, found, _ := service.ClaimNoteJob(ctx); found {
		t.Fatal("typing did not push the organize job later")
	}
	if _, err := service.RequestNoteProcessing(ctx, storeNoteOwner, note.ID, "p"); err != nil {
		t.Fatal(err)
	}
	job, found, err := service.ClaimNoteJob(ctx)
	if err != nil || !found || job.Kind != state.NoteJobProcess {
		t.Fatalf("claim = %+v %v %v", job, found, err)
	}
	if _, found, _ := service.ClaimNoteJob(ctx); found {
		t.Fatal("two jobs for one note")
	}
	retry := clock.Add(time.Minute)
	if err := service.FinishNoteJob(ctx, job, state.NoteJobQueued, &retry); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(2 * time.Minute)
	again, found, _ := service.ClaimNoteJob(ctx)
	if !found || again.Attempts != 1 {
		t.Fatalf("retry = %+v %v", again, found)
	}
	if err := service.RequeueRunningNoteJobs(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := service.ClaimNoteJob(ctx); !found {
		t.Fatal("a job left running by a crash was not requeued")
	}
}

func TestNotesDictionaryPersistsWithRevisionAndAudit(t *testing.T) {
	t.Parallel()
	service, repository, dataDirectory, _ := newStoreAIService(t)
	ctx := context.Background()

	seeded, err := service.SeedNotesDictionary(ctx, "word: Supabase\nalways: ZEVDISK -> sevDesk\ncontext: Note -> Node\n")
	if err != nil || !seeded {
		t.Fatalf("SeedNotesDictionary() = %v, %v", seeded, err)
	}
	revision := int64(1)
	updated, err := service.UpdateNotesDictionary(ctx, storeNoteOwner, state.UpdateNotesDictionaryInput{
		Words:            []string{"Supabase", "Vercel"},
		Corrections:      []state.DictionaryCorrection{{From: "ZEVDISK", To: "sevDesk", Mode: state.DictionaryModeAlways}},
		ExpectedRevision: &revision,
	})
	if err != nil || updated.Revision != 2 {
		t.Fatalf("UpdateNotesDictionary() = %#v, %v", updated, err)
	}
	stale := state.NotesDictionary{Words: []string{"x"}, Revision: 2}
	if err := repository.SaveNotesDictionary(ctx, stale, state.AuditEvent{}); err != state.ErrRevisionConflict {
		t.Fatalf("stale save error = %v, want revision conflict", err)
	}
	if err := repository.VerifyAuditChain(ctx); err != nil {
		t.Fatalf("audit chain: %v", err)
	}

	reopened, err := NewPocketBaseRepository(bootstrappedApp(t, dataDirectory), deterministicSigningKey())
	if err != nil {
		t.Fatal(err)
	}
	restarted := state.NewService(reopened)
	dictionary, err := restarted.GetNotesDictionary(ctx)
	if err != nil || dictionary.Revision != 2 || strings.Join(dictionary.Words, "|") != "Supabase|Vercel" || len(dictionary.Corrections) != 1 {
		t.Fatalf("after restart = %#v, %v", dictionary, err)
	}
	if again, err := restarted.SeedNotesDictionary(ctx, "word: Anderes"); err != nil || again {
		t.Fatalf("seed after save = %v, %v", again, err)
	}
}
