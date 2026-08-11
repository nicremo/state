package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	stateauth "github.com/nicremo/state/internal/auth"
	statepush "github.com/nicremo/state/internal/push"
	"github.com/nicremo/state/internal/state"
)

type Config struct {
	Auth    *stateauth.Manager
	State   *state.Service
	Push    *statepush.Service
	Version string
}

type server struct {
	auth  *stateauth.Manager
	state *state.Service
	push  *statepush.Service
	mcp   *mcp.Server
}

type getBriefingInput struct {
	AfterCursor int64 `json:"after_cursor,omitempty" jsonschema:"Return changes after this cursor. Use zero for the first briefing."`
	Limit       int   `json:"limit,omitempty" jsonschema:"Maximum reminders and changes to return. Maximum 50."`
}

type searchRemindersInput struct {
	Query string `json:"query" jsonschema:"Words to find in titles, descriptions, comments, and audit summaries."`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum results to return. Maximum 100."`
}

type getReminderInput struct {
	ReminderID string `json:"reminder_id" jsonschema:"UUIDv7 of the reminder."`
}

type getChangesInput struct {
	AfterCursor int64 `json:"after_cursor,omitempty" jsonschema:"Return changes after this cursor."`
	Limit       int   `json:"limit,omitempty" jsonschema:"Maximum changes to return. Maximum 100."`
}

type createReminderInput struct {
	Title           string                `json:"title" jsonschema:"Short reminder title."`
	Description     string                `json:"description,omitempty" jsonschema:"Detailed Markdown context."`
	Schedule        *state.Schedule       `json:"schedule,omitempty" jsonschema:"Optional local date, local time, IANA time zone, mode, and prewarning."`
	Recurrence      *state.RecurrenceRule `json:"recurrence,omitempty" jsonschema:"Optional daily, weekly, monthly, or yearly recurrence."`
	ClientRequestID string                `json:"client_request_id" jsonschema:"Stable UUIDv7 for idempotent retries."`
	SourceText      string                `json:"source_text" jsonschema:"Relevant original user wording that caused this write."`
	CorrelationID   string                `json:"correlation_id,omitempty" jsonschema:"Optional UUIDv7 shared by related actions."`
}

type updateReminderInput struct {
	ReminderID       string                `json:"reminder_id" jsonschema:"UUIDv7 of the reminder."`
	Title            *string               `json:"title,omitempty" jsonschema:"Replacement title."`
	Description      *string               `json:"description,omitempty" jsonschema:"Replacement Markdown description."`
	Schedule         *state.Schedule       `json:"schedule,omitempty" jsonschema:"Replacement schedule."`
	ClearSchedule    bool                  `json:"clear_schedule,omitempty" jsonschema:"Remove the schedule when true."`
	Recurrence       *state.RecurrenceRule `json:"recurrence,omitempty" jsonschema:"Replacement recurrence."`
	ClearRecurrence  bool                  `json:"clear_recurrence,omitempty" jsonschema:"Remove recurrence when true."`
	ExpectedRevision int64                 `json:"expected_revision" jsonschema:"Current reminder revision. Stale revisions are rejected."`
	ClientRequestID  string                `json:"client_request_id" jsonschema:"Stable UUIDv7 for idempotent retries."`
	SourceText       string                `json:"source_text" jsonschema:"Relevant original user wording that caused this write."`
	CorrelationID    string                `json:"correlation_id,omitempty" jsonschema:"Optional UUIDv7 shared by related actions."`
}

type addCommentInput struct {
	ReminderID      string `json:"reminder_id" jsonschema:"UUIDv7 of the reminder."`
	Body            string `json:"body" jsonschema:"Comment text or Markdown context."`
	ClientRequestID string `json:"client_request_id" jsonschema:"Stable UUIDv7 for idempotent retries."`
	SourceText      string `json:"source_text" jsonschema:"Relevant original user wording that caused this write."`
	CorrelationID   string `json:"correlation_id,omitempty" jsonschema:"Optional UUIDv7 shared by related actions."`
}

