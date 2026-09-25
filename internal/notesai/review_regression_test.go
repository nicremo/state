package notesai_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"

	"github.com/nicremo/state/internal/notesai/fakeopenrouter"
	"github.com/nicremo/state/internal/state"
)

// Review finding: a document the note validator rejects makes ApplyNoteAgentOutcome
// fail; the job is never finished and the note stays "running". After a
// restart the job is requeued and the paid run happens again.
func TestRegressionInvalidAgentDocumentLeavesJobRunning(t *testing.T) {
	h := newHarness(t, testKey)
	h.consent(t, 10)
	note := h.captureNote(t, state.NoteCaptureImage, "image", "image/jpeg", jpeg)
	h.fake.ScriptChat(submit("Titel", "Zusammenfassung", "‮"), submit("Titel", "Zusammenfassung", "‮"))
	ctx := context.Background()
	if _, err := h.service.RequestNoteProcessing(ctx, device, note.ID, "p1"); err != nil {
		t.Fatal(err)
	}
	_, runErr := h.worker.RunOnce(ctx)
	view, _ := h.service.GetNoteView(ctx, note.ID)
	if view.Processing.Status == state.NoteProcessingRunning {
		t.Errorf("job error %v left processing stuck at %q", runErr, view.Processing.Status)
	}
	_ = h.service.RequeueRunningNoteJobs(ctx) // server restart
	_, _ = h.worker.RunOnce(ctx)
	if calls := len(h.fake.Requests("/chat/completions")); calls > 1 {
		t.Errorf("same job was paid for %d times", calls)
	}
}

// Review finding: providerMessage cuts at 200 bytes before redact, so a key that
// straddles the cut is stored on the note in part.
func TestRegressionKeyPrefixLeaksThroughTruncation(t *testing.T) {
	h := newHarness(t, testKey)
	h.consent(t, 10)
	note, _ := h.service.CreateNote(context.Background(), device, state.CreateNoteInput{Document: "Text", ClientRequestID: "n"})
	message := strings.Repeat("x", 180) + " " + testKey
	h.fake.ScriptChat(fakeopenrouter.Error(http.StatusUnauthorized, message))
	view := h.process(t, note.ID)
	if strings.Contains(view.Processing.Error, testKey[:15]) {
		t.Errorf("key prefix leaked into note error: %q", view.Processing.Error)
	}
}

// Review finding: a paid call whose response has no choices is never counted.
func TestRegressionCostOfEmptyChoicesNotCounted(t *testing.T) {
	h := newHarness(t, testKey)
	h.consent(t, 1)
	note, _ := h.service.CreateNote(context.Background(), device, state.CreateNoteInput{Document: "Text", ClientRequestID: "n"})
	empty := fakeopenrouter.Reply{Status: http.StatusOK, Body: map[string]any{"choices": []any{}, "usage": map[string]any{"cost": 0.9}}}
	h.fake.ScriptChat(empty)
	h.process(t, note.ID)
	settings, _ := h.service.GetNoteAISettings(context.Background())
	if settings.SpentThisMonthUSD < 0.9 {
		t.Errorf("OpenRouter billed 0.9 USD, budget counted %.2f", settings.SpentThisMonthUSD)
	}
}

// Review finding: an empty transcript (silence) is stored as "", which the worker reads
// as "not transcribed" and pays for again on every request.
func TestRegressionEmptyTranscriptPaidAgain(t *testing.T) {
	h := newHarness(t, testKey)
	h.consent(t, 10)
	note := h.captureNote(t, state.NoteCaptureAudio, "audio", "audio/mp4", m4a)
	h.fake.ScriptTranscription(fakeopenrouter.Transcript("", 0.01), fakeopenrouter.Transcript("", 0.01))
	h.process(t, note.ID)
	h.process(t, note.ID)
	if calls := len(h.fake.Requests("/audio/transcriptions")); calls != 1 {
		t.Errorf("same recording transcribed %d times", calls)
	}
}

// Review finding: RequestNoteProcessing only remembers the latest request ID; replaying
// an older client_request_id enqueues another paid job.
func TestRegressionOldRequestIDReplayReprocesses(t *testing.T) {
	h := newHarness(t, testKey)
	h.consent(t, 10)
	ctx := context.Background()
	note, _ := h.service.CreateNote(ctx, device, state.CreateNoteInput{Document: "Text", ClientRequestID: "n"})
	for _, id := range []string{"req-A", "req-B", "req-A"} {
		h.fake.ScriptChat(submit("T", "S", ""))
		if _, err := h.service.RequestNoteProcessing(ctx, device, note.ID, id); err != nil {
			t.Fatal(err)
		}
		for {
			ok, err := h.worker.RunOnce(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				break
			}
		}
	}
	if calls := len(h.fake.Requests("/chat/completions")); calls != 2 {
		t.Errorf("replayed request req-A caused %d agent calls, want 2", calls)
	}
}

// Review finding: SourceHash is documented as the hash of the text the result was made
// from, but is computed from the text at apply time.
func TestRegressionSourceHashDescribesEditedText(t *testing.T) {
	h := newHarness(t, testKey)
	h.consent(t, 10)
	ctx := context.Background()
	note, _ := h.service.CreateNote(ctx, device, state.CreateNoteInput{Document: "Alpha Einkauf", ClientRequestID: "n"})
	edited := "Beta Steuererklaerung"
	h.fake.DefaultChat = func(map[string]any) fakeopenrouter.Reply {
		if _, err := h.service.UpdateNote(ctx, device, note.ID, state.UpdateNoteInput{Document: &edited, ExpectedRevision: note.Revision, ClientRequestID: "edit"}); err != nil {
			t.Errorf("edit: %v", err)
		}
		return submit("Alpha Einkauf Liste", "Einkauf", "")
	}
	view := h.process(t, note.ID)
	sum := sha256.Sum256([]byte("Alpha Einkauf"))
	if view.AI == nil || view.AI.SourceHash != hex.EncodeToString(sum[:]) {
		t.Errorf("source_hash does not describe the input text; shown title %q (%s) for text %q", view.Title, view.TitleSource, view.PlainText)
	}
}
