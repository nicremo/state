// Package fakeopenrouter is a scripted stand-in for OpenRouter in tests and
// local end-to-end runs. It serves the model catalogues, chat completions
// with tool calls and speech-to-text, records every request and never needs
// a real key.
package fakeopenrouter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// Reply is one scripted answer.
type Reply struct {
	Status int
	Body   any
}

// Request is one recorded call.
type Request struct {
	Method        string
	Path          string
	Authorization string
	Body          []byte
}

type Server struct {
	*httptest.Server

	mu                sync.Mutex
	models            []map[string]any
	sttModels         []map[string]any
	chatReplies       []Reply
	transcribeReplies []Reply
	requests          []Request
	// DefaultChat answers when no reply is scripted. Nil means 500.
	DefaultChat func(body map[string]any) Reply
}

// New starts a fake with DeepSeek V4.1 Flash (text and images, tools, 1M
// context) and Whisper Large V3 Turbo, as the live catalogue listed them on
// 25.09.2026.
func New() *Server {
	server := &Server{
		models:    []map[string]any{Model("deepseek/deepseek-v4.1-flash", []string{"text", "image"}, 1048576, true)},
		sttModels: []map[string]any{TranscriptionModel("openai/whisper-large-v3-turbo")},
	}
	server.Server = httptest.NewServer(http.HandlerFunc(server.serve))
	return server
}

// BaseURL is what the client uses in place of https://openrouter.ai/api/v1.
func (server *Server) BaseURL() string {
	return server.URL + "/api/v1"
}

func Model(id string, inputs []string, contextLength int, tools bool) map[string]any {
	parameters := []string{"max_tokens", "temperature", "response_format"}
	if tools {
		parameters = append(parameters, "tools", "tool_choice")
	}
	return map[string]any{
		"id":             id,
		"name":           id,
		"context_length": contextLength,
		"architecture": map[string]any{
			"input_modalities":  inputs,
			"output_modalities": []string{"text"},
		},
		"supported_parameters": parameters,
		"pricing":              map[string]string{"prompt": "0.0000003", "completion": "0.0000012"},
	}
}

func TranscriptionModel(id string) map[string]any {
	return map[string]any{
		"id":             id,
		"name":           id,
		"context_length": 0,
		"architecture": map[string]any{
			"input_modalities":  []string{"audio"},
			"output_modalities": []string{"transcription"},
		},
		"supported_parameters": []string{},
		"pricing":              map[string]string{"prompt": "0.00000333", "completion": "0"},
	}
}

func (server *Server) SetModels(models ...map[string]any) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.models = models
}

func (server *Server) SetTranscriptionModels(models ...map[string]any) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.sttModels = models
}

func (server *Server) ScriptChat(replies ...Reply) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.chatReplies = append(server.chatReplies, replies...)
}

func (server *Server) ScriptTranscription(replies ...Reply) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.transcribeReplies = append(server.transcribeReplies, replies...)
}

// Requests returns every recorded call to the given path suffix.
func (server *Server) Requests(pathSuffix string) []Request {
	server.mu.Lock()
	defer server.mu.Unlock()
	matched := make([]Request, 0)
	for _, request := range server.requests {
		if strings.HasSuffix(request.Path, pathSuffix) {
			matched = append(matched, request)
		}
	}
	return matched
}

// ToolCall is a chat reply in which the model calls one tool.
func ToolCall(id string, name string, arguments any, cost float64) Reply {
	encoded, _ := json.Marshal(arguments)
	return Reply{Status: http.StatusOK, Body: map[string]any{
		"model": "deepseek/deepseek-v4.1-flash",
		"choices": []any{map[string]any{
			"finish_reason": "tool_calls",
			"message": map[string]any{
				"role":    "assistant",
				"content": "",
				"tool_calls": []any{map[string]any{
					"id": id, "type": "function",
					"function": map[string]any{"name": name, "arguments": string(encoded)},
				}},
			},
		}},
		"usage": map[string]any{"prompt_tokens": 1000, "completion_tokens": 100, "cost": cost},
	}}
}

// RawToolCall sends arguments exactly as given, for malformed JSON tests.
func RawToolCall(id string, name string, arguments string, cost float64) Reply {
	reply := ToolCall(id, name, map[string]any{}, cost)
	choice := reply.Body.(map[string]any)["choices"].([]any)[0].(map[string]any)
	call := choice["message"].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	call["function"].(map[string]any)["arguments"] = arguments
	return reply
}

// Text is a chat reply without tool calls.
func Text(content string, cost float64) Reply {
	return Reply{Status: http.StatusOK, Body: map[string]any{
		"model":   "deepseek/deepseek-v4.1-flash",
		"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": content}}},
		"usage":   map[string]any{"prompt_tokens": 500, "completion_tokens": 50, "cost": cost},
	}}
}

