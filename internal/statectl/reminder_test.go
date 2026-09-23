package statectl

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	stateauth "github.com/nicremo/state/internal/auth"
	"github.com/nicremo/state/internal/mcpserver"
	"github.com/nicremo/state/internal/state"
	"github.com/nicremo/state/internal/store"
	"github.com/pocketbase/pocketbase"
)

const testReminderID = "01990000-0000-7000-8000-000000000001"

func fixedID(id string) func() (string, error) {
	return func() (string, error) { return id, nil }
}

func TestBuildCreateReminderArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		options   CreateReminderOptions
		localZone string
		wantError string
		want      map[string]any
	}{
		{
			name: "full schedule with recurrence",
			options: CreateReminderOptions{
				Title:       "Monthly reporting",
				Description: "Prepare the report for the board.",
				SourceText:  "I need to do the monthly report every month",
				RequestID:   testReminderID,
				Schedule: ScheduleOptions{
					Date:       "2026-10-01",
					Time:       "09:00",
					TimeZone:   "Europe/Berlin",
					Prewarning: 30,
					Repeat:     "monthly",
					Interval:   1,
				},
			},
			localZone: "Europe/Berlin",
			want: map[string]any{
				"title":             "Monthly reporting",
				"description":       "Prepare the report for the board.",
				"source_text":       "I need to do the monthly report every month",
				"client_request_id": testReminderID,
				"schedule": &state.Schedule{
					LocalDate:         "2026-10-01",
					LocalTime:         "09:00",
					TimeZone:          "Europe/Berlin",
					Mode:              state.TimeZoneModeFixed,
					PrewarningMinutes: 30,
				},
				"recurrence": &state.RecurrenceRule{Frequency: state.RecurrenceMonthly, Interval: 1},
			},
		},
		{
			name: "without schedule omits schedule and recurrence",
			options: CreateReminderOptions{
				Title:      "Call the bank",
				SourceText: "remind me to call the bank",
				RequestID:  testReminderID,
			},
			localZone: "Europe/Berlin",
			want: map[string]any{
				"title":             "Call the bank",
				"source_text":       "remind me to call the bank",
				"client_request_id": testReminderID,
			},
		},
		{
			name: "empty request id is generated",
			options: CreateReminderOptions{
				Title:      "Call the bank",
				SourceText: "remind me to call the bank",
			},
			localZone: "Europe/Berlin",
			want: map[string]any{
				"title":             "Call the bank",
				"source_text":       "remind me to call the bank",
				"client_request_id": "01990000-0000-7000-8000-0000000000ff",
			},
		},
		{
			name: "date without time zone uses the local zone and floating mode",
			options: CreateReminderOptions{
				Title:      "Call the bank",
				SourceText: "remind me to call the bank",
				RequestID:  testReminderID,
				Schedule:   ScheduleOptions{Date: "2026-10-01", Time: "09:00"},
			},
			localZone: "Europe/Berlin",
			want: map[string]any{
				"title":             "Call the bank",
				"source_text":       "remind me to call the bank",
				"client_request_id": testReminderID,
				"schedule": &state.Schedule{
					LocalDate: "2026-10-01",
					LocalTime: "09:00",
					TimeZone:  "Europe/Berlin",
					Mode:      state.TimeZoneModeFloating,
				},
			},
		},
		{
			name: "explicit time zone pins the schedule",
			options: CreateReminderOptions{
				Title:      "Call the bank",
				SourceText: "remind me to call the bank",
				RequestID:  testReminderID,
				Schedule:   ScheduleOptions{Date: "2026-10-01", TimeZone: "Asia/Tokyo"},
			},
			localZone: "Europe/Berlin",
			want: map[string]any{
				"title":             "Call the bank",
				"source_text":       "remind me to call the bank",
				"client_request_id": testReminderID,
				"schedule": &state.Schedule{
					LocalDate: "2026-10-01",
					TimeZone:  "Asia/Tokyo",
					Mode:      state.TimeZoneModeFixed,
				},
			},
		},
		{
			name: "zero interval becomes one",
			options: CreateReminderOptions{
				Title:      "Water the plants",
				SourceText: "water the plants every week",
				RequestID:  testReminderID,
				Schedule:   ScheduleOptions{Date: "2026-10-01", TimeZone: "Europe/Berlin", Repeat: "weekly"},
			},
			localZone: "Europe/Berlin",
			want: map[string]any{
				"title":             "Water the plants",
				"source_text":       "water the plants every week",
				"client_request_id": testReminderID,
				"schedule": &state.Schedule{
					LocalDate: "2026-10-01",
					TimeZone:  "Europe/Berlin",
					Mode:      state.TimeZoneModeFixed,
				},
				"recurrence": &state.RecurrenceRule{Frequency: state.RecurrenceWeekly, Interval: 1},
			},
		},
		{
			name: "recurrence keeps interval and until",
			options: CreateReminderOptions{
				Title:      "Water the plants",
				SourceText: "water the plants every two weeks",
				RequestID:  testReminderID,
				Schedule: ScheduleOptions{
					Date:     "2026-10-01",
					TimeZone: "Europe/Berlin",
					Repeat:   "weekly",
					Interval: 2,
					Until:    "2026-12-31",
				},
			},
			localZone: "Europe/Berlin",
			want: map[string]any{
				"title":             "Water the plants",
				"source_text":       "water the plants every two weeks",
				"client_request_id": testReminderID,
				"schedule": &state.Schedule{
					LocalDate: "2026-10-01",
					TimeZone:  "Europe/Berlin",
					Mode:      state.TimeZoneModeFixed,
				},
				"recurrence": &state.RecurrenceRule{
					Frequency: state.RecurrenceWeekly,
					Interval:  2,
					UntilDate: "2026-12-31",
				},
			},
		},
		{
			name:      "missing title",
			options:   CreateReminderOptions{SourceText: "remind me"},
			localZone: "Europe/Berlin",
			wantError: "title is required",
		},
		{
			name:      "blank title",
			options:   CreateReminderOptions{Title: "   ", SourceText: "remind me"},
			localZone: "Europe/Berlin",
			wantError: "title is required",
		},
		{
			name:      "missing source text",
			options:   CreateReminderOptions{Title: "Call the bank"},
			localZone: "Europe/Berlin",
			wantError: "source text is required",
		},
		{
			name: "invalid date",
			options: CreateReminderOptions{
				Title:      "Call the bank",
				SourceText: "remind me",
				Schedule:   ScheduleOptions{Date: "2026-13-01"},
			},
			localZone: "Europe/Berlin",
			wantError: "date",
		},
		{
			name: "time without date",
			options: CreateReminderOptions{
				Title:      "Call the bank",
				SourceText: "remind me",
				Schedule:   ScheduleOptions{Time: "09:00"},
			},
			localZone: "Europe/Berlin",
			wantError: "time requires a date",
		},
		{
			name: "invalid time",
			options: CreateReminderOptions{
				Title:      "Call the bank",
				SourceText: "remind me",
				Schedule:   ScheduleOptions{Date: "2026-10-01", Time: "25:00"},
			},
			localZone: "Europe/Berlin",
			wantError: "time",
		},
		{
			name: "invalid time zone",
			options: CreateReminderOptions{
				Title:      "Call the bank",
				SourceText: "remind me",
				Schedule:   ScheduleOptions{Date: "2026-10-01", TimeZone: "Mars/Base"},
			},
			localZone: "Europe/Berlin",
			wantError: "time zone",
		},
		{
			name: "invalid repeat",
			options: CreateReminderOptions{
				Title:      "Call the bank",
				SourceText: "remind me",
				Schedule:   ScheduleOptions{Date: "2026-10-01", Repeat: "hourly"},
			},
			localZone: "Europe/Berlin",
			wantError: "repeat",
		},
		{
			name: "repeat without date",
			options: CreateReminderOptions{
				Title:      "Call the bank",
				SourceText: "remind me",
				Schedule:   ScheduleOptions{Repeat: "daily"},
			},
			localZone: "Europe/Berlin",
			wantError: "repeat requires a date",
		},
		{
			name: "negative interval",
			options: CreateReminderOptions{
				Title:      "Call the bank",
				SourceText: "remind me",
				Schedule:   ScheduleOptions{Interval: -1},
			},
			localZone: "Europe/Berlin",
			wantError: "interval",
		},
		{
			name: "until before date",
			options: CreateReminderOptions{
				Title:      "Call the bank",
				SourceText: "remind me",
				Schedule: ScheduleOptions{
					Date:     "2026-10-01",
					TimeZone: "Europe/Berlin",
					Repeat:   "daily",
					Until:    "2026-09-01",
				},
			},
			localZone: "Europe/Berlin",
			wantError: "until",
		},
		{
			name: "until without repeat",
			options: CreateReminderOptions{
				Title:      "Call the bank",
				SourceText: "remind me",
				Schedule:   ScheduleOptions{Date: "2026-10-01", TimeZone: "Europe/Berlin", Until: "2026-12-01"},
			},
			localZone: "Europe/Berlin",
			wantError: "until",
		},
		{
			name: "negative prewarning",
			options: CreateReminderOptions{
				Title:      "Call the bank",
				SourceText: "remind me",
				Schedule:   ScheduleOptions{Date: "2026-10-01", Prewarning: -5},
			},
			localZone: "Europe/Berlin",
			wantError: "prewarning",
		},
		{
			name: "schedule without any known local zone",
			options: CreateReminderOptions{
				Title:      "Call the bank",
				SourceText: "remind me",
				Schedule:   ScheduleOptions{Date: "2026-10-01"},
			},
			localZone: "",
			wantError: "cannot determine local time zone, pass --tz",
		},
		{
			name: "invalid request id",
			options: CreateReminderOptions{
				Title:      "Call the bank",
				SourceText: "remind me",
				RequestID:  "not-a-uuid",
			},
			localZone: "Europe/Berlin",
			wantError: "request id",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			arguments, err := BuildCreateReminderArguments(test.options, test.localZone, fixedID("01990000-0000-7000-8000-0000000000ff"))
			if test.wantError != "" {
				if err == nil {
					t.Fatalf("BuildCreateReminderArguments() error = nil, want %q", test.wantError)
				}
				if !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("BuildCreateReminderArguments() error = %q, want it to contain %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildCreateReminderArguments() error = %v", err)
			}
			if !reflect.DeepEqual(arguments, test.want) {
				t.Fatalf("BuildCreateReminderArguments() = %#v\nwant %#v", arguments, test.want)
			}
		})
	}
}

