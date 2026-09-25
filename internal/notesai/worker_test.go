package notesai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nicremo/state/internal/notesai"
	"github.com/nicremo/state/internal/notesai/fakeopenrouter"
	"github.com/nicremo/state/internal/state"
)

var (
	owner  = state.Actor{ID: "owner-1", Kind: state.ActorKindOwner, DisplayName: "Fabian"}
	device = state.Actor{ID: "device-1", Kind: state.ActorKindDevice, DisplayName: "iPhone"}
	runner = state.Actor{ID: "runner-1", Kind: state.ActorKindRunner, DisplayName: "Mac"}
	jpeg   = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte("p"), 64)...)
	m4a    = append([]byte("\x00\x00\x00\x1cftypM4A "), bytes.Repeat([]byte("a"), 64)...)
)

type harness struct {
	fake    *fakeopenrouter.Server
	service *state.Service
	media   *notesai.MediaStore
	worker  *notesai.Worker
	ids     int
}

func newHarness(t *testing.T, key string) *harness {
	t.Helper()
	fake := fakeopenrouter.New()
	t.Cleanup(fake.Close)
	gateway := notesai.NewGateway(notesai.NewClient(fake.BaseURL(), key, nil), state.DefaultNoteMediaPolicy())
	service := state.NewService(state.NewMemoryRepository(), state.WithNoteAI(gateway.Configured, gateway.MediaPolicy))
	media, err := notesai.NewMediaStore(filepath.Join(t.TempDir(), "media"))
	if err != nil {
		t.Fatal(err)
	}
	return &harness{fake: fake, service: service, media: media, worker: notesai.NewWorker(service, gateway, media, nil)}
}

func (h *harness) id(prefix string) string {
	h.ids++
	return prefix + "-" + string(rune('a'+h.ids))
}

