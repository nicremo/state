package statectl

import (
	"context"
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
