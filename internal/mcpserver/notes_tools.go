package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nicremo/state/internal/state"
)

type searchNotesInput struct {
	Query string `json:"query,omitempty" jsonschema:"Words to find in note titles, summaries and text. Leave empty for the most recently changed notes."`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum notes to return. Maximum 50."`
}

type getNoteInput struct {
	NoteID string `json:"note_id" jsonschema:"UUIDv7 of the note."`
}

type createNoteInput struct {
	Title           string `json:"title,omitempty" jsonschema:"Optional title. Leave empty to use the first line of the document."`
	Document        string `json:"document,omitempty" jsonschema:"Note content as Markdown: headings, lists, - [ ] checklists, --- dividers. Never include secrets."`
	ClientRequestID string `json:"client_request_id" jsonschema:"Stable UUIDv7 for idempotent retries."`
	SourceText      string `json:"source_text" jsonschema:"Relevant original user wording that caused this write."`
	CorrelationID   string `json:"correlation_id,omitempty" jsonschema:"Optional UUIDv7 shared by related actions."`
}

type updateNoteInput struct {
	NoteID           string  `json:"note_id" jsonschema:"UUIDv7 of the note."`
	ExpectedRevision int64   `json:"expected_revision" jsonschema:"Current note revision from get_note. Stale revisions are rejected."`
	Title            *string `json:"title,omitempty" jsonschema:"Replacement title. An empty string returns to the title derived from the first line."`
	Document         *string `json:"document,omitempty" jsonschema:"Replacement Markdown document."`
	Summary          *string `json:"summary,omitempty" jsonschema:"Replacement short summary. An empty string returns to the derived summary."`
	ClientRequestID  string  `json:"client_request_id" jsonschema:"Stable UUIDv7 for idempotent retries."`
	SourceText       string  `json:"source_text" jsonschema:"Relevant original user wording that caused this write."`
	CorrelationID    string  `json:"correlation_id,omitempty" jsonschema:"Optional UUIDv7 shared by related actions."`
}

// noteListItem keeps search results small: agents open one note with get_note.
type noteListItem struct {
	ID            string                `json:"id"`
	Title         string                `json:"title"`
	Summary       string                `json:"summary"`
	TitleSource   state.NoteFieldSource `json:"title_source"`
	SummarySource state.NoteFieldSource `json:"summary_source"`
	Archived      bool                  `json:"archived"`
	Revision      int64                 `json:"revision"`
	UpdatedAt     time.Time             `json:"updated_at"`
}

func (server *server) registerNoteTools(readOnly *mcp.ToolAnnotations, mutating *mcp.ToolAnnotations) {
	mcp.AddTool(server.mcp, &mcp.Tool{
		Name:        "search_notes",
		Description: "Find the owner's notes by words, or list the most recently changed ones. Returns titles and summaries; open one with get_note. Read notes only when they matter for the current request.",
		Annotations: readOnly,
	}, server.searchNotes)
	mcp.AddTool(server.mcp, &mcp.Tool{
		Name:        "get_note",
		Description: "Get one note with its Markdown document, revision and complete audit history.",
		Annotations: readOnly,
	}, server.getNote)
	mcp.AddTool(server.mcp, &mcp.Tool{
		Name:        "create_note",
		Description: "Store a note only when the owner explicitly asks for one. Notes are for knowledge, not obligations; use create_reminder for anything with a date. Report success only when the result says stored=true.",
		Annotations: mutating,
	}, server.createNote)
	mcp.AddTool(server.mcp, &mcp.Tool{
		Name:        "update_note",
		Description: "Change a note on the owner's explicit request. Read it with get_note first and pass its revision as expected_revision. Agents cannot archive notes.",
		Annotations: mutating,
	}, server.updateNote)
}