func TestBuildScheduleWithoutDateReturnsNothing(t *testing.T) {
	t.Parallel()

	schedule, recurrence, err := BuildSchedule(ScheduleOptions{}, "Europe/Berlin")
	if err != nil {
		t.Fatalf("BuildSchedule() error = %v", err)
	}
	if schedule != nil || recurrence != nil {
		t.Fatalf("BuildSchedule() = %#v, %#v, want nil, nil", schedule, recurrence)
	}
}

func TestBuildCreateReminderArgumentsPropagatesIDFailure(t *testing.T) {
	t.Parallel()

	_, err := BuildCreateReminderArguments(CreateReminderOptions{Title: "Call the bank", SourceText: "remind me"}, "Europe/Berlin",
		func() (string, error) { return "", context.Canceled })
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("BuildCreateReminderArguments() error = %v, want the generator failure", err)
	}
}

var _ ToolCaller = (*mcp.ClientSession)(nil)

// fakeCaller records every tool call and answers from a fixed result table.
type fakeCaller struct {
	calls   []*mcp.CallToolParams
	results map[string]*mcp.CallToolResult
}

func (fake *fakeCaller) CallTool(_ context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	fake.calls = append(fake.calls, params)
	result, ok := fake.results[params.Name]
	if !ok {
		return nil, fmt.Errorf("unexpected tool %s", params.Name)
	}
	return result, nil
}

