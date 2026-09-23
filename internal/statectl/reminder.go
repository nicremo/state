package statectl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nicremo/state/internal/state"
)

// ToolCaller is the subset of *mcp.ClientSession the reminder commands need.
type ToolCaller interface {
	CallTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error)
}

// ScheduleOptions is the schedule a caller asked for before validation. An
// empty Date means the reminder has no schedule at all.
type ScheduleOptions struct {
	Date       string // YYYY-MM-DD, empty means no schedule
	Time       string // HH:MM, optional
	TimeZone   string // IANA name, empty means the local zone of the machine
	Prewarning int    // minutes, 0 means none
	Repeat     string // "", daily, weekly, monthly, yearly
	Interval   int    // >= 1 when Repeat is set, default 1
	Until      string // YYYY-MM-DD, optional
}

// CreateReminderOptions is the input for one reminder capture.
type CreateReminderOptions struct {
	Title       string
	Description string
	SourceText  string
	RequestID   string // optional, generated when empty
	Schedule    ScheduleOptions
}

const (
	reminderDateLayout = "2006-01-02"
	reminderTimeLayout = "15:04"
)

var reminderRepeats = map[string]state.RecurrenceFrequency{
	string(state.RecurrenceDaily):   state.RecurrenceDaily,
	string(state.RecurrenceWeekly):  state.RecurrenceWeekly,
	string(state.RecurrenceMonthly): state.RecurrenceMonthly,
	string(state.RecurrenceYearly):  state.RecurrenceYearly,
}

// BuildSchedule validates the schedule options and converts them into the
// state model. A schedule without a date yields no schedule and no recurrence.
// An explicit time zone pins the schedule, otherwise it floats with the local
// zone of the writer.
func BuildSchedule(options ScheduleOptions, localZone string) (*state.Schedule, *state.RecurrenceRule, error) {
	if err := validateScheduleOptions(options); err != nil {
		return nil, nil, err
	}
	if options.Date == "" {
		return nil, nil, nil
	}
	zone := options.TimeZone
	mode := state.TimeZoneModeFixed
	if zone == "" {
		if localZone == "" {
			return nil, nil, errors.New("cannot determine local time zone, pass --tz")
		}
		zone = localZone
		mode = state.TimeZoneModeFloating
	}
	schedule := &state.Schedule{
		LocalDate:         options.Date,
		LocalTime:         options.Time,
		TimeZone:          zone,
		Mode:              mode,
		PrewarningMinutes: options.Prewarning,
	}
	if options.Repeat == "" {
		return schedule, nil, nil
	}
	interval := options.Interval
	if interval == 0 {
		interval = 1
	}
	return schedule, &state.RecurrenceRule{
		Frequency: reminderRepeats[options.Repeat],
		Interval:  interval,
		UntilDate: options.Until,
	}, nil
}

// BuildCreateReminderArguments validates a capture and returns the exact
// arguments of the create_reminder MCP tool. Unknown or empty fields stay out
// of the map so the server keeps its own defaults.
func BuildCreateReminderArguments(options CreateReminderOptions, localZone string, newID func() (string, error)) (map[string]any, error) {
	title := strings.TrimSpace(options.Title)
	if title == "" {
		return nil, errors.New("title is required")
	}
	sourceText := strings.TrimSpace(options.SourceText)
	if sourceText == "" {
		return nil, errors.New("source text is required")
	}
	requestID, err := resolveRequestID(strings.TrimSpace(options.RequestID), newID)
	if err != nil {
		return nil, err
	}
	schedule, recurrence, err := BuildSchedule(options.Schedule, localZone)
	if err != nil {
		return nil, err
	}
	arguments := map[string]any{
		"title":             title,
		"source_text":       sourceText,
		"client_request_id": requestID,
	}
	if description := strings.TrimSpace(options.Description); description != "" {
		arguments["description"] = description
	}
	if schedule != nil {
		arguments["schedule"] = schedule
	}
	if recurrence != nil {
		arguments["recurrence"] = recurrence
	}
	return arguments, nil
}

func resolveRequestID(requestID string, newID func() (string, error)) (string, error) {
	if requestID == "" {
		if newID == nil {
			return "", errors.New("request id is required")
		}
		generated, err := newID()
		if err != nil {
			return "", fmt.Errorf("generate client request id: %w", err)
		}
		return generated, nil
	}
	if _, err := uuid.Parse(requestID); err != nil {
		return "", fmt.Errorf("request id %q must be a UUID", requestID)
	}
	return requestID, nil
}

