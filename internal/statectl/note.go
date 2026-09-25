package statectl

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NoteService is the terminal client of the note MCP tools. Like the reminder
// commands it is another client of the same audited contract, never a second
// store.
type NoteService struct {
	caller ToolCaller
	newID  func() (string, error)
}

// StoredNote is the part of a stored note the CLI prints.
type StoredNote struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	Revision int64  `json:"revision"`
}

// UpdateNoteOptions carries only the fields that change. A nil pointer keeps
// the current value.
type UpdateNoteOptions struct {
	NoteID     string
	Title      *string
	Summary    *string
	Document   *string
	SourceText string
	RequestID  string
}

func NewNoteService(caller ToolCaller, newID func() (string, error)) *NoteService {
	return &NoteService{caller: caller, newID: newID}
}

// Create stores a text note, or with capture "image" or "audio" an empty
// photo or voice note that attachments fill.
func (service *NoteService) Create(ctx context.Context, title, document, capture, sourceText, requestID string) (StoredNote, json.RawMessage, error) {
	if capture != "" && capture != "image" && capture != "audio" {
		return StoredNote{}, nil, errors.New("capture must be image or audio")
	}
	if capture == "" && strings.TrimSpace(title) == "" && strings.TrimSpace(document) == "" {
		return StoredNote{}, nil, errors.New("a note needs a title or a document")
	}
	if strings.TrimSpace(sourceText) == "" {
		return StoredNote{}, nil, errors.New("source text is required")
	}
	requestID, err := resolveRequestID(requestID, service.newID)
	if err != nil {
		return StoredNote{}, nil, err
	}
	arguments := map[string]any{
		"document":          document,
		"source_text":       sourceText,
		"client_request_id": requestID,
	}
	if strings.TrimSpace(title) != "" {
		arguments["title"] = title
	}
	if capture != "" {
		arguments["capture"] = capture
	}
	return service.write(ctx, "create_note", arguments)
}

func (service *NoteService) Update(ctx context.Context, options UpdateNoteOptions) (StoredNote, json.RawMessage, error) {
	if strings.TrimSpace(options.NoteID) == "" {
		return StoredNote{}, nil, errors.New("note id is required")
	}
	if strings.TrimSpace(options.SourceText) == "" {
		return StoredNote{}, nil, errors.New("source text is required")
	}
	if options.Title == nil && options.Summary == nil && options.Document == nil {
		return StoredNote{}, nil, errors.New("nothing to update")
	}
	revision, err := service.currentRevision(ctx, options.NoteID)
	if err != nil {
		return StoredNote{}, nil, err
	}
	requestID, err := resolveRequestID(options.RequestID, service.newID)
	if err != nil {
		return StoredNote{}, nil, err
	}
	arguments := map[string]any{
		"note_id":           options.NoteID,
		"expected_revision": revision,
		"source_text":       options.SourceText,
		"client_request_id": requestID,
	}
	if options.Title != nil {
		arguments["title"] = *options.Title
	}
	if options.Summary != nil {
		arguments["summary"] = *options.Summary
	}
	if options.Document != nil {
		arguments["document"] = *options.Document
	}
	return service.write(ctx, "update_note", arguments)
}

func (service *NoteService) Show(ctx context.Context, noteID string) (json.RawMessage, error) {
	if strings.TrimSpace(noteID) == "" {
		return nil, errors.New("note id is required")
	}
	return callToolRaw(ctx, service.caller, "get_note", map[string]any{"note_id": noteID})
}

func (service *NoteService) List(ctx context.Context, query string, limit int) (json.RawMessage, error) {
	arguments := map[string]any{}
	if strings.TrimSpace(query) != "" {
		arguments["query"] = query
	}
	if limit > 0 {
		arguments["limit"] = limit
	}
	return callToolRaw(ctx, service.caller, "search_notes", arguments)
}