// Transcript is a speech-to-text reply.
func Transcript(text string, cost float64) Reply {
	return Reply{Status: http.StatusOK, Body: map[string]any{"text": text, "usage": map[string]any{"seconds": 12.5, "cost": cost}}}
}

// Segment is one timed passage of a verbose transcript.
type Segment struct {
	Start float64
	End   float64
	Text  string
}

// TranscriptWithSegments is a verbose_json speech-to-text reply.
func TranscriptWithSegments(text string, segments []Segment, cost float64) Reply {
	encoded := make([]map[string]any, 0, len(segments))
	for index, segment := range segments {
		encoded = append(encoded, map[string]any{"id": index, "start": segment.Start, "end": segment.End, "text": segment.Text})
	}
	return Reply{Status: http.StatusOK, Body: map[string]any{"text": text, "segments": encoded, "usage": map[string]any{"seconds": 12.5, "cost": cost}}}
}

// Error is an OpenRouter error envelope.
func Error(status int, message string) Reply {
	return Reply{Status: status, Body: map[string]any{"error": map[string]any{"code": status, "message": message}}}
}

func (server *Server) serve(writer http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	path := request.URL.Path
	if request.URL.RawQuery != "" {
		path += "?" + request.URL.RawQuery
	}
	server.mu.Lock()
	server.requests = append(server.requests, Request{
		Method:        request.Method,
		Path:          path,
		Authorization: request.Header.Get("Authorization"),
		Body:          body,
	})
	server.mu.Unlock()

	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/api/v1/models":
		server.mu.Lock()
		models := server.models
		if request.URL.Query().Get("output_modalities") == "transcription" {
			models = server.sttModels
		}
		server.mu.Unlock()
		write(writer, Reply{Status: http.StatusOK, Body: map[string]any{"data": models}})
	case request.Method == http.MethodGet && request.URL.Path == "/api/v1/key":
		// Keys containing "bad" are refused, as OpenRouter refuses unknown keys.
		if !authorized(request) || strings.Contains(request.Header.Get("Authorization"), "bad") {
			write(writer, Error(http.StatusUnauthorized, "User not found."))
			return
		}
		write(writer, Reply{Status: http.StatusOK, Body: map[string]any{"data": map[string]any{"label": "fake", "limit": nil}}})
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/chat/completions":
		if !authorized(request) {
			write(writer, Error(http.StatusUnauthorized, "No auth credentials found"))
			return
		}
		var decoded map[string]any
		_ = json.Unmarshal(body, &decoded)
		write(writer, server.next(&server.chatReplies, func() Reply {
			if server.DefaultChat != nil {
				return server.DefaultChat(decoded)
			}
			return Error(http.StatusInternalServerError, "no scripted chat reply")
		}))
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/audio/transcriptions":
		if !authorized(request) {
			write(writer, Error(http.StatusUnauthorized, "No auth credentials found"))
			return
		}
		var decoded struct {
			InputAudio struct {
				Data   string `json:"data"`
				Format string `json:"format"`
			} `json:"input_audio"`
		}
		if json.Unmarshal(body, &decoded) != nil || decoded.InputAudio.Data == "" || decoded.InputAudio.Format == "" {
			write(writer, Error(http.StatusBadRequest, "input_audio.data and input_audio.format are required"))
			return
		}
		verbose := bytes.Contains(body, []byte(`"verbose_json"`))
		write(writer, server.next(&server.transcribeReplies, func() Reply {
			if verbose {
				// The mishearings from the owner's first voice note, so the
				// dictionary shows in end-to-end runs.
				return TranscriptWithSegments("Heute war ziemlich nervig. Ich habe meine Cloud.md angepasst. Die Rechnung liegt in ZEVDISK.", []Segment{
					{Start: 0, End: 2, Text: " Heute war ziemlich nervig."},
					{Start: 2, End: 4.5, Text: " Ich habe meine Cloud.md angepasst."},
					{Start: 4.5, End: 7, Text: " Die Rechnung liegt in ZEVDISK."},
				}, 0.0004)
			}
			return Transcript("Transkript aus dem Fake", 0.0004)
		}))
	default:
		write(writer, Error(http.StatusNotFound, fmt.Sprintf("unknown path %s", request.URL.Path)))
	}
}

func authorized(request *http.Request) bool {
	return strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") && len(request.Header.Get("Authorization")) > len("Bearer ")
}

func (server *Server) next(queue *[]Reply, fallback func() Reply) Reply {
	server.mu.Lock()
	if len(*queue) > 0 {
		reply := (*queue)[0]
		*queue = (*queue)[1:]
		server.mu.Unlock()
		return reply
	}
	server.mu.Unlock()
	return fallback()
}

func write(writer http.ResponseWriter, reply Reply) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(reply.Status)
	_ = json.NewEncoder(writer).Encode(reply.Body)
}