func validateScheduleOptions(options ScheduleOptions) error {
	if options.Interval < 0 {
		return errors.New("interval must not be negative")
	}
	if options.Prewarning < 0 {
		return errors.New("prewarning must not be negative")
	}
	if options.Time != "" {
		if _, err := time.Parse(reminderTimeLayout, options.Time); err != nil {
			return fmt.Errorf("time %q must be HH:MM", options.Time)
		}
	}
	if options.TimeZone != "" {
		if _, err := time.LoadLocation(options.TimeZone); err != nil {
			return fmt.Errorf("time zone %q is unknown", options.TimeZone)
		}
	}
	if options.Repeat != "" {
		if _, ok := reminderRepeats[options.Repeat]; !ok {
			return fmt.Errorf("repeat %q must be daily, weekly, monthly, or yearly", options.Repeat)
		}
	}
	if options.Date != "" {
		if err := validateReminderDate("date", options.Date); err != nil {
			return err
		}
	}
	if options.Until != "" {
		if err := validateReminderDate("until", options.Until); err != nil {
			return err
		}
	}
	if options.Time != "" && options.Date == "" {
		return errors.New("time requires a date")
	}
	if options.Repeat != "" && options.Date == "" {
		return errors.New("repeat requires a date")
	}
	if options.Until != "" && options.Repeat == "" {
		return errors.New("until requires a repeat")
	}
	if options.Until != "" && options.Date != "" && options.Until < options.Date {
		return errors.New("until must not be before date")
	}
	return nil
}

func validateReminderDate(field string, value string) error {
	if _, err := time.Parse(reminderDateLayout, value); err != nil {
		return fmt.Errorf("%s %q must be YYYY-MM-DD", field, value)
	}
	return nil
}

// StoredReminder is the confirmation of a write, decoded from the reminder the
// server returned. Raw keeps the untouched object for JSON output.
type StoredReminder struct {
	ID       string          `json:"id"`
	Title    string          `json:"title"`
	Revision int64           `json:"revision"`
	Schedule *state.Schedule `json:"schedule,omitempty"`
	Raw      json.RawMessage `json:"-"` // full reminder object as returned
}

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 100
)

// ReminderService is the terminal client of the State reminder tools. It owns
// no persistence: every method maps to exactly one MCP tool call on the
// session given to NewReminderService.
type ReminderService struct {
	caller    ToolCaller
	localZone string
	newID     func() (string, error)
}

func NewReminderService(caller ToolCaller, localZone string, newID func() (string, error)) *ReminderService {
	return &ReminderService{caller: caller, localZone: localZone, newID: newID}
}

// Create captures one reminder through create_reminder. Success is reported
// only after the server confirms storage.
func (service *ReminderService) Create(ctx context.Context, options CreateReminderOptions) (StoredReminder, error) {
	arguments, err := BuildCreateReminderArguments(options, service.localZone, service.newID)
	if err != nil {
		return StoredReminder{}, err
	}
	result, err := service.caller.CallTool(ctx, &mcp.CallToolParams{Name: "create_reminder", Arguments: arguments})
	if err != nil {
		return StoredReminder{}, err
	}
	return decodeStoredReminder(result)
}

// AddContext appends comment context to an existing reminder.
func (service *ReminderService) AddContext(ctx context.Context, reminderID, body, sourceText string) error {
	if strings.TrimSpace(reminderID) == "" {
		return errors.New("reminder id is required")
	}
	if strings.TrimSpace(body) == "" {
		return errors.New("comment body is required")
	}
	requestID, err := resolveRequestID("", service.newID)
	if err != nil {
		return err
	}
	result, err := service.caller.CallTool(ctx, &mcp.CallToolParams{
		Name: "add_comment",
		Arguments: map[string]any{
			"reminder_id":       reminderID,
			"body":              body,
			"source_text":       sourceText,
			"client_request_id": requestID,
		},
	})
	if err != nil {
		return err
	}
	_, err = decodeConfirmation(result)
	return err
}

// Reschedule reads the current revision and writes the new schedule with
// optimistic revision checking. Clear flags win over replacement values.
func (service *ReminderService) Reschedule(ctx context.Context, reminderID string, options ScheduleOptions, clearSchedule, clearRepeat bool, sourceText string) (StoredReminder, error) {
	if strings.TrimSpace(reminderID) == "" {
		return StoredReminder{}, errors.New("reminder id is required")
	}
	if clearSchedule && options.Date != "" {
		return StoredReminder{}, errors.New("--clear cannot be combined with --date")
	}
	if clearRepeat && options.Repeat != "" {
		return StoredReminder{}, errors.New("--clear-repeat cannot be combined with --repeat")
	}
	schedule, recurrence, err := BuildSchedule(options, service.localZone)
	if err != nil {
		return StoredReminder{}, err
	}
	revision, err := service.currentRevision(ctx, reminderID)
	if err != nil {
		return StoredReminder{}, err
	}
	requestID, err := resolveRequestID("", service.newID)
	if err != nil {
		return StoredReminder{}, err
	}
	arguments := map[string]any{
		"reminder_id":       reminderID,
		"expected_revision": revision,
		"client_request_id": requestID,
		"source_text":       sourceText,
	}
	switch {
	case clearSchedule:
		arguments["clear_schedule"] = true
	case schedule != nil:
		arguments["schedule"] = schedule
	}
	switch {
	case clearRepeat:
		arguments["clear_recurrence"] = true
	case recurrence != nil:
		arguments["recurrence"] = recurrence
	}
	result, err := service.caller.CallTool(ctx, &mcp.CallToolParams{Name: "update_reminder", Arguments: arguments})
	if err != nil {
		return StoredReminder{}, err
	}
	return decodeStoredReminder(result)
}

