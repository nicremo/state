package statectl

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nicremo/state/internal/state"
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
