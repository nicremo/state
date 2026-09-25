package notesai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nicremo/state/internal/notesai"
	"github.com/nicremo/state/internal/notesai/fakeopenrouter"
	"github.com/nicremo/state/internal/state"
)

func TestTranscribeAsksForSegments(t *testing.T) {
	t.Parallel()
	fake := fakeopenrouter.New()
	defer fake.Close()
	client := notesai.NewClient(fake.BaseURL(), testKey, nil)
	fake.ScriptTranscription(fakeopenrouter.TranscriptWithSegments("Erster Satz. Zweiter Satz.", []fakeopenrouter.Segment{
		{Start: 0, End: 1.25, Text: " Erster Satz."},
		{Start: 1.25, End: 3.5, Text: " Zweiter Satz."},
	}, 0.0004))

	transcript, err := client.Transcribe(context.Background(), notesai.TranscribeRequest{Model: "openai/whisper-large-v3-turbo", Audio: []byte("m4a"), Format: "m4a", Segments: true})
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
	if len(transcript.Segments) != 2 || transcript.Segments[1].Start != 1.25 || transcript.Segments[1].Text != " Zweiter Satz." {
		t.Fatalf("segments = %+v", transcript.Segments)
	}
	var sent map[string]any
	_ = json.Unmarshal(fake.Requests("/audio/transcriptions")[0].Body, &sent)
	granularities, _ := sent["timestamp_granularities"].([]any)
	if sent["response_format"] != "verbose_json" || len(granularities) != 1 || granularities[0] != "segment" {
		t.Fatalf("request = %v", sent)
	}
}

func TestTranscribeRetriesWithoutSegmentsWhenRefused(t *testing.T) {
	t.Parallel()
	fake := fakeopenrouter.New()
	defer fake.Close()
	client := notesai.NewClient(fake.BaseURL(), testKey, nil)
	fake.ScriptTranscription(
		fakeopenrouter.Error(http.StatusBadRequest, "response_format verbose_json is not supported"),
		fakeopenrouter.Transcript("Nur Text.", 0.0004),
	)
	transcript, err := client.Transcribe(context.Background(), notesai.TranscribeRequest{Model: "m/stt", Audio: []byte("m4a"), Format: "m4a", Segments: true})
	if err != nil || transcript.Text != "Nur Text." || len(transcript.Segments) != 0 {
		t.Fatalf("Transcribe() = %+v, %v", transcript, err)
	}
	requests := fake.Requests("/audio/transcriptions")
	if len(requests) != 2 || strings.Contains(string(requests[1].Body), "verbose_json") {
		t.Fatalf("requests = %d, second = %s", len(requests), requests[len(requests)-1].Body)
	}
}

func (h *harness) dictionary(t *testing.T, words []string, corrections ...state.DictionaryCorrection) {
	t.Helper()
	if _, err := h.service.UpdateNotesDictionary(context.Background(), owner, state.UpdateNotesDictionaryInput{Words: words, Corrections: corrections}); err != nil {
		t.Fatal(err)
	}
}

func TestVoiceNoteKeepsRawTranscriptAndCorrectsSegments(t *testing.T) {
	t.Parallel()
	h := newHarness(t, testKey)
	h.consent(t, 10)
	h.dictionary(t, []string{"sevDesk"},
		state.DictionaryCorrection{From: "Cloud.md", To: "CLAUDE.md", Mode: state.DictionaryModeAlways},
		state.DictionaryCorrection{From: "ZEVDISK", To: "sevDesk", Mode: state.DictionaryModeAlways},
		state.DictionaryCorrection{From: "Cloud", To: "Claude", Mode: state.DictionaryModeContext},
	)
	note := h.captureNote(t, state.NoteCaptureAudio, "audio", "audio/mp4", m4a)
	raw := "Ich habe meine Cloud.md angepasst. Die Rechnung liegt in ZEVDISK."
	h.fake.ScriptTranscription(fakeopenrouter.TranscriptWithSegments(raw, []fakeopenrouter.Segment{
		{Start: 0, End: 2.4, Text: " Ich habe meine Cloud.md angepasst."},
		{Start: 2.4, End: 5.1, Text: " Die Rechnung liegt in ZEVDISK."},
	}, 0.0004))
	h.fake.ScriptChat(submit("CLAUDE.md und Rechnung", "Anpassung und Rechnung.", "- CLAUDE.md angepasst\n- Rechnung in sevDesk"))
	view := h.process(t, note.ID)

	recording := view.Attachments[0]
	if recording.RawText != raw {
		t.Fatalf("raw text = %q", recording.RawText)
	}
	if recording.DerivedText != "Ich habe meine CLAUDE.md angepasst. Die Rechnung liegt in sevDesk." {
		t.Fatalf("corrected text = %q", recording.DerivedText)
	}
	want := []state.TranscriptSegment{
		{StartMS: 0, EndMS: 2400, Text: "Ich habe meine CLAUDE.md angepasst."},
		{StartMS: 2400, EndMS: 5100, Text: "Die Rechnung liegt in sevDesk."},
	}
	if len(recording.Segments) != len(want) {
		t.Fatalf("segments = %+v", recording.Segments)
	}
	for index := range want {
		if recording.Segments[index] != want[index] {
			t.Fatalf("segment %d = %+v, want %+v", index, recording.Segments[index], want[index])
		}
	}
	chat := string(h.fake.Requests("/chat/completions")[0].Body)
	if !strings.Contains(chat, "Ich habe meine CLAUDE.md angepasst") {
		t.Fatal("the agent did not get the corrected transcript")
	}
}

func TestVoiceNoteWithoutSegmentsStillWorks(t *testing.T) {
	t.Parallel()
	h := newHarness(t, testKey)
	h.consent(t, 10)
	note := h.captureNote(t, state.NoteCaptureAudio, "audio", "audio/mp4", m4a)
	h.fake.ScriptTranscription(fakeopenrouter.Transcript("Milch kaufen und Karla anrufen.", 0.0004))
	h.fake.ScriptChat(submit("Einkauf", "Milch und Anruf.", "- [ ] Milch kaufen\n- [ ] Karla anrufen"))
	view := h.process(t, note.ID)
	if view.Processing.Status != state.NoteProcessingReady || len(view.Attachments[0].Segments) != 0 || view.Attachments[0].DerivedText == "" {
		t.Fatalf("view = %+v %+v", view.Processing, view.Attachments[0])
	}
}