func (h *harness) consent(t *testing.T, limit float64) {
	t.Helper()
	consent := true
	if _, err := h.service.UpdateNoteAISettings(context.Background(), owner, state.UpdateNoteAISettingsInput{Consent: &consent, MonthlyLimitUSD: &limit}); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) captureNote(t *testing.T, capture state.NoteCapture, kind string, mime string, contents ...[]byte) state.Note {
	t.Helper()
	ctx := context.Background()
	note, err := h.service.CreateNote(ctx, device, state.CreateNoteInput{Capture: capture, ClientRequestID: h.id("note")})
	if err != nil {
		t.Fatal(err)
	}
	for ordinal, content := range contents {
		stored, err := h.media.Put(bytes.NewReader(content), 1<<20, mime)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.service.AddNoteAttachment(ctx, device, note.ID, state.AddNoteAttachmentInput{
			Kind: kind, MimeType: mime, ByteSize: stored.Size, SHA256: stored.SHA256, Ordinal: ordinal, DurationMS: 12000, ClientRequestID: h.id("attach"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	return note
}

func (h *harness) process(t *testing.T, noteID string) state.NoteView {
	t.Helper()
	ctx := context.Background()
	if _, err := h.service.RequestNoteProcessing(ctx, device, noteID, h.id("process")); err != nil {
		t.Fatal(err)
	}
	for {
		processed, err := h.worker.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce() error = %v", err)
		}
		if !processed {
			break
		}
	}
	view, err := h.service.GetNoteView(ctx, noteID)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func submit(title, summary, document string, imageTexts ...string) fakeopenrouter.Reply {
	texts := make([]map[string]any, 0)
	for index, text := range imageTexts {
		texts = append(texts, map[string]any{"index": index + 1, "text": text})
	}
	return fakeopenrouter.ToolCall("submit", "submit_result", map[string]any{
		"title": title, "summary": summary, "document": document, "image_texts": texts,
	}, 0.002)
}

func TestVisionNoteFromHandwriting(t *testing.T) {
	t.Parallel()
	h := newHarness(t, testKey)
	h.consent(t, 10)
	older, _ := h.service.CreateNote(context.Background(), device, state.CreateNoteInput{Document: "Karlsruhe Report Planung", ClientRequestID: "older"})
	note := h.captureNote(t, state.NoteCaptureImage, "image", "image/jpeg", jpeg, jpeg[:len(jpeg)-1])
	h.fake.ScriptChat(
		fakeopenrouter.ToolCall("c1", "search_notes", map[string]any{"query": "Karlsruhe"}, 0.001),
		fakeopenrouter.ToolCall("c2", "link_related_note", map[string]any{"note_id": older.ID, "reason": "Beide zum Karlsruhe Report", "confidence": 0.9}, 0.001),
		submit("Karlsruhe Report", "Handschriftliche Punkte zum Monatsreport.", "## Karlsruhe Report\n- [ ] Zahlen holen\n- [ ] Karla schreiben", "Karlsruhe Report: Zahlen holen", "Karla schreiben"),
	)
	view := h.process(t, note.ID)

	if view.Processing.Status != state.NoteProcessingReady || view.Title != "Karlsruhe Report" || view.TitleSource != state.NoteFieldSourceAI {
		t.Fatalf("view = %q (%s), %+v", view.Title, view.TitleSource, view.Processing)
	}
	if !strings.Contains(view.Document, "- [ ] Zahlen holen") || view.Revision != 2 {
		t.Fatalf("document = %q rev %d", view.Document, view.Revision)
	}
	if view.Attachments[0].DerivedText != "Karlsruhe Report: Zahlen holen" || view.Attachments[1].DerivedText != "Karla schreiben" {
		t.Fatalf("OCR = %+v", view.Attachments)
	}
	if len(view.Relations) != 1 || view.Relations[0].RelatedNoteID != older.ID {
		t.Fatalf("relations = %+v", view.Relations)
	}
	first := h.fake.Requests("/chat/completions")[0]
	var request struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(first.Body, &request)
	if count := strings.Count(string(request.Messages[1].Content), "data:image/jpeg;base64,"); count != 2 {
		t.Fatalf("images sent = %d, want 2", count)
	}
	if !strings.Contains(string(request.Messages[1].Content), older.ID) {
		t.Fatal("related note was not offered as context")
	}
	names := make([]string, 0)
	for _, tool := range request.Tools {
		names = append(names, tool.Function.Name)
	}
	if strings.Join(names, ",") != "search_notes,get_note_excerpt,search_reminders,link_related_note,propose_reminder,submit_result" {
		t.Fatalf("tools = %v", names)
	}
	settings, _ := h.service.GetNoteAISettings(context.Background())
	if settings.SpentThisMonthUSD < 0.004 {
		t.Fatalf("spent = %v, costs not counted", settings.SpentThisMonthUSD)
	}
}

func TestAudioNoteTranscribedThenOrganized(t *testing.T) {
	t.Parallel()
	h := newHarness(t, testKey)
	h.consent(t, 10)
	note := h.captureNote(t, state.NoteCaptureAudio, "audio", "audio/mp4", m4a, m4a[:len(m4a)-2])
	h.fake.ScriptTranscription(fakeopenrouter.Transcript("Erster Teil: Milch kaufen.", 0.0004), fakeopenrouter.Transcript("Zweiter Teil: am Freitag Karla anrufen.", 0.0004))
	h.fake.ScriptChat(
		fakeopenrouter.ToolCall("p", "propose_reminder", map[string]any{"title": "Karla anrufen", "local_date": "2026-09-25", "reason": "am Freitag Karla anrufen"}, 0.001),
		submit("Einkauf und Anruf", "Milch kaufen, Karla anrufen.", "- [ ] Milch kaufen\n- [ ] Karla anrufen"),
	)
	view := h.process(t, note.ID)

	if view.Attachments[0].DerivedKind != "transcript" || view.Attachments[1].DerivedText != "Zweiter Teil: am Freitag Karla anrufen." {
		t.Fatalf("transcripts = %+v", view.Attachments)
	}
	if view.Processing.Status != state.NoteProcessingReady || !strings.Contains(view.Document, "Karla anrufen") {
		t.Fatalf("view = %+v %q", view.Processing, view.Document)
	}
	if len(view.Proposals) != 1 || view.Proposals[0].Status != state.ReminderProposalPending {
		t.Fatalf("proposals = %+v", view.Proposals)
	}
	reminders, _ := h.service.ListReminders(context.Background(), state.ReminderListOptions{})
	if len(reminders) != 0 {
		t.Fatal("a reminder was created without the owner")
	}
	chat := string(h.fake.Requests("/chat/completions")[0].Body)
	if !strings.Contains(chat, "Erster Teil: Milch kaufen.") || !strings.Contains(chat, "Zweiter Teil") {
		t.Fatal("transcripts did not reach the agent")
	}
	if !strings.Contains(chat, "Today is "+time.Now().Format("2006-01-02")) {
		t.Fatal("the agent cannot resolve \"am Freitag\" without today's date")
	}
	if len(h.fake.Requests("/audio/transcriptions")) != 2 {
		t.Fatal("each segment must be transcribed once")
	}
}

func TestToolsCannotTouchOtherNotesOrUnknownTools(t *testing.T) {
	t.Parallel()
	h := newHarness(t, testKey)
	h.consent(t, 10)
	other, _ := h.service.CreateNote(context.Background(), device, state.CreateNoteInput{Title: "Geheim", Document: "Andere Notiz", ClientRequestID: "other"})
	note, _ := h.service.CreateNote(context.Background(), device, state.CreateNoteInput{Document: "Heute war gut", ClientRequestID: "note"})
	h.fake.ScriptChat(
		fakeopenrouter.ToolCall("x1", "update_note", map[string]any{"note_id": other.ID, "document": "überschrieben"}, 0.001),
		fakeopenrouter.ToolCall("x2", "run_shell", map[string]any{"command": "rm -rf /"}, 0.001),
		fakeopenrouter.RawToolCall("x3", "link_related_note", "{not json", 0.001),
		fakeopenrouter.ToolCall("x4", "link_related_note", map[string]any{"note_id": note.ID, "reason": "sich selbst"}, 0.001),
		submit("Guter Tag", "Ein guter Tag.", "Ignorierter Text"),
	)
	view := h.process(t, note.ID)
	if view.Processing.Status != state.NoteProcessingReady || len(view.Relations) != 0 {
		t.Fatalf("view = %+v relations %+v", view.Processing, view.Relations)
	}
	if view.Document != "Heute war gut" {
		t.Fatalf("text note body changed to %q", view.Document)
	}
	untouched, _ := h.service.GetNote(context.Background(), other.ID)
	if untouched.Document != "Andere Notiz" || untouched.Revision != 1 {
		t.Fatal("another note was changed")
	}
	replies := string(h.fake.Requests("/chat/completions")[4].Body)
	if !strings.Contains(replies, "unknown tool") || !strings.Contains(replies, "not valid JSON") {
		t.Fatal("tool errors were not reported back to the model")
	}
}

func TestUserTitleSurvivesAgent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, testKey)
	h.consent(t, 10)
	note, _ := h.service.CreateNote(context.Background(), device, state.CreateNoteInput{Title: "Mein Titel", Document: "Text", ClientRequestID: "n"})
	h.fake.ScriptChat(submit("KI Titel", "KI Zusammenfassung", ""))
	view := h.process(t, note.ID)
	if view.Title != "Mein Titel" || view.TitleSource != state.NoteFieldSourceUser || view.SummarySource != state.NoteFieldSourceAI {
		t.Fatalf("title %q (%s), summary %s", view.Title, view.TitleSource, view.SummarySource)
	}
	if !strings.Contains(string(h.fake.Requests("/chat/completions")[0].Body), `The owner wrote the title`) {
		t.Fatal("the model was not told the title is fixed")
	}
}

func TestImageLimitEnforcedByServer(t *testing.T) {
	t.Parallel()
	h := newHarness(t, testKey)
	ctx := context.Background()
	h.fake.SetModels(fakeopenrouter.Model("deepseek/deepseek-v4.1-flash", []string{"text", "image"}, 40000, true))
	gatewayCapabilities := notesai.NewGateway(notesai.NewClient(h.fake.BaseURL(), testKey, nil), state.DefaultNoteMediaPolicy())
	service := state.NewService(state.NewMemoryRepository(), state.WithNoteAI(gatewayCapabilities.Configured, gatewayCapabilities.MediaPolicy))
	settings, _ := service.GetNoteAISettings(ctx)
	if limit := gatewayCapabilities.Capabilities(ctx, settings).Vision.MaxImages; limit != 4 {
		t.Fatalf("capability limit = %d, want 4", limit)
	}
	note, _ := service.CreateNote(ctx, device, state.CreateNoteInput{Capture: state.NoteCaptureImage, ClientRequestID: "n"})
	for ordinal := 0; ordinal < 5; ordinal++ {
		_, err := service.AddNoteAttachment(ctx, device, note.ID, state.AddNoteAttachmentInput{
			Kind: "image", MimeType: "image/jpeg", ByteSize: 10, SHA256: strings.Repeat(string(rune('a'+ordinal)), 64), Ordinal: ordinal, ClientRequestID: h.id("img"),
		})
		if ordinal < 4 && err != nil {
			t.Fatalf("image %d rejected: %v", ordinal, err)
		}
		if ordinal == 4 && err == nil {
			t.Fatal("fifth image accepted over the model's limit")
		}
	}
}

func TestBudgetExhaustedStopsBeforeCall(t *testing.T) {
	t.Parallel()
	h := newHarness(t, testKey)
	h.consent(t, 1)
	if err := h.service.AddNoteAIUsage(context.Background(), 1.0); err != nil {
		t.Fatal(err)
	}
	note, _ := h.service.CreateNote(context.Background(), device, state.CreateNoteInput{Document: "Text", ClientRequestID: "n"})
	view := h.process(t, note.ID)
	if view.Processing.Status != state.NoteProcessingBudgetExhausted {
		t.Fatalf("status = %+v", view.Processing)
	}
	if len(h.fake.Requests("/chat/completions")) != 0 {
		t.Fatal("OpenRouter was called over the limit")
	}
}

func TestBudgetStopsInTheMiddleOfAJob(t *testing.T) {
	t.Parallel()
	h := newHarness(t, testKey)
	h.consent(t, 0.05)
	note, _ := h.service.CreateNote(context.Background(), device, state.CreateNoteInput{Document: "Text", ClientRequestID: "n"})
	h.fake.ScriptChat(fakeopenrouter.ToolCall("c", "search_notes", map[string]any{"query": "x"}, 0.049))
	view := h.process(t, note.ID)
	if view.Processing.Status != state.NoteProcessingBudgetExhausted || len(h.fake.Requests("/chat/completions")) != 1 {
		t.Fatalf("status = %+v after %d calls", view.Processing, len(h.fake.Requests("/chat/completions")))
	}
}

func TestMissingKeyLeavesNoteUsable(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "")
	h.consent(t, 10)
	note := h.captureNote(t, state.NoteCaptureImage, "image", "image/jpeg", jpeg)
	view := h.process(t, note.ID)
	if view.Processing.Status != state.NoteProcessingNotConfigured || len(view.Attachments) != 1 {
		t.Fatalf("view = %+v", view)
	}
	document := "Selbst geschrieben"
	if _, err := h.service.UpdateNote(context.Background(), device, note.ID, state.UpdateNoteInput{Document: &document, ExpectedRevision: note.Revision, ClientRequestID: "edit"}); err != nil {
		t.Fatalf("note not editable without AI: %v", err)
	}
	if len(h.fake.Requests("/chat/completions"))+len(h.fake.Requests("/audio/transcriptions")) != 0 {
		t.Fatal("OpenRouter was called without a key")
	}
}

func TestConsentRequired(t *testing.T) {
	t.Parallel()
	h := newHarness(t, testKey)
	note := h.captureNote(t, state.NoteCaptureAudio, "audio", "audio/mp4", m4a)
	view := h.process(t, note.ID)
	if view.Processing.Status != state.NoteProcessingConsentRequired || len(h.fake.Requests("/audio/transcriptions")) != 0 {
		t.Fatalf("status = %+v", view.Processing)
	}
}

func TestProviderErrorRetriesThenFails(t *testing.T) {
	t.Parallel()
	h := newHarness(t, testKey)
	h.consent(t, 10)
	note, _ := h.service.CreateNote(context.Background(), device, state.CreateNoteInput{Document: "Text", ClientRequestID: "n"})
	h.fake.ScriptChat(fakeopenrouter.Error(http.StatusServiceUnavailable, "upstream down"))
	view := h.process(t, note.ID)
	if view.Processing.Status != state.NoteProcessingQueued || view.Processing.Error == "" {
		t.Fatalf("after one 503 = %+v", view.Processing)
	}

	h.fake.ScriptChat(fakeopenrouter.Error(http.StatusUnauthorized, "invalid key "+testKey))
	permanentNote, _ := h.service.CreateNote(context.Background(), device, state.CreateNoteInput{Document: "Anderer Text", ClientRequestID: "n2"})
	view = h.process(t, permanentNote.ID)
	if view.Processing.Status != state.NoteProcessingFailed || strings.Contains(view.Processing.Error, testKey) || !strings.Contains(view.Processing.Error, "401") {
		t.Fatalf("after 401 = %+v", view.Processing)
	}
}

func TestNoSubmitMeansFailedNotFiction(t *testing.T) {
	t.Parallel()
	h := newHarness(t, testKey)
	h.consent(t, 10)
	note := h.captureNote(t, state.NoteCaptureImage, "image", "image/jpeg", jpeg)
	h.fake.DefaultChat = func(map[string]any) fakeopenrouter.Reply { return fakeopenrouter.Text("Ich weiß nicht.", 0.0001) }
	view := h.process(t, note.ID)
	if view.Processing.Status != state.NoteProcessingFailed || view.Document != "" || view.AI != nil {
		t.Fatalf("view = %+v %q %+v", view.Processing, view.Document, view.AI)
	}
	if calls := len(h.fake.Requests("/chat/completions")); calls != 8 {
		t.Fatalf("rounds = %d, want the 8 round bound", calls)
	}
}

func TestPlainJSONAnswerIsAccepted(t *testing.T) {
	t.Parallel()
	h := newHarness(t, testKey)
	h.consent(t, 10)
	note, _ := h.service.CreateNote(context.Background(), device, state.CreateNoteInput{Document: "Text", ClientRequestID: "n"})
	h.fake.ScriptChat(fakeopenrouter.Text("```json\n{\"title\":\"JSON Titel\",\"summary\":\"S\"}\n```", 0.0001))
	if view := h.process(t, note.ID); view.Title != "JSON Titel" {
		t.Fatalf("title = %q", view.Title)
	}
}

func TestRunnerCannotSeeResultOrStartJobs(t *testing.T) {
	t.Parallel()
	h := newHarness(t, testKey)
	note, _ := h.service.CreateNote(context.Background(), device, state.CreateNoteInput{Document: "Text", ClientRequestID: "n"})
	if _, err := h.service.RequestNoteProcessing(context.Background(), runner, note.ID, "r"); err == nil {
		t.Fatal("runner started a paid job")
	}
}

func TestWorkerRunStopsWithTheContext(t *testing.T) {
	t.Parallel()
	h := newHarness(t, testKey)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.worker.Run(ctx); close(done) }()
	h.worker.Kick()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop")
	}
}
