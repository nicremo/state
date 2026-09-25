package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nicremo/state/internal/notesai"
	"github.com/nicremo/state/internal/state"
)

// NotesAI is what the MCP tools need from the notes AI.
type NotesAI interface {
	Media() *notesai.MediaStore
	Kick()
}

// maxMCPAttachmentBytes bounds one decoded attachment sent over MCP. Larger
// files go through statectl note attach or the REST upload.
const maxMCPAttachmentBytes = 8 << 20

type addNoteAttachmentInput struct {
	NoteID          string `json:"note_id" jsonschema:"UUIDv7 of the note."`
	MimeType        string `json:"mime_type" jsonschema:"One of image/jpeg, image/png, image/heic, audio/mp4, audio/m4a, audio/aac, audio/mpeg, audio/wav."`
	ContentBase64   string `json:"content_base64" jsonschema:"The file, base64 encoded. At most 8 MB decoded. Only files the owner explicitly named."`
	Ordinal         int    `json:"ordinal,omitempty" jsonschema:"Position among the note's attachments, starting at 0."`
	DurationMS      int64  `json:"duration_ms,omitempty" jsonschema:"Length of a recording in milliseconds, if known."`
	ClientRequestID string `json:"client_request_id" jsonschema:"Stable UUIDv7 for idempotent retries."`
	SourceText      string `json:"source_text" jsonschema:"Relevant original user wording that caused this write."`
}

type processNoteInput struct {
	NoteID          string `json:"note_id" jsonschema:"UUIDv7 of the note."`
	ClientRequestID string `json:"client_request_id" jsonschema:"Stable UUIDv7 for idempotent retries."`
}

func (server *server) registerNoteAITools(readOnly *mcp.ToolAnnotations, mutating *mcp.ToolAnnotations) {
	mcp.AddTool(server.mcp, &mcp.Tool{
		Name:        "add_note_attachment",
		Description: "Attach a photo or recording the owner explicitly gave you to a note. Create a note with capture image or audio first if needed. Then call process_note once all files are attached.",
		Annotations: mutating,
	}, server.addNoteAttachment)
	mcp.AddTool(server.mcp, &mcp.Tool{
		Name:        "process_note",
		Description: "Ask State's notes AI to transcribe recordings, read photos and handwriting, and give the note an AI title, summary and related notes. Runs on the server with the owner's consent and monthly limit; check the result with get_note_processing.",
		Annotations: mutating,
	}, server.processNote)
	mcp.AddTool(server.mcp, &mcp.Tool{
		Name:        "get_note_processing",
		Description: "Get a note's AI processing status, the AI title and summary with their model, and each attachment's OCR text or transcript. Recordings also carry the raw transcript before the owner's dictionary corrected it and, when available, timed segments (start_ms, end_ms, text).",
		Annotations: readOnly,
	}, server.getNoteProcessing)
	mcp.AddTool(server.mcp, &mcp.Tool{
		Name:        "list_related_notes",
		Description: "List the notes State's notes AI linked to this note, each with the reason.",
		Annotations: readOnly,
	}, server.listRelatedNotes)
	mcp.AddTool(server.mcp, &mcp.Tool{
		Name:        "get_notes_dictionary",
		Description: "Read the owner's dictionary for notes: words to spell exactly like this, and corrections of words speech recognition mishears (mode always: replaced in every transcript; mode context: only where the context shows that meaning). Use these spellings when you write notes or reminders. Only the owner changes it, in the app.",
		Annotations: readOnly,
	}, server.getNotesDictionary)
}

type emptyInput struct{}

func (server *server) getNotesDictionary(ctx context.Context, request *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, any, error) {
	if _, err := server.reminderActor(ctx, request); err != nil {
		return nil, nil, err
	}
	dictionary, err := server.state.GetNotesDictionary(ctx)
	if err != nil {
		return nil, nil, err
	}
	return nil, dictionary, nil
}

func (server *server) addNoteAttachment(ctx context.Context, request *mcp.CallToolRequest, input addNoteAttachmentInput) (*mcp.CallToolResult, any, error) {
	actor, err := server.reminderActor(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	if server.notesAI == nil || server.notesAI.Media() == nil || !notesai.AllowedMedia(input.MimeType) {
		return nil, nil, state.ErrInvalidInput
	}
	if base64.StdEncoding.DecodedLen(len(input.ContentBase64)) > maxMCPAttachmentBytes+3 {
		return nil, nil, state.ErrInvalidInput
	}
	content, err := base64.StdEncoding.DecodeString(strings.TrimSpace(input.ContentBase64))
	if err != nil || len(content) == 0 {
		return nil, nil, state.ErrInvalidInput
	}
	kind := state.NoteAttachmentImage
	limit := server.state.NoteMediaPolicy().MaxImageBytes
	if strings.HasPrefix(input.MimeType, "audio/") {
		kind = state.NoteAttachmentAudio
		limit = server.state.NoteMediaPolicy().MaxAudioBytes
	}
	stored, err := server.notesAI.Media().Stage(bytes.NewReader(content), min(limit, maxMCPAttachmentBytes), input.MimeType)
	if err != nil {
		return nil, nil, state.ErrInvalidInput
	}
	attachment, err := server.state.AddNoteAttachment(ctx, actor, input.NoteID, state.AddNoteAttachmentInput{
		Kind:            kind,
		MimeType:        input.MimeType,
		ByteSize:        stored.Size,
		SHA256:          stored.SHA256,
		DurationMS:      input.DurationMS,
		Ordinal:         input.Ordinal,
		Source:          "mcp",
		ClientRequestID: input.ClientRequestID,
	})
	if err != nil {
		stored.Discard()
		return nil, nil, err
	}
	if err := stored.Commit(); err != nil {
		return nil, nil, err
	}
	server.notifySync(ctx, actor.ID)
	return nil, map[string]any{"stored": true, "attachment": attachment}, nil
}

func (server *server) processNote(ctx context.Context, request *mcp.CallToolRequest, input processNoteInput) (*mcp.CallToolResult, any, error) {
	actor, err := server.reminderActor(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	processing, err := server.state.RequestNoteProcessing(ctx, actor, input.NoteID, input.ClientRequestID)
	if err != nil {
		return nil, nil, err
	}
	if processing.Status == state.NoteProcessingQueued && server.notesAI != nil {
		server.notesAI.Kick()
	}
	server.notifySync(ctx, actor.ID)
	return nil, map[string]any{"processing": processing}, nil
}

func (server *server) getNoteProcessing(ctx context.Context, request *mcp.CallToolRequest, input getNoteInput) (*mcp.CallToolResult, any, error) {
	if _, err := server.reminderActor(ctx, request); err != nil {
		return nil, nil, err
	}
	view, err := server.state.GetNoteView(ctx, input.NoteID)
	if err != nil {
		return nil, nil, err
	}
	return nil, map[string]any{"processing": view.Processing, "ai": view.AI, "attachments": view.Attachments}, nil
}

func (server *server) listRelatedNotes(ctx context.Context, request *mcp.CallToolRequest, input getNoteInput) (*mcp.CallToolResult, any, error) {
	if _, err := server.reminderActor(ctx, request); err != nil {
		return nil, nil, err
	}
	view, err := server.state.GetNoteView(ctx, input.NoteID)
	if err != nil {
		return nil, nil, err
	}
	return nil, map[string]any{"relations": view.Relations}, nil
}