// Show returns the complete reminder detail exactly as the server sent it.
func (service *ReminderService) Show(ctx context.Context, reminderID string) (json.RawMessage, error) {
	if strings.TrimSpace(reminderID) == "" {
		return nil, errors.New("reminder id is required")
	}
	return service.callRaw(ctx, "get_reminder", map[string]any{"reminder_id": reminderID})
}

// Search returns the raw search result. A missing limit becomes 20 and an
// oversized limit is clamped to 100.
func (service *ReminderService) Search(ctx context.Context, query string, limit int) (json.RawMessage, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("query is required")
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	return service.callRaw(ctx, "search_reminders", map[string]any{"query": query, "limit": limit})
}

func (service *ReminderService) callRaw(ctx context.Context, name string, arguments map[string]any) (json.RawMessage, error) {
	result, err := service.caller.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
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

func (service *ReminderService) currentRevision(ctx context.Context, reminderID string) (int64, error) {
	raw, err := service.Show(ctx, reminderID)
	if err != nil {
		return 0, err
	}
	detail := struct {
		Reminder struct {
			Revision int64 `json:"revision"`
		} `json:"reminder"`
	}{}
	if err := json.Unmarshal(raw, &detail); err != nil {
		return 0, fmt.Errorf("decode current revision: %w", err)
	}
	return detail.Reminder.Revision, nil
}

func decodeStoredReminder(result *mcp.CallToolResult) (StoredReminder, error) {
	reminder, err := decodeConfirmation(result)
	if err != nil {
		return StoredReminder{}, err
	}
	if len(reminder) == 0 {
		return StoredReminder{}, errors.New("server did not return the reminder")
	}
	stored := StoredReminder{Raw: reminder}
	if err := json.Unmarshal(reminder, &stored); err != nil {
		return StoredReminder{}, fmt.Errorf("decode stored reminder: %w", err)
	}
	return stored, nil
}

// decodeConfirmation reads the stored flag every mutating tool returns and
// hands back the reminder object when the tool sent one.
func decodeConfirmation(result *mcp.CallToolResult) (json.RawMessage, error) {
	if result == nil {
		return nil, errors.New("tool returned no result")
	}
	if result.IsError {
		return nil, errors.New(toolErrorText(result))
	}
	confirmation := struct {
		Stored   bool            `json:"stored"`
		Reminder json.RawMessage `json:"reminder"`
	}{}
	if err := decodeToolResult(result, &confirmation); err != nil {
		return nil, err
	}
	if !confirmation.Stored {
		return nil, errors.New("server did not confirm the reminder")
	}
	return confirmation.Reminder, nil
}

// decodeToolResult decodes whatever shape a tool result carries: structured
// content when the server sent it, otherwise the JSON text block.
func decodeToolResult(result *mcp.CallToolResult, target any) error {
	if result == nil {
		return errors.New("tool returned no result")
	}
	if result.StructuredContent != nil {
		encoded, err := json.Marshal(result.StructuredContent)
		if err != nil {
			return fmt.Errorf("encode tool result: %w", err)
		}
		if err := json.Unmarshal(encoded, target); err != nil {
			return fmt.Errorf("decode tool result: %w", err)
		}
		return nil
	}
	for _, content := range result.Content {
		text, ok := content.(*mcp.TextContent)
		if !ok {
			continue
		}
		if err := json.Unmarshal([]byte(text.Text), target); err != nil {
			return fmt.Errorf("decode tool result text: %w", err)
		}
		return nil
	}
	return errors.New("tool returned no structured content")
}

func toolErrorText(result *mcp.CallToolResult) string {
	parts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		text, ok := content.(*mcp.TextContent)
		if !ok {
			continue
		}
		if trimmed := strings.TrimSpace(text.Text); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	if len(parts) == 0 {
		return "tool call failed"
	}
	return strings.Join(parts, ": ")
}