func (server *server) searchNotes(ctx context.Context, request *mcp.CallToolRequest, input searchNotesInput) (*mcp.CallToolResult, any, error) {
	if _, err := server.reminderActor(ctx, request); err != nil {
		return nil, nil, err
	}
	limit := input.Limit
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	notes, err := server.state.ListNotes(ctx, state.NoteListOptions{Query: input.Query, Limit: limit})
	if err != nil {
		return nil, nil, err
	}
	items := make([]noteListItem, 0, len(notes))
	for _, note := range notes {
		items = append(items, noteListItem{
			ID:            note.ID,
			Title:         note.Title,
			Summary:       note.Summary,
			TitleSource:   note.TitleSource,
			SummarySource: note.SummarySource,
			Archived:      note.Archived,
			Revision:      note.Revision,
			UpdatedAt:     note.UpdatedAt,
		})
	}
	return nil, map[string]any{"notes": items}, nil
}

func (server *server) getNote(ctx context.Context, request *mcp.CallToolRequest, input getNoteInput) (*mcp.CallToolResult, any, error) {
	if _, err := server.reminderActor(ctx, request); err != nil {
		return nil, nil, err
	}
	note, err := server.state.GetNote(ctx, input.NoteID)
	if err != nil {
		return nil, nil, err
	}
	history, err := server.state.ListNoteHistory(ctx, input.NoteID)
	if err != nil {
		return nil, nil, err
	}
	return nil, map[string]any{"note": note, "history": compactNoteHistory(history)}, nil
}

func (server *server) createNote(ctx context.Context, request *mcp.CallToolRequest, input createNoteInput) (*mcp.CallToolResult, any, error) {
	actor, err := server.reminderActor(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	note, err := server.state.CreateNote(ctx, actor, state.CreateNoteInput{
		Title:           input.Title,
		Document:        input.Document,
		Source:          "mcp",
		SourceExcerpt:   input.SourceText,
		ClientRequestID: input.ClientRequestID,
		CorrelationID:   input.CorrelationID,
	})
	if err != nil {
		return nil, nil, err
	}
	server.notifySync(ctx, actor.ID)
	return nil, map[string]any{"stored": true, "note": note}, nil
}

func (server *server) updateNote(ctx context.Context, request *mcp.CallToolRequest, input updateNoteInput) (*mcp.CallToolResult, any, error) {
	actor, err := server.reminderActor(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	note, err := server.state.UpdateNote(ctx, actor, input.NoteID, state.UpdateNoteInput{
		Title:            input.Title,
		Document:         input.Document,
		Summary:          input.Summary,
		ExpectedRevision: input.ExpectedRevision,
		Source:           "mcp",
		SourceExcerpt:    input.SourceText,
		ClientRequestID:  input.ClientRequestID,
		CorrelationID:    input.CorrelationID,
	})
	if err != nil {
		return nil, nil, err
	}
	server.notifySync(ctx, actor.ID)
	return nil, map[string]any{"stored": true, "note": note}, nil
}

// maxNoteHistoryForAgents bounds what get_note puts into an agent's context.
// The full chain, snapshots included, stays available over REST.
const maxNoteHistoryForAgents = 50

type noteHistoryItem struct {
	Action        state.AuditAction `json:"action"`
	Actor         state.Actor       `json:"actor"`
	ServerTime    time.Time         `json:"server_time"`
	SourceExcerpt string            `json:"source_excerpt,omitempty"`
	ChangedFields []string          `json:"changed_fields"`
	Revision      int64             `json:"revision"`
}

func compactNoteHistory(events []state.AuditEvent) []noteHistoryItem {
	if len(events) > maxNoteHistoryForAgents {
		events = events[len(events)-maxNoteHistoryForAgents:]
	}
	items := make([]noteHistoryItem, 0, len(events))
	for _, event := range events {
		items = append(items, noteHistoryItem{
			Action:        event.Action,
			Actor:         event.Actor,
			ServerTime:    event.ServerTime,
			SourceExcerpt: event.SourceExcerpt,
			ChangedFields: event.ChangedFields,
			Revision:      event.Revision,
		})
	}
	return items
}