func (fake *fakeCaller) callNames() []string {
	names := make([]string, 0, len(fake.calls))
	for _, call := range fake.calls {
		names = append(names, call.Name)
	}
	return names
}

func (fake *fakeCaller) call(t *testing.T, name string) *mcp.CallToolParams {
	t.Helper()

	for _, call := range fake.calls {
		if call.Name == name {
			return call
		}
	}
	t.Fatalf("no call to %s, got %v", name, fake.callNames())
	return nil
}

// storedReminderResult mirrors the exact shape the state MCP server returns.
func storedReminderResult(stored bool, id string, title string, revision int64) *mcp.CallToolResult {
	reminder := map[string]any{
		"id":       id,
		"title":    title,
		"status":   "active",
		"revision": revision,
		"archived": false,
	}
	if stored {
		reminder["schedule"] = map[string]any{
			"local_date":         "2026-10-01",
			"local_time":         "09:00",
			"time_zone":          "Europe/Berlin",
			"mode":               "fixed",
			"prewarning_minutes": 30,
		}
	}
	return &mcp.CallToolResult{StructuredContent: map[string]any{"stored": stored, "reminder": reminder}}
}

func toolErrorResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

func sequenceID() func() (string, error) {
	index := 0
	return func() (string, error) {
		index++
		return fmt.Sprintf("01990000-0000-7000-8000-%012d", index), nil
	}
}

