package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicremo/state/internal/state"
	"github.com/nicremo/state/internal/statectl"
)

func runStatectl(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&stderr, nil))
	err := run(args, &stdout, &stderr, logger)
	return stdout.String(), stderr.String(), err
}

func missingConfig(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "missing-statectl.json")
}

func TestRunReminderRequiresASubcommand(t *testing.T) {
	t.Parallel()

	_, _, err := runStatectl(t, "reminder")
	if err == nil || !strings.Contains(err.Error(), "usage: statectl reminder") {
		t.Fatalf("run(reminder) error = %v, want usage", err)
	}
}

func TestRunReminderRejectsUnknownSubcommand(t *testing.T) {
	t.Parallel()

	_, _, err := runStatectl(t, "reminder", "unknown")
	if err == nil || !strings.Contains(err.Error(), "usage: statectl reminder") {
		t.Fatalf("run(reminder unknown) error = %v, want usage", err)
	}
}

func TestRunReminderCreateRequiresProfile(t *testing.T) {
	t.Parallel()

	_, _, err := runStatectl(t, "reminder", "create", "--title", "Monthly reporting", "--source-text", "every month")
	if err == nil || !strings.Contains(err.Error(), "profile is required") {
		t.Fatalf("run(reminder create) error = %v, want a profile error", err)
	}
}

func TestRunReminderCreateRequiresTitleAndSourceText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing title",
			args: []string{"reminder", "create", "--profile", "codex", "--source-text", "every month"},
			want: "--title",
		},
		{
			name: "missing source text",
			args: []string{"reminder", "create", "--profile", "codex", "--title", "Monthly reporting"},
			want: "--source-text",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := runStatectl(t, test.args...)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run(%v) error = %v, want %q", test.args, err, test.want)
			}
		})
	}
}

func TestRunReminderCreateRejectsTwoDescriptionSources(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "description.md")
	if err := os.WriteFile(path, []byte("From a file."), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	_, _, err := runStatectl(t, "reminder", "create",
		"--profile", "codex",
		"--title", "Monthly reporting",
		"--source-text", "every month",
		"--description", "Inline.",
		"--description-file", path,
	)
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("run(reminder create) error = %v, want a conflict error", err)
	}
}

func TestRunReminderCreateReportsUnreadableDescriptionFile(t *testing.T) {
	t.Parallel()

	_, _, err := runStatectl(t, "reminder", "create",
		"--profile", "codex",
		"--title", "Monthly reporting",
		"--source-text", "every month",
		"--description-file", filepath.Join(t.TempDir(), "missing.md"),
	)
	if err == nil {
		t.Fatal("run(reminder create) accepted a missing description file")
	}
}

func TestRunReminderAddContextRequiresBody(t *testing.T) {
	t.Parallel()

	_, _, err := runStatectl(t, "reminder", "add-context",
		"--profile", "codex",
		"--id", "01990000-0000-7000-8000-000000000001",
		"--source-text", "add context",
	)
	if err == nil || !strings.Contains(err.Error(), "--body") {
		t.Fatalf("run(reminder add-context) error = %v, want a body error", err)
	}
}

func TestRunReminderScheduleRejectsClearWithDateBeforeConnecting(t *testing.T) {
	t.Parallel()

	// The config path does not exist, so reaching the connection would fail
	// with a different error. The flag conflict must win.
	_, _, err := runStatectl(t, "reminder", "schedule",
		"--profile", "codex",
		"--config", missingConfig(t),
		"--id", "01990000-0000-7000-8000-000000000001",
		"--source-text", "move it",
		"--clear",
		"--date", "2026-10-01",
	)
	if err == nil || !strings.Contains(err.Error(), "--clear") {
		t.Fatalf("run(reminder schedule) error = %v, want the --clear conflict before connecting", err)
	}
}

func TestRunReminderScheduleRejectsClearRepeatWithRepeat(t *testing.T) {
	t.Parallel()

	_, _, err := runStatectl(t, "reminder", "schedule",
		"--profile", "codex",
		"--config", missingConfig(t),
		"--id", "01990000-0000-7000-8000-000000000001",
		"--source-text", "stop repeating",
		"--clear-repeat",
		"--repeat", "monthly",
	)
	if err == nil || !strings.Contains(err.Error(), "--clear-repeat") {
		t.Fatalf("run(reminder schedule) error = %v, want the --clear-repeat conflict", err)
	}
}

func TestRunReminderScheduleRequiresAChange(t *testing.T) {
	t.Parallel()

	_, _, err := runStatectl(t, "reminder", "schedule",
		"--profile", "codex",
		"--id", "01990000-0000-7000-8000-000000000001",
		"--source-text", "nothing changes",
	)
	if err == nil || !strings.Contains(err.Error(), "--date") {
		t.Fatalf("run(reminder schedule) error = %v, want a missing change error", err)
	}
}

