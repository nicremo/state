package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nicremo/state/internal/state"
	"github.com/nicremo/state/internal/statectl"
)

// reminderCallTimeout bounds every single tool call the reminder commands make.
const reminderCallTimeout = 30 * time.Second

// maxReminderFileBytes caps --description-file and --body-file content.
const maxReminderFileBytes = 64 * 1024

func runReminder(args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: statectl reminder <create|add-context|schedule|show|search>")
	}
	switch args[0] {
	case "create":
		return runReminderCreate(args[1:], stdout, stderr)
	case "add-context":
		return runReminderAddContext(args[1:], stdout, stderr)
	case "schedule":
		return runReminderSchedule(args[1:], stdout, stderr)
	case "show":
		return runReminderShow(args[1:], stdout, stderr)
	case "search":
		return runReminderSearch(args[1:], stdout, stderr)
	default:
		return errors.New("usage: statectl reminder <create|add-context|schedule|show|search>")
	}
}

func runReminderCreate(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl reminder create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profileName := flags.String("profile", "", "statectl profile name")
	configPath := flags.String("config", defaultConfigPath(), "statectl config path")
	title := flags.String("title", "", "reminder title")
	sourceText := flags.String("source-text", "", "original wording that caused the reminder")
	description := flags.String("description", "", "detailed Markdown context")
	descriptionFile := flags.String("description-file", "", "read the description from a file, - for stdin")
	date := flags.String("date", "", "local date, YYYY-MM-DD")
	localTime := flags.String("time", "", "local time, HH:MM")
	timeZone := flags.String("tz", "", "IANA time zone, defaults to the local zone")
	prewarning := flags.Int("prewarning", 0, "advance notice in minutes")
	repeat := flags.String("repeat", "", "daily, weekly, monthly or yearly")
	interval := flags.Int("interval", 1, "recurrence interval")
	until := flags.String("until", "", "last date of the recurrence, YYYY-MM-DD")
	requestID := flags.String("request-id", "", "stable UUID for idempotent retries")
	asJSON := flags.Bool("json", false, "print the stored reminder as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireReminderProfile(*profileName); err != nil {
		return err
	}
	if strings.TrimSpace(*title) == "" {
		return errors.New("statectl reminder create requires --title")
	}
	if strings.TrimSpace(*sourceText) == "" {
		return errors.New("statectl reminder create requires --source-text")
	}
	resolvedDescription, err := resolveTextInput("--description", *description, "--description-file", *descriptionFile)
	if err != nil {
		return err
	}
	service, closeSession, err := connectReminderService(*configPath, *profileName)
	if err != nil {
		return err
	}
	defer closeSession()

	ctx, cancel := context.WithTimeout(context.Background(), reminderCallTimeout)
	defer cancel()
	stored, err := service.Create(ctx, statectl.CreateReminderOptions{
		Title:       *title,
		Description: resolvedDescription,
		SourceText:  *sourceText,
		RequestID:   *requestID,
		Schedule: statectl.ScheduleOptions{
			Date:       *date,
			Time:       *localTime,
			TimeZone:   *timeZone,
			Prewarning: *prewarning,
			Repeat:     *repeat,
			Interval:   *interval,
			Until:      *until,
		},
	})
	if err != nil {
		return err
	}
	return writeStoredReminder(stdout, stored, *asJSON)
}

func runReminderAddContext(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl reminder add-context", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profileName := flags.String("profile", "", "statectl profile name")
	configPath := flags.String("config", defaultConfigPath(), "statectl config path")
	reminderID := flags.String("id", "", "reminder UUIDv7")
	sourceText := flags.String("source-text", "", "original wording that caused the comment")
	body := flags.String("body", "", "comment text or Markdown context")
	bodyFile := flags.String("body-file", "", "read the comment from a file, - for stdin")
	asJSON := flags.Bool("json", false, "print the accepted comment as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireReminderProfile(*profileName); err != nil {
		return err
	}
	if strings.TrimSpace(*reminderID) == "" {
		return errors.New("statectl reminder add-context requires --id")
	}
	if strings.TrimSpace(*sourceText) == "" {
		return errors.New("statectl reminder add-context requires --source-text")
	}
	resolvedBody, err := resolveTextInput("--body", *body, "--body-file", *bodyFile)
	if err != nil {
		return err
	}
	if strings.TrimSpace(resolvedBody) == "" {
		return errors.New("statectl reminder add-context requires --body or --body-file")
	}
	service, closeSession, err := connectReminderService(*configPath, *profileName)
	if err != nil {
		return err
	}
	defer closeSession()

	ctx, cancel := context.WithTimeout(context.Background(), reminderCallTimeout)
	defer cancel()
	if err := service.AddContext(ctx, *reminderID, strings.TrimSpace(resolvedBody), *sourceText); err != nil {
		return err
	}
	if *asJSON {
		encoded, err := json.Marshal(map[string]any{"stored": true, "reminder_id": *reminderID})
		if err != nil {
			return err
		}
		return writeIndentedJSON(stdout, encoded)
	}
	_, err = fmt.Fprintf(stdout, "stored comment on reminder %s\n", *reminderID)
	return err
}

