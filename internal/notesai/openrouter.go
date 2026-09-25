package notesai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DefaultBaseURL is OpenRouter's OpenAI-compatible API.
const DefaultBaseURL = "https://openrouter.ai/api/v1"

// ErrNotConfigured means the server has no OpenRouter key. Notes keep
// working; only AI processing is off.
var ErrNotConfigured = errors.New("openrouter key not configured")

// Client talks to OpenRouter with the server's key. The key never leaves the
// server, never appears in an error and never reaches the audit log.
type Client struct {
	baseURL string
	key     string
	http    *http.Client
}

func NewClient(baseURL string, key string, httpClient *http.Client) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 90 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), key: strings.TrimSpace(key), http: httpClient}
}

// Configured reports whether a key is present.
func (client *Client) Configured() bool {
	return client != nil && client.key != ""
}

// LoadKey reads the key file. A missing file is not an error: the server
// simply runs without AI. The key itself is never logged.
func LoadKey(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read OpenRouter key file: %w", err)
	}
	return strings.TrimSpace(string(content)), nil
}

type Model struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	ContextLength       int               `json:"context_length"`
	Architecture        ModelArchitecture `json:"architecture"`
	SupportedParameters []string          `json:"supported_parameters"`
	Pricing             ModelPricing      `json:"pricing"`
}

type ModelArchitecture struct {
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
}

type ModelPricing struct {
	Prompt     string `json:"prompt"`
	Completion string `json:"completion"`
}

func (model Model) Accepts(modality string) bool {
	return contains(model.Architecture.InputModalities, modality)
}

func (model Model) Supports(parameter string) bool {
	return contains(model.SupportedParameters, parameter)
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// Models lists the chat models. The endpoint is public; the key is sent
// when present so account-specific routing applies.
func (client *Client) Models(ctx context.Context) ([]Model, error) {
	return client.listModels(ctx, "/models")
}

// TranscriptionModels lists the live speech-to-text catalogue.
func (client *Client) TranscriptionModels(ctx context.Context) ([]Model, error) {
	return client.listModels(ctx, "/models?output_modalities=transcription")
}

func (client *Client) listModels(ctx context.Context, path string) ([]Model, error) {
	var response struct {
		Data []Model `json:"data"`
	}
	if err := client.do(ctx, http.MethodGet, path, nil, &response, false); err != nil {
		return nil, err
	}
	return response.Data, nil
}

type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL string `json:"url"`
}

type ChatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// Text returns the message content when it is plain text.
func (message ChatMessage) Text() string {
	switch content := message.Content.(type) {
	case string:
		return content
	case []any:
		var builder strings.Builder
		for _, part := range content {
			if object, ok := part.(map[string]any); ok {
				if text, ok := object["text"].(string); ok {
					builder.WriteString(text)
				}
			}
		}
		return builder.String()
	default:
		return ""
	}
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Tools       []Tool        `json:"tools,omitempty"`
	ToolChoice  any           `json:"tool_choice,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature"`
	Usage       *usageRequest `json:"usage,omitempty"`
}

type usageRequest struct {
	Include bool `json:"include"`
}

type Usage struct {
	PromptTokens     int     `json:"prompt_tokens,omitempty"`
	CompletionTokens int     `json:"completion_tokens,omitempty"`
	Seconds          float64 `json:"seconds,omitempty"`
	Cost             float64 `json:"cost"`
}

type ChatChoice struct {
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type ChatResponse struct {
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   Usage        `json:"usage"`
}

// Chat sends one chat completion. Usage accounting is always requested, so
// every call's cost can be counted against the monthly limit.
func (client *Client) Chat(ctx context.Context, request ChatRequest) (ChatResponse, error) {
	if !client.Configured() {
		return ChatResponse{}, ErrNotConfigured
	}
	request.Usage = &usageRequest{Include: true}
	var response ChatResponse
	if err := client.do(ctx, http.MethodPost, "/chat/completions", request, &response, true); err != nil {
		return ChatResponse{}, err
	}
	if len(response.Choices) == 0 {
		return ChatResponse{}, &ProviderError{Status: http.StatusBadGateway, Message: "no choices in model response", Retryable: true}
	}
	return response, nil
}

type TranscribeRequest struct {
	Model    string
	Audio    []byte
	Format   string
	Language string
}

type Transcript struct {
	Text  string `json:"text"`
	Usage Usage  `json:"usage"`
}

// Transcribe uses the dedicated speech-to-text endpoint with base64 JSON.
func (client *Client) Transcribe(ctx context.Context, request TranscribeRequest) (Transcript, error) {
	if !client.Configured() {
		return Transcript{}, ErrNotConfigured
	}
	body := map[string]any{
		"model": request.Model,
		"input_audio": map[string]string{
			"data":   base64.StdEncoding.EncodeToString(request.Audio),
			"format": request.Format,
		},
	}
	if request.Language != "" {
		body["language"] = request.Language
	}
	var transcript Transcript
	if err := client.do(ctx, http.MethodPost, "/audio/transcriptions", body, &transcript, true); err != nil {
		return Transcript{}, err
	}
	return transcript, nil
}

// ProviderError is a failed OpenRouter call. Its message is safe to store
// on a note: it never contains the key or the request.
type ProviderError struct {
	Status    int
	Message   string
	Retryable bool
}

func (err *ProviderError) Error() string {
	return fmt.Sprintf("OpenRouter %d: %s", err.Status, err.Message)
}

func (client *Client) do(ctx context.Context, method string, path string, body any, output any, authenticated bool) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode OpenRouter request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build OpenRouter request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Title", "State")
	if client.key != "" && (authenticated || strings.HasPrefix(path, "/models")) {
		request.Header.Set("Authorization", "Bearer "+client.key)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return &ProviderError{Status: 0, Message: client.redact(safeTransportMessage(err)), Retryable: true}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return &ProviderError{Status: response.StatusCode, Message: "response could not be read", Retryable: true}
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return &ProviderError{
			Status:    response.StatusCode,
			Message:   client.redact(providerMessage(payload, response.Status)),
			Retryable: response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 || response.StatusCode == http.StatusRequestTimeout,
		}
	}
	if err := json.Unmarshal(payload, output); err != nil {
		return &ProviderError{Status: response.StatusCode, Message: "response was not valid JSON", Retryable: true}
	}
	return nil
}

// providerMessage extracts OpenRouter's error message, cut to one short line.
func providerMessage(payload []byte, fallback string) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	message := fallback
	if json.Unmarshal(payload, &envelope) == nil && envelope.Error.Message != "" {
		message = envelope.Error.Message
	}
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > 200 {
		message = message[:200]
	}
	return message
}

func safeTransportMessage(err error) string {
	var urlError *url.Error
	if errors.As(err, &urlError) {
		if urlError.Timeout() {
			return "request timed out"
		}
		return "connection failed"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "request timed out"
	}
	return "connection failed"
}

// redact removes the key should a provider ever echo it back.
func (client *Client) redact(message string) string {
	if client.key == "" {
		return message
	}
	return strings.ReplaceAll(message, client.key, "[redacted]")
}