func newTestService(caller ToolCaller) *ReminderService {
	return NewReminderService(caller, "Europe/Berlin", sequenceID())
}

func TestCreateRequiresStoredConfirmation(t *testing.T) {
	t.Parallel()

	fake := &fakeCaller{results: map[string]*mcp.CallToolResult{
		"create_reminder": storedReminderResult(false, testReminderID, "Monthly reporting", 1),
	}}
	_, err := newTestService(fake).Create(context.Background(), CreateReminderOptions{
		Title:      "Monthly reporting",
		SourceText: "I need to do the monthly report every month",
	})
	if err == nil || !strings.Contains(err.Error(), "server did not confirm the reminder") {
		t.Fatalf("Create() error = %v, want a missing stored confirmation", err)
	}
}

func TestCreateReturnsReminder(t *testing.T) {
	t.Parallel()

	fake := &fakeCaller{results: map[string]*mcp.CallToolResult{
		"create_reminder": storedReminderResult(true, testReminderID, "Monthly reporting", 1),
	}}
	stored, err := newTestService(fake).Create(context.Background(), CreateReminderOptions{
		Title:       "Monthly reporting",
		Description: "Prepare the report for the board.",
		SourceText:  "I need to do the monthly report every month",
		RequestID:   testReminderID,
		Schedule: ScheduleOptions{
			Date:       "2026-10-01",
			Time:       "09:00",
			TimeZone:   "Europe/Berlin",
			Prewarning: 30,
			Repeat:     "monthly",
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if stored.ID != testReminderID || stored.Title != "Monthly reporting" || stored.Revision != 1 {
		t.Fatalf("Create() = %#v", stored)
	}
	if stored.Schedule == nil || stored.Schedule.LocalTime != "09:00" {
		t.Fatalf("Create() schedule = %#v", stored.Schedule)
	}
	if len(stored.Raw) == 0 {
		t.Fatal("Create() kept no raw reminder")
	}
	if names := fake.callNames(); len(names) != 1 || names[0] != "create_reminder" {
		t.Fatalf("Create() calls = %v", names)
	}
}

func TestCreateSurfacesToolErrors(t *testing.T) {
	t.Parallel()

	fake := &fakeCaller{results: map[string]*mcp.CallToolResult{
		"create_reminder": toolErrorResult("validation failed"),
	}}
	_, err := newTestService(fake).Create(context.Background(), CreateReminderOptions{
		Title:      "Monthly reporting",
		SourceText: "I need to do the monthly report every month",
	})
	if err == nil || !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("Create() error = %v, want the tool error text", err)
	}
}

func TestAddContextRejectsEmptyBody(t *testing.T) {
	t.Parallel()

	fake := &fakeCaller{results: map[string]*mcp.CallToolResult{}}
	err := newTestService(fake).AddContext(context.Background(), testReminderID, "   ", "add context")
	if err == nil || !strings.Contains(err.Error(), "body") {
		t.Fatalf("AddContext() error = %v, want an empty body error", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("AddContext() called %v, want no tool call", fake.callNames())
	}
}

func TestAddContextCallsAddComment(t *testing.T) {
	t.Parallel()

	fake := &fakeCaller{results: map[string]*mcp.CallToolResult{
		"add_comment": {StructuredContent: map[string]any{"stored": true, "comment": map[string]any{"id": "comment-1"}}},
	}}
	err := newTestService(fake).AddContext(context.Background(), testReminderID, "Ask finance for the numbers.", "add context to the report")
	if err != nil {
		t.Fatalf("AddContext() error = %v", err)
	}
	call := fake.call(t, "add_comment")
	arguments := call.Arguments.(map[string]any)
	if arguments["reminder_id"] != testReminderID {
		t.Fatalf("add_comment reminder_id = %#v", arguments["reminder_id"])
	}
	if arguments["body"] != "Ask finance for the numbers." {
		t.Fatalf("add_comment body = %#v", arguments["body"])
	}
	if arguments["source_text"] != "add context to the report" {
		t.Fatalf("add_comment source_text = %#v", arguments["source_text"])
	}
	if arguments["client_request_id"] == "" || arguments["client_request_id"] == nil {
		t.Fatalf("add_comment client_request_id = %#v", arguments["client_request_id"])
	}
}

func TestRescheduleUsesCurrentRevision(t *testing.T) {
	t.Parallel()

	fake := &fakeCaller{results: map[string]*mcp.CallToolResult{
		"get_reminder": {StructuredContent: map[string]any{
			"reminder": map[string]any{"id": testReminderID, "title": "Monthly reporting", "revision": 7},
		}},
		"update_reminder": storedReminderResult(true, testReminderID, "Monthly reporting", 8),
	}}
	stored, err := newTestService(fake).Reschedule(context.Background(), testReminderID, ScheduleOptions{
		Date:     "2026-11-01",
		Time:     "09:00",
		TimeZone: "Europe/Berlin",
	}, false, false, "move the report to November")
	if err != nil {
		t.Fatalf("Reschedule() error = %v", err)
	}
	if stored.Revision != 8 {
		t.Fatalf("Reschedule() revision = %d, want 8", stored.Revision)
	}
	if names := fake.callNames(); len(names) != 2 || names[0] != "get_reminder" || names[1] != "update_reminder" {
		t.Fatalf("Reschedule() calls = %v", names)
	}
	arguments := fake.call(t, "update_reminder").Arguments.(map[string]any)
	if arguments["expected_revision"] != int64(7) {
		t.Fatalf("update_reminder expected_revision = %#v, want 7", arguments["expected_revision"])
	}
	if arguments["reminder_id"] != testReminderID {
		t.Fatalf("update_reminder reminder_id = %#v", arguments["reminder_id"])
	}
	schedule, ok := arguments["schedule"].(*state.Schedule)
	if !ok || schedule.LocalDate != "2026-11-01" {
		t.Fatalf("update_reminder schedule = %#v", arguments["schedule"])
	}
	if _, present := arguments["clear_schedule"]; present {
		t.Fatal("update_reminder sent clear_schedule for a dated reschedule")
	}
	if _, present := arguments["title"]; present {
		t.Fatal("update_reminder sent an unchanged title")
	}
}

func TestRescheduleSendsClearFlags(t *testing.T) {
	t.Parallel()

	fake := &fakeCaller{results: map[string]*mcp.CallToolResult{
		"get_reminder": {StructuredContent: map[string]any{
			"reminder": map[string]any{"id": testReminderID, "title": "Monthly reporting", "revision": 3},
		}},
		"update_reminder": storedReminderResult(true, testReminderID, "Monthly reporting", 4),
	}}
	_, err := newTestService(fake).Reschedule(context.Background(), testReminderID, ScheduleOptions{}, true, true, "drop the schedule")
	if err != nil {
		t.Fatalf("Reschedule() error = %v", err)
	}
	arguments := fake.call(t, "update_reminder").Arguments.(map[string]any)
	if arguments["clear_schedule"] != true || arguments["clear_recurrence"] != true {
		t.Fatalf("update_reminder arguments = %#v", arguments)
	}
	if _, present := arguments["schedule"]; present {
		t.Fatal("update_reminder sent a schedule while clearing it")
	}
	if _, present := arguments["recurrence"]; present {
		t.Fatal("update_reminder sent a recurrence while clearing it")
	}
}

func TestRescheduleRejectsClearWithDate(t *testing.T) {
	t.Parallel()

	fake := &fakeCaller{results: map[string]*mcp.CallToolResult{}}
	_, err := newTestService(fake).Reschedule(context.Background(), testReminderID, ScheduleOptions{Date: "2026-11-01"}, true, false, "reschedule")
	if err == nil {
		t.Fatal("Reschedule() accepted --clear together with a date")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("Reschedule() called %v, want no tool call", fake.callNames())
	}
}

func TestShowReturnsReminderDetail(t *testing.T) {
	t.Parallel()

	fake := &fakeCaller{results: map[string]*mcp.CallToolResult{
		"get_reminder": {StructuredContent: map[string]any{
			"reminder": map[string]any{"id": testReminderID, "title": "Monthly reporting", "revision": 2},
			"comments": []any{map[string]any{"id": "comment-1", "body": "Ask finance."}},
		}},
	}}
	raw, err := newTestService(fake).Show(context.Background(), testReminderID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	detail := struct {
		Reminder struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"reminder"`
		Comments []struct {
			Body string `json:"body"`
		} `json:"comments"`
	}{}
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("Show() returned undecodable JSON: %v (%s)", err, raw)
	}
	if detail.Reminder.ID != testReminderID || len(detail.Comments) != 1 || detail.Comments[0].Body != "Ask finance." {
		t.Fatalf("Show() = %s", raw)
	}
	arguments := fake.call(t, "get_reminder").Arguments.(map[string]any)
	if arguments["reminder_id"] != testReminderID {
		t.Fatalf("get_reminder arguments = %#v", arguments)
	}
}

func TestSearchDefaultsAndClampsTheLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		limit     int
		wantLimit int
	}{
		{name: "default", limit: 0, wantLimit: 20},
		{name: "explicit", limit: 5, wantLimit: 5},
		{name: "clamped", limit: 500, wantLimit: 100},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeCaller{results: map[string]*mcp.CallToolResult{
				"search_reminders": {StructuredContent: map[string]any{"reminders": []any{}}},
			}}
			if _, err := newTestService(fake).Search(context.Background(), "report", test.limit); err != nil {
				t.Fatalf("Search() error = %v", err)
			}
			arguments := fake.call(t, "search_reminders").Arguments.(map[string]any)
			if arguments["query"] != "report" {
				t.Fatalf("search_reminders query = %#v", arguments["query"])
			}
			if arguments["limit"] != test.wantLimit {
				t.Fatalf("search_reminders limit = %#v, want %d", arguments["limit"], test.wantLimit)
			}
		})
	}
}

func TestSearchRejectsEmptyQuery(t *testing.T) {
	t.Parallel()

	fake := &fakeCaller{results: map[string]*mcp.CallToolResult{}}
	if _, err := newTestService(fake).Search(context.Background(), "   ", 20); err == nil {
		t.Fatal("Search() accepted an empty query")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("Search() called %v, want no tool call", fake.callNames())
	}
}

func mustUUIDv7() func() (string, error) {
	return func() (string, error) {
		id, err := uuid.NewV7()
		if err != nil {
			return "", err
		}
		return id.String(), nil
	}
}

// newReminderTestSession boots a real State MCP server in memory and connects
// to it exactly like the CLI does, over streamable HTTP with a bearer token.
func newReminderTestSession(t *testing.T) *mcp.ClientSession {
	t.Helper()

	handler, token := newReminderTestHandler(t)
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	session, err := ConnectRemote(context.Background(), Profile{ServerURL: httpServer.URL}, token, "test-version")
	if err != nil {
		t.Fatalf("ConnectRemote() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func newReminderTestHandler(t *testing.T) (http.Handler, string) {
	t.Helper()

	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:   t.TempDir(),
		HideStartBanner:  true,
		DataMaxOpenConns: 1,
		DataMaxIdleConns: 1,
		AuxMaxOpenConns:  1,
		AuxMaxIdleConns:  1,
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	t.Cleanup(func() {
		if err := app.ResetBootstrapState(); err != nil {
			t.Errorf("ResetBootstrapState() error = %v", err)
		}
	})
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 21)
	}
	repository, err := store.NewPocketBaseRepository(app, ed25519.NewKeyFromSeed(seed))
	if err != nil {
		t.Fatalf("NewPocketBaseRepository() error = %v", err)
	}
	authManager, err := stateauth.NewManager(app, "bootstrap-secret")
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	ownerCredential, err := authManager.BootstrapOwner(context.Background(), "bootstrap-secret", stateauth.OwnerBootstrapRequest{
		DisplayName: "Fabian",
		DeviceName:  "iPhone",
	})
	if err != nil {
		t.Fatalf("BootstrapOwner() error = %v", err)
	}
	owner, err := authManager.Authenticate(context.Background(), ownerCredential.Token)
	if err != nil {
		t.Fatalf("Authenticate(owner) error = %v", err)
	}
	pairing, err := authManager.CreatePairingCode(context.Background(), owner, stateauth.PairingCodeRequest{
		Harness:     "codex",
		DisplayName: "Codex",
		DeviceName:  "MacBook",
	})
	if err != nil {
		t.Fatalf("CreatePairingCode() error = %v", err)
	}
	credential, err := authManager.ExchangePairingCode(context.Background(), pairing.Code)
	if err != nil {
		t.Fatalf("ExchangePairingCode() error = %v", err)
	}
	handler := mcpserver.NewHandler(mcpserver.Config{
		Auth:    authManager,
		State:   state.NewService(repository),
		Version: "test-version",
	})
	return handler, credential.Token
}

type reminderDetail struct {
	Reminder state.Reminder  `json:"reminder"`
	Comments []state.Comment `json:"comments"`
}

func (detail reminderDetail) commentBodies() []string {
	bodies := make([]string, 0, len(detail.Comments))
	for _, comment := range detail.Comments {
		bodies = append(bodies, comment.Body)
	}
	return bodies
}

func TestReminderServiceAgainstInMemoryServer(t *testing.T) {
	t.Parallel()

	session := newReminderTestSession(t)
	service := NewReminderService(session, "Europe/Berlin", mustUUIDv7())
	ctx := context.Background()

	created, err := service.Create(ctx, CreateReminderOptions{
		Title:       "Monthly reporting",
		Description: "Prepare the report for the board.",
		SourceText:  "I need to do the monthly report every month",
		Schedule: ScheduleOptions{
			Date:       "2026-10-01",
			Time:       "09:00",
			TimeZone:   "Europe/Berlin",
			Prewarning: 30,
			Repeat:     "monthly",
			Interval:   1,
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID == "" || created.Title != "Monthly reporting" || created.Revision != 1 {
		t.Fatalf("Create() = %#v", created)
	}
	if created.Schedule == nil || created.Schedule.Mode != state.TimeZoneModeFixed {
		t.Fatalf("Create() schedule = %#v", created.Schedule)
	}

	raw, err := service.Show(ctx, created.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	var detail reminderDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("Show() returned undecodable JSON: %v", err)
	}
	if detail.Reminder.Recurrence == nil {
		t.Fatalf("Show() recurrence = %#v", detail.Reminder.Recurrence)
	}
	if detail.Reminder.Recurrence.Frequency != state.RecurrenceMonthly || detail.Reminder.Recurrence.Interval != 1 {
		t.Fatalf("Show() recurrence = %#v", detail.Reminder.Recurrence)
	}
	if detail.Reminder.Schedule == nil || detail.Reminder.Schedule.LocalTime != "09:00" {
		t.Fatalf("Show() schedule = %#v", detail.Reminder.Schedule)
	}

	if err := service.AddContext(ctx, created.ID, "Ask finance for the numbers.", "add context to the report"); err != nil {
		t.Fatalf("AddContext() error = %v", err)
	}
	raw, err = service.Show(ctx, created.ID)
	if err != nil {
		t.Fatalf("Show() after AddContext error = %v", err)
	}
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("Show() after AddContext returned undecodable JSON: %v", err)
	}
	if bodies := detail.commentBodies(); len(bodies) != 1 || bodies[0] != "Ask finance for the numbers." {
		t.Fatalf("Show() comments = %v", bodies)
	}

	rescheduled, err := service.Reschedule(ctx, created.ID, ScheduleOptions{
		Date:     "2026-11-01",
		Time:     "09:00",
		TimeZone: "Europe/Berlin",
		Repeat:   "monthly",
		Interval: 1,
	}, false, false, "move the report to November")
	if err != nil {
		t.Fatalf("Reschedule() error = %v", err)
	}
	if rescheduled.Revision <= created.Revision {
		t.Fatalf("Reschedule() revision = %d, want more than %d", rescheduled.Revision, created.Revision)
	}
	if rescheduled.Schedule == nil || rescheduled.Schedule.LocalDate != "2026-11-01" {
		t.Fatalf("Reschedule() schedule = %#v", rescheduled.Schedule)
	}

	matches, err := service.Search(ctx, "reporting", 20)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	searchResult := struct {
		Reminders []state.Reminder `json:"reminders"`
	}{}
	if err := json.Unmarshal(matches, &searchResult); err != nil {
		t.Fatalf("Search() returned undecodable JSON: %v", err)
	}
	if len(searchResult.Reminders) != 1 || searchResult.Reminders[0].ID != created.ID {
		t.Fatalf("Search() = %s", matches)
	}
}

func TestReminderServiceCreateIsIdempotent(t *testing.T) {
	t.Parallel()

	session := newReminderTestSession(t)
	service := NewReminderService(session, "Europe/Berlin", mustUUIDv7())
	ctx := context.Background()

	options := CreateReminderOptions{
		Title:      "Idempotent capture",
		SourceText: "remind me to file the taxes",
		RequestID:  "01990000-0000-7000-8000-0000000000ab",
		Schedule:   ScheduleOptions{Date: "2026-10-01", TimeZone: "Europe/Berlin"},
	}
	first, err := service.Create(ctx, options)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	second, err := service.Create(ctx, options)
	if err != nil {
		t.Fatalf("second Create() error = %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("Create() ids = %s and %s, want the same reminder", first.ID, second.ID)
	}
}