func runReminderSchedule(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl reminder schedule", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profileName := flags.String("profile", "", "statectl profile name")
	configPath := flags.String("config", defaultConfigPath(), "statectl config path")
	reminderID := flags.String("id", "", "reminder UUIDv7")
	sourceText := flags.String("source-text", "", "original wording that caused the change")
	clear := flags.Bool("clear", false, "remove the schedule")
	clearRepeat := flags.Bool("clear-repeat", false, "remove the recurrence")
	date := flags.String("date", "", "local date, YYYY-MM-DD")
	localTime := flags.String("time", "", "local time, HH:MM")
	timeZone := flags.String("tz", "", "IANA time zone, defaults to the local zone")
	prewarning := flags.Int("prewarning", 0, "advance notice in minutes")
	repeat := flags.String("repeat", "", "daily, weekly, monthly or yearly")
	interval := flags.Int("interval", 1, "recurrence interval")
	until := flags.String("until", "", "last date of the recurrence, YYYY-MM-DD")
	asJSON := flags.Bool("json", false, "print the stored reminder as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireReminderProfile(*profileName); err != nil {
		return err
	}
	if strings.TrimSpace(*reminderID) == "" {
		return errors.New("statectl reminder schedule requires --id")
	}
	if strings.TrimSpace(*sourceText) == "" {
		return errors.New("statectl reminder schedule requires --source-text")
	}
	if *clear && *date != "" {
		return errors.New("statectl reminder schedule cannot combine --clear with --date")
	}
	if *clearRepeat && *repeat != "" {
		return errors.New("statectl reminder schedule cannot combine --clear-repeat with --repeat")
	}
	if !*clear && !*clearRepeat && *date == "" {
		return errors.New("statectl reminder schedule requires --clear, --clear-repeat or --date")
	}
	service, closeSession, err := connectReminderService(*configPath, *profileName)
	if err != nil {
		return err
	}
	defer closeSession()

	ctx, cancel := context.WithTimeout(context.Background(), reminderCallTimeout)
	defer cancel()
	stored, err := service.Reschedule(ctx, *reminderID, statectl.ScheduleOptions{
		Date:       *date,
		Time:       *localTime,
		TimeZone:   *timeZone,
		Prewarning: *prewarning,
		Repeat:     *repeat,
		Interval:   *interval,
		Until:      *until,
	}, *clear, *clearRepeat, *sourceText)
	if err != nil {
		return err
	}
	return writeStoredReminder(stdout, stored, *asJSON)
}

func runReminderShow(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl reminder show", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profileName := flags.String("profile", "", "statectl profile name")
	configPath := flags.String("config", defaultConfigPath(), "statectl config path")
	reminderID := flags.String("id", "", "reminder UUIDv7")
	asJSON := flags.Bool("json", false, "print the full reminder detail as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireReminderProfile(*profileName); err != nil {
		return err
	}
	if strings.TrimSpace(*reminderID) == "" {
		return errors.New("statectl reminder show requires --id")
	}
	service, closeSession, err := connectReminderService(*configPath, *profileName)
	if err != nil {
		return err
	}
	defer closeSession()

	ctx, cancel := context.WithTimeout(context.Background(), reminderCallTimeout)
	defer cancel()
	raw, err := service.Show(ctx, *reminderID)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeIndentedJSON(stdout, raw)
	}
	return writeReminderDetail(stdout, raw)
}

func runReminderSearch(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl reminder search", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profileName := flags.String("profile", "", "statectl profile name")
	configPath := flags.String("config", defaultConfigPath(), "statectl config path")
	query := flags.String("query", "", "words to find in titles, descriptions, comments and history")
	limit := flags.Int("limit", 20, "maximum results, at most 100")
	asJSON := flags.Bool("json", false, "print the raw search result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireReminderProfile(*profileName); err != nil {
		return err
	}
	if strings.TrimSpace(*query) == "" {
		return errors.New("statectl reminder search requires --query")
	}
	service, closeSession, err := connectReminderService(*configPath, *profileName)
	if err != nil {
		return err
	}
	defer closeSession()

	ctx, cancel := context.WithTimeout(context.Background(), reminderCallTimeout)
	defer cancel()
	raw, err := service.Search(ctx, *query, *limit)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeIndentedJSON(stdout, raw)
	}
	return writeSearchResults(stdout, raw, *query)
}

func requireReminderProfile(profileName string) error {
	if strings.TrimSpace(profileName) == "" {
		return errors.New("profile is required")
	}
	return nil
}

// connectReminderService pairs the local profile with a live MCP session. The
// returned closer owns the session.
func connectReminderService(configPath string, profileName string) (*statectl.ReminderService, func(), error) {
	profile, token, err := loadProfileAndCredential(configPath, profileName)
	if err != nil {
		return nil, nil, err
	}
	session, err := statectl.ConnectRemote(context.Background(), profile, token, version)
	if err != nil {
		return nil, nil, err
	}
	service := statectl.NewReminderService(session, detectLocalTimeZone(), newUUIDv7)
	return service, func() { _ = session.Close() }, nil
}