type completeOccurrenceInput struct {
	OccurrenceID     string `json:"occurrence_id" jsonschema:"UUIDv7 of the occurrence."`
	ExpectedRevision int64  `json:"expected_revision" jsonschema:"Current occurrence revision."`
	ClientRequestID  string `json:"client_request_id" jsonschema:"Stable UUIDv7 for idempotent retries."`
	SourceText       string `json:"source_text" jsonschema:"Relevant original user wording that caused this write."`
	CorrelationID    string `json:"correlation_id,omitempty" jsonschema:"Optional UUIDv7 shared by related actions."`
}

type snoozeOccurrenceInput struct {
	OccurrenceID     string    `json:"occurrence_id" jsonschema:"UUIDv7 of the occurrence."`
	Until            time.Time `json:"until" jsonschema:"New UTC wake time in RFC 3339 format."`
	ExpectedRevision int64     `json:"expected_revision" jsonschema:"Current occurrence revision."`
	ClientRequestID  string    `json:"client_request_id" jsonschema:"Stable UUIDv7 for idempotent retries."`
	SourceText       string    `json:"source_text" jsonschema:"Relevant original user wording that caused this write."`
	CorrelationID    string    `json:"correlation_id,omitempty" jsonschema:"Optional UUIDv7 shared by related actions."`
}