func (service *NoteService) currentRevision(ctx context.Context, noteID string) (int64, error) {
	raw, err := service.Show(ctx, noteID)
	if err != nil {
		return 0, err
	}
	detail := struct {
		Note StoredNote `json:"note"`
	}{}
	if err := json.Unmarshal(raw, &detail); err != nil {
		return 0, err
	}
	if detail.Note.Revision <= 0 {
		return 0, errors.New("note has no revision")
	}
	return detail.Note.Revision, nil
}

func (service *NoteService) write(ctx context.Context, tool string, arguments map[string]any) (StoredNote, json.RawMessage, error) {
	result, err := service.caller.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: arguments})
	if err != nil {
		return StoredNote{}, nil, err
	}
	if result.IsError {
		return StoredNote{}, nil, errors.New(toolErrorText(result))
	}
	confirmation := struct {
		Stored bool            `json:"stored"`
		Note   json.RawMessage `json:"note"`
	}{}
	if err := decodeToolResult(result, &confirmation); err != nil {
		return StoredNote{}, nil, err
	}
	if !confirmation.Stored {
		return StoredNote{}, nil, errors.New("server did not confirm the note")
	}
	var stored StoredNote
	if err := json.Unmarshal(confirmation.Note, &stored); err != nil {
		return StoredNote{}, nil, err
	}
	return stored, confirmation.Note, nil
}

func callToolRaw(ctx context.Context, caller ToolCaller, name string, arguments map[string]any) (json.RawMessage, error) {
	result, err := caller.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, errors.New(toolErrorText(result))
	}
	var raw json.RawMessage
	if err := decodeToolResult(result, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// AttachOptions describes one file the owner named for a note.
type AttachOptions struct {
	NoteID     string
	MimeType   string
	Content    []byte
	Ordinal    int
	SourceText string
	RequestID  string
}

// Attach uploads one photo or recording through the authenticated server;
// the CLI never sends a file to OpenRouter itself.
func (service *NoteService) Attach(ctx context.Context, options AttachOptions) (json.RawMessage, error) {
	if strings.TrimSpace(options.NoteID) == "" {
		return nil, errors.New("note id is required")
	}
	if strings.TrimSpace(options.SourceText) == "" {
		return nil, errors.New("source text is required")
	}
	if len(options.Content) == 0 {
		return nil, errors.New("the file is empty")
	}
	requestID, err := resolveRequestID(options.RequestID, service.newID)
	if err != nil {
		return nil, err
	}
	return callToolRaw(ctx, service.caller, "add_note_attachment", map[string]any{
		"note_id":           options.NoteID,
		"mime_type":         options.MimeType,
		"content_base64":    base64.StdEncoding.EncodeToString(options.Content),
		"ordinal":           options.Ordinal,
		"source_text":       options.SourceText,
		"client_request_id": requestID,
	})
}

// Process asks the server's notes AI to process a note.
func (service *NoteService) Process(ctx context.Context, noteID string, requestID string) (json.RawMessage, error) {
	if strings.TrimSpace(noteID) == "" {
		return nil, errors.New("note id is required")
	}
	requestID, err := resolveRequestID(requestID, service.newID)
	if err != nil {
		return nil, err
	}
	return callToolRaw(ctx, service.caller, "process_note", map[string]any{"note_id": noteID, "client_request_id": requestID})
}

func (service *NoteService) Processing(ctx context.Context, noteID string) (json.RawMessage, error) {
	if strings.TrimSpace(noteID) == "" {
		return nil, errors.New("note id is required")
	}
	return callToolRaw(ctx, service.caller, "get_note_processing", map[string]any{"note_id": noteID})
}

// Dictionary reads the owner's dictionary for notes.
func (service *NoteService) Dictionary(ctx context.Context) (json.RawMessage, error) {
	return callToolRaw(ctx, service.caller, "get_notes_dictionary", map[string]any{})
}

func (service *NoteService) Related(ctx context.Context, noteID string) (json.RawMessage, error) {
	if strings.TrimSpace(noteID) == "" {
		return nil, errors.New("note id is required")
	}
	return callToolRaw(ctx, service.caller, "list_related_notes", map[string]any{"note_id": noteID})
}