func newUUIDv7() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// detectLocalTimeZone returns the IANA zone of this machine, or an empty
// string when the zone cannot be named. An empty result makes a dated reminder
// without --tz fail with explicit guidance instead of storing a wrong zone.
func detectLocalTimeZone() string {
	if zone := strings.TrimPrefix(time.Local.String(), ":"); zone != "" && zone != "Local" {
		if _, err := time.LoadLocation(zone); err == nil {
			return zone
		}
	}
	zone := strings.TrimPrefix(strings.TrimSpace(os.Getenv("TZ")), ":")
	if zone == "" {
		return ""
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return ""
	}
	return zone
}

// resolveTextInput reads an inline value or a file, never both. "-" means stdin.
func resolveTextInput(inlineFlag string, inlineValue string, fileFlag string, filePath string) (string, error) {
	if strings.TrimSpace(inlineValue) == "" && filePath == "" {
		return "", nil
	}
	if strings.TrimSpace(inlineValue) != "" && filePath != "" {
		return "", fmt.Errorf("%s and %s cannot be combined", inlineFlag, fileFlag)
	}
	return readReminderText(filePath)
}

func readReminderText(path string) (string, error) {
	var reader io.Reader
	if path == "-" {
		reader = os.Stdin
	} else {
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer file.Close()
		reader = file
	}
	content, err := io.ReadAll(io.LimitReader(reader, maxReminderFileBytes+1))
	if err != nil {
		return "", err
	}
	if len(content) > maxReminderFileBytes {
		return "", fmt.Errorf("%s is larger than 64 KB", path)
	}
	return string(content), nil
}

type reminderView struct {
	ID         string                `json:"id"`
	Title      string                `json:"title"`
	Status     string                `json:"status"`
	Revision   int64                 `json:"revision"`
	Schedule   *state.Schedule       `json:"schedule"`
	Recurrence *state.RecurrenceRule `json:"recurrence"`
}

func writeStoredReminder(stdout io.Writer, stored statectl.StoredReminder, asJSON bool) error {
	if asJSON {
		return writeIndentedJSON(stdout, stored.Raw)
	}
	_, err := fmt.Fprintf(stdout, "stored reminder %s %q%s\n",
		stored.ID,
		stored.Title,
		summarizeSchedule(stored.Schedule, storedRecurrence(stored)),
	)
	return err
}

func writeReminderDetail(stdout io.Writer, raw json.RawMessage) error {
	detail := struct {
		Reminder reminderView `json:"reminder"`
		Comments []struct {
			Body string `json:"body"`
		} `json:"comments"`
	}{}
	if err := json.Unmarshal(raw, &detail); err != nil {
		return fmt.Errorf("decode reminder detail: %w", err)
	}
	comments := "comments"
	if len(detail.Comments) == 1 {
		comments = "comment"
	}
	_, err := fmt.Fprintf(stdout, "reminder %s %q%s, %s, %d %s\n",
		detail.Reminder.ID,
		detail.Reminder.Title,
		summarizeSchedule(detail.Reminder.Schedule, detail.Reminder.Recurrence),
		detail.Reminder.Status,
		len(detail.Comments),
		comments,
	)
	return err
}

func writeSearchResults(stdout io.Writer, raw json.RawMessage, query string) error {
	decoded := struct {
		Reminders []reminderView `json:"reminders"`
	}{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("decode search result: %w", err)
	}
	if len(decoded.Reminders) == 0 {
		_, err := fmt.Fprintf(stdout, "no reminders matched %q\n", query)
		return err
	}
	for _, reminder := range decoded.Reminders {
		if _, err := fmt.Fprintf(stdout, "%s %q%s\n", reminder.ID, reminder.Title, summarizeSchedule(reminder.Schedule, reminder.Recurrence)); err != nil {
			return err
		}
	}
	return nil
}

func storedRecurrence(stored statectl.StoredReminder) *state.RecurrenceRule {
	if len(stored.Raw) == 0 {
		return nil
	}
	decoded := struct {
		Recurrence *state.RecurrenceRule `json:"recurrence"`
	}{}
	if err := json.Unmarshal(stored.Raw, &decoded); err != nil {
		return nil
	}
	return decoded.Recurrence
}

// summarizeSchedule renders " (2026-10-01 09:00 Europe/Berlin, monthly)" or an
// empty string when the reminder has no schedule.
func summarizeSchedule(schedule *state.Schedule, recurrence *state.RecurrenceRule) string {
	if schedule == nil {
		return ""
	}
	when := schedule.LocalDate
	if schedule.LocalTime != "" {
		when += " " + schedule.LocalTime
	}
	if schedule.TimeZone != "" {
		when += " " + schedule.TimeZone
	}
	if recurrence != nil && recurrence.Frequency != "" {
		when += ", " + string(recurrence.Frequency)
	}
	return " (" + when + ")"
}

func writeIndentedJSON(stdout io.Writer, raw json.RawMessage) error {
	var buffer bytes.Buffer
	if err := json.Indent(&buffer, raw, "", "  "); err != nil {
		return err
	}
	buffer.WriteByte('\n')
	_, err := stdout.Write(buffer.Bytes())
	return err
}