func NewHandler(config Config) http.Handler {
	instance := &server{auth: config.Auth, state: config.State, push: config.Push}
	instance.mcp = mcp.NewServer(&mcp.Implementation{
		Name:    "state",
		Version: config.Version,
	}, &mcp.ServerOptions{
		Instructions: "At session start, call get_briefing with the last known cursor. When the user explicitly asks to be reminded, call create_reminder. Include the relevant original user wording in source_text and use a stable client_request_id. Report success only after the tool confirms storage. Use expected_revision for edits. Fetch get_reminder when full comments, occurrences, or history are needed.",
	})
	instance.registerTools()
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return instance.mcp
	}, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		MaxRequestBodyBytes:          1 << 20,
		PropagateRequestCancellation: true,
	})
	protected := http.NewCrossOriginProtection().Handler(streamable)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		if _, err := instance.authenticateHTTP(request); err != nil {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="state-mcp"`)
			writer.Header().Set("Content-Type", "application/json; charset=utf-8")
			writer.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(writer).Encode(map[string]string{"code": "unauthorized"})
			return
		}
		protected.ServeHTTP(writer, request)
	})
}

func (server *server) registerTools() {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolean(false)}
	mutating := &mcp.ToolAnnotations{DestructiveHint: boolean(false), IdempotentHint: true, OpenWorldHint: boolean(false)}

	mcp.AddTool(server.mcp, &mcp.Tool{
		Name:        "get_briefing",
		Description: "Return bounded current reminders and changes since a cursor for session startup.",
		Annotations: readOnly,
	}, server.getBriefing)
	mcp.AddTool(server.mcp, &mcp.Tool{
		Name:        "search_reminders",
		Description: "Search reminder titles, descriptions, comments, and audit context.",
		Annotations: readOnly,
	}, server.searchReminders)
	mcp.AddTool(server.mcp, &mcp.Tool{
		Name:        "get_reminder",
		Description: "Get a reminder with comments, occurrences, and complete audit history.",
		Annotations: readOnly,
	}, server.getReminder)
	mcp.AddTool(server.mcp, &mcp.Tool{
		Name:        "get_changes",
		Description: "Return ordered audit changes after a sync cursor.",
		Annotations: readOnly,
	}, server.getChanges)
	mcp.AddTool(server.mcp, &mcp.Tool{
		Name:        "create_reminder",
		Description: "Create exactly one idempotent reminder and record the authenticated harness as actor.",
		Annotations: mutating,
	}, server.createReminder)
	mcp.AddTool(server.mcp, &mcp.Tool{
		Name:        "update_reminder",
		Description: "Update a reminder with optimistic revision checking. Agents cannot archive reminders.",
		Annotations: mutating,
	}, server.updateReminder)
	mcp.AddTool(server.mcp, &mcp.Tool{
		Name:        "add_comment",
		Description: "Append agent context to a reminder and its audit timeline.",
		Annotations: mutating,
	}, server.addComment)
	mcp.AddTool(server.mcp, &mcp.Tool{
		Name:        "complete_occurrence",
		Description: "Mark one occurrence complete with optimistic revision checking.",
		Annotations: mutating,
	}, server.completeOccurrence)
	mcp.AddTool(server.mcp, &mcp.Tool{
		Name:        "snooze_occurrence",
		Description: "Snooze one occurrence until an explicit UTC time.",
		Annotations: mutating,
	}, server.snoozeOccurrence)
}

func (server *server) getBriefing(ctx context.Context, request *mcp.CallToolRequest, input getBriefingInput) (*mcp.CallToolResult, any, error) {
	if _, err := server.actor(ctx, request); err != nil {
		return nil, nil, err
	}
	briefing, err := server.state.GetBriefing(ctx, state.BriefingOptions{AfterCursor: input.AfterCursor, Limit: input.Limit})
	if err != nil {
		return nil, nil, err
	}
	return nil, briefing, nil
}

func (server *server) searchReminders(ctx context.Context, request *mcp.CallToolRequest, input searchRemindersInput) (*mcp.CallToolResult, any, error) {
	if _, err := server.actor(ctx, request); err != nil {
		return nil, nil, err
	}
	reminders, err := server.state.SearchReminders(ctx, input.Query, input.Limit)
	if err != nil {
		return nil, nil, err
	}
	return nil, map[string]any{"reminders": reminders}, nil
}

func (server *server) getReminder(ctx context.Context, request *mcp.CallToolRequest, input getReminderInput) (*mcp.CallToolResult, any, error) {
	if _, err := server.actor(ctx, request); err != nil {
		return nil, nil, err
	}
	reminder, err := server.state.GetReminder(ctx, input.ReminderID)
	if err != nil {
		return nil, nil, err
	}
	comments, err := server.state.ListComments(ctx, input.ReminderID)
	if err != nil {
		return nil, nil, err
	}
	occurrences, err := server.state.ListOccurrences(ctx, input.ReminderID, state.OccurrenceListOptions{Limit: 500})
	if err != nil {
		return nil, nil, err
	}
	history, err := server.state.ListAuditEvents(ctx, input.ReminderID)
	if err != nil {
		return nil, nil, err
	}
	return nil, map[string]any{
		"reminder":    reminder,
		"comments":    comments,
		"occurrences": occurrences,
		"history":     history,
	}, nil
}

func (server *server) getChanges(ctx context.Context, request *mcp.CallToolRequest, input getChangesInput) (*mcp.CallToolResult, any, error) {
	if _, err := server.actor(ctx, request); err != nil {
		return nil, nil, err
	}
	changes, err := server.state.ListChanges(ctx, input.AfterCursor, input.Limit)
	if err != nil {
		return nil, nil, err
	}
	cursor := input.AfterCursor
	if len(changes) > 0 {
		cursor = changes[len(changes)-1].Cursor
	}
	return nil, map[string]any{"changes": changes, "cursor": cursor}, nil
}

func (server *server) createReminder(ctx context.Context, request *mcp.CallToolRequest, input createReminderInput) (*mcp.CallToolResult, any, error) {
	actor, err := server.actor(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	reminder, err := server.state.CreateReminder(ctx, actor, state.CreateReminderInput{
		Title:           input.Title,
		Description:     input.Description,
		Schedule:        input.Schedule,
		Recurrence:      input.Recurrence,
		Source:          "mcp",
		SourceExcerpt:   input.SourceText,
		ClientRequestID: input.ClientRequestID,
		CorrelationID:   input.CorrelationID,
	})
	if err != nil {
		return nil, nil, err
	}
	server.notifySync(ctx, actor.ID)
	return nil, map[string]any{"stored": true, "reminder": reminder}, nil
}

func (server *server) updateReminder(ctx context.Context, request *mcp.CallToolRequest, input updateReminderInput) (*mcp.CallToolResult, any, error) {
	actor, err := server.actor(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	update := state.UpdateReminderInput{
		Title:            input.Title,
		Description:      input.Description,
		ExpectedRevision: input.ExpectedRevision,
		Source:           "mcp",
		SourceExcerpt:    input.SourceText,
		ClientRequestID:  input.ClientRequestID,
		CorrelationID:    input.CorrelationID,
	}
	if input.ClearSchedule {
		var cleared *state.Schedule
		update.Schedule = &cleared
	} else if input.Schedule != nil {
		schedule := input.Schedule
		update.Schedule = &schedule
	}
	if input.ClearRecurrence {
		var cleared *state.RecurrenceRule
		update.Recurrence = &cleared
	} else if input.Recurrence != nil {
		recurrence := input.Recurrence
		update.Recurrence = &recurrence
	}
	reminder, err := server.state.UpdateReminder(ctx, actor, input.ReminderID, update)
	if err != nil {
		return nil, nil, err
	}
	server.notifySync(ctx, actor.ID)
	return nil, map[string]any{"stored": true, "reminder": reminder}, nil
}

func (server *server) addComment(ctx context.Context, request *mcp.CallToolRequest, input addCommentInput) (*mcp.CallToolResult, any, error) {
	actor, err := server.actor(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	comment, err := server.state.AddComment(ctx, actor, input.ReminderID, state.AddCommentInput{
		Body:            input.Body,
		Source:          "mcp",
		SourceExcerpt:   input.SourceText,
		ClientRequestID: input.ClientRequestID,
		CorrelationID:   input.CorrelationID,
	})
	if err != nil {
		return nil, nil, err
	}
	server.notifySync(ctx, actor.ID)
	return nil, map[string]any{"stored": true, "comment": comment}, nil
}

func (server *server) completeOccurrence(ctx context.Context, request *mcp.CallToolRequest, input completeOccurrenceInput) (*mcp.CallToolResult, any, error) {
	actor, err := server.actor(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	occurrence, err := server.state.CompleteOccurrence(ctx, actor, input.OccurrenceID, state.CompleteOccurrenceInput{
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
	return nil, map[string]any{"stored": true, "occurrence": occurrence}, nil
}

func (server *server) snoozeOccurrence(ctx context.Context, request *mcp.CallToolRequest, input snoozeOccurrenceInput) (*mcp.CallToolResult, any, error) {
	actor, err := server.actor(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	occurrence, err := server.state.SnoozeOccurrence(ctx, actor, input.OccurrenceID, state.SnoozeOccurrenceInput{
		Until:            input.Until,
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
	return nil, map[string]any{"stored": true, "occurrence": occurrence}, nil
}

func (server *server) notifySync(parent context.Context, excludedActorID string) {
	if server.push == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	_ = server.push.NotifySync(ctx, excludedActorID)
}

func (server *server) actor(ctx context.Context, request *mcp.CallToolRequest) (state.Actor, error) {
	if request == nil || request.Extra == nil {
		return state.Actor{}, stateauth.ErrInvalidCredential
	}
	token := bearerToken(request.Extra.Header.Get("Authorization"))
	return server.auth.Authenticate(ctx, token)
}

func (server *server) authenticateHTTP(request *http.Request) (state.Actor, error) {
	if server.auth == nil || server.state == nil {
		return state.Actor{}, errors.New("MCP server is not configured")
	}
	return server.auth.Authenticate(request.Context(), bearerToken(request.Header.Get("Authorization")))
}

func bearerToken(header string) string {
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
}

func boolean(value bool) *bool {
	return &value
}