func TestRunReminderShowAndSearchRequireTheirArguments(t *testing.T) {
	t.Parallel()

	if _, _, err := runStatectl(t, "reminder", "show", "--profile", "codex"); err == nil || !strings.Contains(err.Error(), "--id") {
		t.Fatalf("run(reminder show) error = %v, want an id error", err)
	}
	if _, _, err := runStatectl(t, "reminder", "search", "--profile", "codex"); err == nil || !strings.Contains(err.Error(), "--query") {
		t.Fatalf("run(reminder search) error = %v, want a query error", err)
	}
}

func TestReadReminderTextReadsFilesAndCapsTheSize(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "body.md")
	content := strings.Repeat("a", maxReminderFileBytes)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	read, err := readReminderText(path)
	if err != nil {
		t.Fatalf("readReminderText() error = %v", err)
	}
	if read != content {
		t.Fatalf("readReminderText() length = %d, want %d", len(read), len(content))
	}

	oversized := filepath.Join(t.TempDir(), "oversized.md")
	if err := os.WriteFile(oversized, []byte(strings.Repeat("a", maxReminderFileBytes+1)), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := readReminderText(oversized); err == nil || !strings.Contains(err.Error(), "64 KB") {
		t.Fatalf("readReminderText(oversized) error = %v, want a size error", err)
	}
}

func TestWriteStoredReminderText(t *testing.T) {
	t.Parallel()

	stored := statectl.StoredReminder{
		ID:       "01990000-0000-7000-8000-000000000001",
		Title:    "Monthly reporting",
		Revision: 1,
		Schedule: &state.Schedule{
			LocalDate: "2026-10-01",
			LocalTime: "09:00",
			TimeZone:  "Europe/Berlin",
			Mode:      state.TimeZoneModeFixed,
		},
		Raw: []byte(`{"id":"01990000-0000-7000-8000-000000000001","title":"Monthly reporting","revision":1,"schedule":{"local_date":"2026-10-01","local_time":"09:00","time_zone":"Europe/Berlin","mode":"fixed"},"recurrence":{"frequency":"monthly","interval":1}}`),
	}
	var stdout bytes.Buffer
	if err := writeStoredReminder(&stdout, stored, false); err != nil {
		t.Fatalf("writeStoredReminder() error = %v", err)
	}
	want := "stored reminder 01990000-0000-7000-8000-000000000001 \"Monthly reporting\" (2026-10-01 09:00 Europe/Berlin, monthly)\n"
	if stdout.String() != want {
		t.Fatalf("writeStoredReminder() = %q, want %q", stdout.String(), want)
	}

	stdout.Reset()
	if err := writeStoredReminder(&stdout, stored, true); err != nil {
		t.Fatalf("writeStoredReminder(json) error = %v", err)
	}
	if !strings.HasPrefix(stdout.String(), "{\n  \"id\":") {
		t.Fatalf("writeStoredReminder(json) = %q, want indented JSON", stdout.String())
	}
}

func TestSummarizeScheduleWithoutSchedule(t *testing.T) {
	t.Parallel()

	if summary := summarizeSchedule(nil, nil); summary != "" {
		t.Fatalf("summarizeSchedule(nil) = %q, want an empty string", summary)
	}
}

func TestRunReminderCreateValidatesTheScheduleBeforeConnecting(t *testing.T) {
	t.Parallel()

	// Every case points --config at a path that does not exist, so a missing
	// pre-flight check would surface as a config error instead.
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "invalid date", args: []string{"--date", "2026-13-01"}, want: "date"},
		{name: "time without date", args: []string{"--time", "09:00"}, want: "time requires a date"},
		{name: "unknown time zone", args: []string{"--date", "2026-10-01", "--tz", "Mars/Base"}, want: "time zone"},
		{name: "repeat without date", args: []string{"--repeat", "monthly"}, want: "repeat requires a date"},
		{name: "until before date", args: []string{"--date", "2026-10-01", "--tz", "Europe/Berlin", "--repeat", "daily", "--until", "2026-09-01"}, want: "until"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			args := append([]string{
				"reminder", "create",
				"--profile", "codex",
				"--config", missingConfig(t),
				"--title", "Monthly reporting",
				"--source-text", "every month",
			}, test.args...)
			_, _, err := runStatectl(t, args...)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run(%v) error = %v, want %q before connecting", args, err, test.want)
			}
		})
	}
}

func TestZoneFromLocaltimeLink(t *testing.T) {
	cases := map[string]string{
		"/var/db/timezone/zoneinfo/Europe/Berlin": "Europe/Berlin",
		"/usr/share/zoneinfo/America/New_York":    "America/New_York",
		"../usr/share/zoneinfo/UTC":               "UTC",
		"/usr/share/zoneinfo/Mars/Base":           "",
		"/etc/something-else":                     "",
		"":                                        "",
	}
	for target, want := range cases {
		if got := zoneFromLocaltimeLink(target); got != want {
			t.Errorf("zoneFromLocaltimeLink(%q) = %q, want %q", target, got, want)
		}
	}
}
