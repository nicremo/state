package notesai_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/nicremo/state/internal/notesai"
	"github.com/nicremo/state/internal/notesai/fakeopenrouter"
	"github.com/nicremo/state/internal/state"
)

const testKey = "sk-or-v1-test-key-that-must-never-leak"

func defaultSettings() state.NoteAISettings {
	return state.NoteAISettings{Consent: true, MonthlyLimitUSD: 10, AgentModel: state.DefaultNotesAgentModel, TranscriptionModel: state.DefaultTranscriptionModel}
}

func TestClientReadsCatalogueChatAndTranscription(t *testing.T) {
	t.Parallel()
	fake := fakeopenrouter.New()
	defer fake.Close()
	client := notesai.NewClient(fake.BaseURL(), testKey, nil)
	ctx := context.Background()

	models, err := client.Models(ctx)
	if err != nil || len(models) != 1 || !models[0].Accepts("image") || !models[0].Supports("tools") {
		t.Fatalf("Models() = %+v, %v", models, err)
	}
	stt, err := client.TranscriptionModels(ctx)
	if err != nil || len(stt) != 1 || stt[0].ID != "openai/whisper-large-v3-turbo" {
		t.Fatalf("TranscriptionModels() = %+v, %v", stt, err)
	}

	fake.ScriptChat(fakeopenrouter.ToolCall("call-1", "search_notes", map[string]any{"query": "Karla"}, 0.0012))
	response, err := client.Chat(ctx, notesai.ChatRequest{Model: "deepseek/deepseek-v4.1-flash", Messages: []notesai.ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil || len(response.Choices[0].Message.ToolCalls) != 1 || response.Usage.Cost != 0.0012 {
		t.Fatalf("Chat() = %+v, %v", response, err)
	}
	chat := fake.Requests("/chat/completions")[0]
	if chat.Authorization != "Bearer "+testKey || !strings.Contains(string(chat.Body), `"usage":{"include":true}`) {
		t.Fatalf("chat request = %s %s", chat.Authorization, chat.Body)
	}

	transcript, err := client.Transcribe(ctx, notesai.TranscribeRequest{Model: "openai/whisper-large-v3-turbo", Audio: []byte("m4a bytes"), Format: "m4a"})
	if err != nil || transcript.Text == "" || transcript.Usage.Cost <= 0 {
		t.Fatalf("Transcribe() = %+v, %v", transcript, err)
	}
	var sent struct {
		Model      string `json:"model"`
		InputAudio struct {
			Data   string `json:"data"`
			Format string `json:"format"`
		} `json:"input_audio"`
	}
	_ = json.Unmarshal(fake.Requests("/audio/transcriptions")[0].Body, &sent)
	if sent.InputAudio.Format != "m4a" || sent.InputAudio.Data != "bTRhIGJ5dGVz" || sent.Model != "openai/whisper-large-v3-turbo" {
		t.Fatalf("transcription request = %+v", sent)
	}
}

func TestClientErrorsAreClassifiedAndNeverLeakTheKey(t *testing.T) {
	t.Parallel()
	fake := fakeopenrouter.New()
	defer fake.Close()
	client := notesai.NewClient(fake.BaseURL(), testKey, nil)
	ctx := context.Background()
	request := notesai.ChatRequest{Model: "m", Messages: []notesai.ChatMessage{{Role: "user", Content: "x"}}}

	fake.ScriptChat(
		fakeopenrouter.Error(http.StatusTooManyRequests, "rate limited"),
		fakeopenrouter.Error(http.StatusUnauthorized, "invalid key "+testKey),
		fakeopenrouter.Error(http.StatusPaymentRequired, "insufficient credits"),
	)
	var providerError *notesai.ProviderError
	_, err := client.Chat(ctx, request)
	if !errors.As(err, &providerError) || !providerError.Retryable {
		t.Fatalf("429 = %v, want retryable", err)
	}
	_, err = client.Chat(ctx, request)
	if !errors.As(err, &providerError) || providerError.Retryable || strings.Contains(err.Error(), testKey) {
		t.Fatalf("401 = %v, want permanent and redacted", err)
	}
	_, err = client.Chat(ctx, request)
	if !errors.As(err, &providerError) || providerError.Retryable {
		t.Fatalf("402 = %v, want permanent", err)
	}

	unconfigured := notesai.NewClient(fake.BaseURL(), "", nil)
	if _, err := unconfigured.Chat(ctx, request); !errors.Is(err, notesai.ErrNotConfigured) {
		t.Fatalf("without key = %v", err)
	}
	if _, err := unconfigured.Transcribe(ctx, notesai.TranscribeRequest{}); !errors.Is(err, notesai.ErrNotConfigured) {
		t.Fatalf("transcribe without key = %v", err)
	}
	if len(fake.Requests("/chat/completions")) != 3 {
		t.Fatal("a request without key reached OpenRouter")
	}
}

func TestCapabilitiesComeFromTheModelAndTheServer(t *testing.T) {
	t.Parallel()
	policy := state.DefaultNoteMediaPolicy()
	decode := func(raw map[string]any) notesai.Model {
		encoded, _ := json.Marshal(raw)
		var model notesai.Model
		_ = json.Unmarshal(encoded, &model)
		return model
	}
	deepseek := decode(fakeopenrouter.Model("deepseek/deepseek-v4.1-flash", []string{"text", "image"}, 1048576, true))
	whisper := decode(fakeopenrouter.TranscriptionModel("openai/whisper-large-v3-turbo"))
	settings := defaultSettings()

	capabilities := notesai.ResolveCapabilities([]notesai.Model{deepseek}, []notesai.Model{whisper}, settings, true, policy)
	if !capabilities.AIAvailable || !capabilities.Vision.Available || capabilities.Vision.MaxImages != policy.MaxImages || capabilities.Vision.LimitSource != "server_policy" || !capabilities.Audio.Available {
		t.Fatalf("large context = %+v", capabilities)
	}

	small := decode(fakeopenrouter.Model("deepseek/deepseek-v4.1-flash", []string{"text", "image"}, 40000, true))
	capabilities = notesai.ResolveCapabilities([]notesai.Model{small}, []notesai.Model{whisper}, settings, true, policy)
	if capabilities.Vision.MaxImages != 4 || capabilities.Vision.LimitSource != "model_context" {
		t.Fatalf("small context = %+v", capabilities.Vision)
	}

	textOnly := decode(fakeopenrouter.Model("deepseek/deepseek-v4.1-flash", []string{"text"}, 1048576, true))
	capabilities = notesai.ResolveCapabilities([]notesai.Model{textOnly}, nil, settings, true, policy)
	if capabilities.Vision.Available || capabilities.Vision.MaxImages != 0 || capabilities.Audio.Available {
		t.Fatalf("text only = %+v", capabilities)
	}

	noTools := decode(fakeopenrouter.Model("deepseek/deepseek-v4.1-flash", []string{"text", "image"}, 1048576, false))
	capabilities = notesai.ResolveCapabilities([]notesai.Model{noTools}, nil, settings, true, policy)
	if capabilities.AIAvailable || capabilities.Reason != notesai.ReasonAgentNeedsToolCalls {
		t.Fatalf("no tools = %+v", capabilities)
	}

	capabilities = notesai.ResolveCapabilities([]notesai.Model{deepseek}, []notesai.Model{whisper}, settings, false, policy)
	if capabilities.AIAvailable || capabilities.Reason != notesai.ReasonNotConfigured || capabilities.Vision.MaxImages != 0 {
		t.Fatalf("without key = %+v", capabilities)
	}
}

func TestGatewayLowersTheUploadPolicyToTheModel(t *testing.T) {
	t.Parallel()
	fake := fakeopenrouter.New()
	defer fake.Close()
	fake.SetModels(fakeopenrouter.Model("deepseek/deepseek-v4.1-flash", []string{"text", "image"}, 40000, true))
	gateway := notesai.NewGateway(notesai.NewClient(fake.BaseURL(), testKey, nil), state.DefaultNoteMediaPolicy())
	if gateway.MediaPolicy().MaxImages != state.DefaultNoteMediaPolicy().MaxImages {
		t.Fatal("policy changed before the catalogue was known")
	}
	capabilities := gateway.Capabilities(context.Background(), defaultSettings())
	if capabilities.Vision.MaxImages != 4 || gateway.MediaPolicy().MaxImages != 4 {
		t.Fatalf("capabilities = %+v, policy = %+v", capabilities.Vision, gateway.MediaPolicy())
	}
}

func TestLoadKeyTreatsAMissingFileAsNoAI(t *testing.T) {
	t.Parallel()
	key, err := notesai.LoadKey(t.TempDir() + "/openrouter.key")
	if err != nil || key != "" {
		t.Fatalf("LoadKey(missing) = %q, %v", key, err)
	}
}
