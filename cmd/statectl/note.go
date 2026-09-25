package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/nicremo/state/internal/state"
	"github.com/nicremo/state/internal/statectl"
)

const noteUsage = "usage: statectl note <list|show|create|update|attach|process|processing|related|dictionary>"

// maxNoteDocumentBytes is the server's limit, not the smaller reminder one.
const maxNoteDocumentBytes = state.MaxNoteDocumentBytes

func runNote(args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New(noteUsage)
	}
	switch args[0] {
	case "list":
		return runNoteList(args[1:], stdout, stderr)
	case "show":
		return runNoteShow(args[1:], stdout, stderr)
	case "create":
		return runNoteCreate(args[1:], stdout, stderr)
	case "update":
		return runNoteUpdate(args[1:], stdout, stderr)
	case "attach":
		return runNoteAttach(args[1:], stdout, stderr)
	case "process":
		return runNoteProcess(args[1:], stdout, stderr)
	case "processing":
		return runNoteProcessing(args[1:], stdout, stderr)
	case "related":
		return runNoteRelated(args[1:], stdout, stderr)
	case "dictionary":
		return runNoteDictionary(args[1:], stdout, stderr)
	default:
		return errors.New(noteUsage)
	}
}

func runNoteList(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl note list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profileName := flags.String("profile", "", "statectl profile name")
	configPath := flags.String("config", defaultConfigPath(), "statectl config path")
	query := flags.String("query", "", "words to find; empty lists the most recently changed notes")
	limit := flags.Int("limit", 20, "maximum notes, at most 50")
	asJSON := flags.Bool("json", false, "print the raw result as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireReminderProfile(*profileName); err != nil {
		return err
	}
	return withNoteService(*configPath, *profileName, func(ctx context.Context, service *statectl.NoteService) error {
		raw, err := service.List(ctx, *query, *limit)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeIndentedJSON(stdout, raw)
		}
		return writeNoteList(stdout, raw)
	})
}

func runNoteShow(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl note show", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profileName := flags.String("profile", "", "statectl profile name")
	configPath := flags.String("config", defaultConfigPath(), "statectl config path")
	noteID := flags.String("id", "", "note UUIDv7")
	asJSON := flags.Bool("json", false, "print the note and its history as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireReminderProfile(*profileName); err != nil {
		return err
	}
	if strings.TrimSpace(*noteID) == "" {
		return errors.New("statectl note show requires --id")
	}
	return withNoteService(*configPath, *profileName, func(ctx context.Context, service *statectl.NoteService) error {
		raw, err := service.Show(ctx, *noteID)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeIndentedJSON(stdout, raw)
		}
		return writeNoteDetail(stdout, raw)
	})
}

func runNoteCreate(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl note create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profileName := flags.String("profile", "", "statectl profile name")
	configPath := flags.String("config", defaultConfigPath(), "statectl config path")
	title := flags.String("title", "", "note title, defaults to the first line of the document")
	documentFile := flags.String("document-file", "", "read the Markdown document from a file, - for stdin")
	capture := flags.String("capture", "", "image or audio: an empty photo or voice note for statectl note attach")
	sourceText := flags.String("source-text", "", "original wording that caused the note")
	requestID := flags.String("request-id", "", "stable UUID for idempotent retries")
	asJSON := flags.Bool("json", false, "print the stored note as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireReminderProfile(*profileName); err != nil {
		return err
	}
	if strings.TrimSpace(*sourceText) == "" {
		return errors.New("statectl note create requires --source-text")
	}
	document := ""
	if *documentFile != "" {
		text, err := readTextFile(*documentFile, maxNoteDocumentBytes)
		if err != nil {
			return err
		}
		document = text
	}
	if *capture == "" && strings.TrimSpace(*title) == "" && strings.TrimSpace(document) == "" {
		return errors.New("statectl note create requires --title, --document-file or --capture")
	}
	return withNoteService(*configPath, *profileName, func(ctx context.Context, service *statectl.NoteService) error {
		stored, raw, err := service.Create(ctx, *title, document, *capture, *sourceText, *requestID)
		if err != nil {
			return err
		}
		return writeStoredNote(stdout, stored, raw, *asJSON)
	})
}

func runNoteUpdate(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl note update", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profileName := flags.String("profile", "", "statectl profile name")
	configPath := flags.String("config", defaultConfigPath(), "statectl config path")
	noteID := flags.String("id", "", "note UUIDv7")
	sourceText := flags.String("source-text", "", "original wording that caused the change")
	documentFile := flags.String("document-file", "", "replace the document from a file, - for stdin")
	requestID := flags.String("request-id", "", "stable UUID for idempotent retries")
	asJSON := flags.Bool("json", false, "print the stored note as JSON")
	var title, summary optionalString
	flags.Var(&title, "title", "replacement title; an empty value returns to the derived title")
	flags.Var(&summary, "summary", "replacement summary; an empty value returns to the derived summary")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireReminderProfile(*profileName); err != nil {
		return err
	}
	if strings.TrimSpace(*noteID) == "" {
		return errors.New("statectl note update requires --id")
	}
	if strings.TrimSpace(*sourceText) == "" {
		return errors.New("statectl note update requires --source-text")
	}
	options := statectl.UpdateNoteOptions{NoteID: *noteID, SourceText: *sourceText, RequestID: *requestID, Title: title.value, Summary: summary.value}
	if *documentFile != "" {
		text, err := readTextFile(*documentFile, maxNoteDocumentBytes)
		if err != nil {
			return err
		}
		options.Document = &text
	}
	if options.Title == nil && options.Summary == nil && options.Document == nil {
		return errors.New("statectl note update requires --title, --summary or --document-file")
	}
	return withNoteService(*configPath, *profileName, func(ctx context.Context, service *statectl.NoteService) error {
		stored, raw, err := service.Update(ctx, options)
		if err != nil {
			return err
		}
		return writeStoredNote(stdout, stored, raw, *asJSON)
	})
}

// optionalString tells "not given" apart from "given as empty", which clears
// a written title or summary back to the derived one.
type optionalString struct{ value *string }

func (option *optionalString) String() string {
	if option.value == nil {
		return ""
	}
	return *option.value
}

func (option *optionalString) Set(value string) error {
	option.value = &value
	return nil
}

func withNoteService(configPath string, profileName string, action func(context.Context, *statectl.NoteService) error) error {
	profile, token, err := loadProfileAndCredential(configPath, profileName)
	if err != nil {
		return err
	}
	session, err := statectl.ConnectRemote(context.Background(), profile, token, version)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), reminderCallTimeout)
	defer cancel()
	return action(ctx, statectl.NewNoteService(session, newUUIDv7))
}

func writeStoredNote(stdout io.Writer, stored statectl.StoredNote, raw json.RawMessage, asJSON bool) error {
	if asJSON {
		return writeIndentedJSON(stdout, raw)
	}
	_, err := fmt.Fprintf(stdout, "stored note %s %q\n", stored.ID, terminalSafe(stored.Title))
	return err
}

func writeNoteList(stdout io.Writer, raw json.RawMessage) error {
	list := struct {
		Notes []statectl.StoredNote `json:"notes"`
	}{}
	if err := json.Unmarshal(raw, &list); err != nil {
		return fmt.Errorf("decode note list: %w", err)
	}
	if len(list.Notes) == 0 {
		_, err := fmt.Fprintln(stdout, "no notes")
		return err
	}
	for _, note := range list.Notes {
		line := fmt.Sprintf("%s %q", note.ID, terminalSafe(note.Title))
		if note.Summary != "" {
			line += "  " + terminalSafe(note.Summary)
		}
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func writeNoteDetail(stdout io.Writer, raw json.RawMessage) error {
	detail := struct {
		Note struct {
			statectl.StoredNote
			TitleSource string `json:"title_source"`
			Document    string `json:"document"`
			Processing  struct {
				Status string `json:"status"`
			} `json:"processing"`
			Attachments []struct {
				Kind        string `json:"kind"`
				DerivedText string `json:"derived_text"`
			} `json:"attachments"`
			Relations []struct {
				RelatedTitle string `json:"related_title"`
				Reason       string `json:"reason"`
			} `json:"relations"`
			Proposals []struct {
				Title     string `json:"title"`
				LocalDate string `json:"local_date"`
				Status    string `json:"status"`
			} `json:"reminder_proposals"`
		} `json:"note"`
		History []json.RawMessage `json:"history"`
	}{}
	if err := json.Unmarshal(raw, &detail); err != nil {
		return fmt.Errorf("decode note: %w", err)
	}
	note := detail.Note
	var builder strings.Builder
	fmt.Fprintf(&builder, "note %s %q, revision %d, %d events\n", note.ID, terminalSafe(note.Title), note.Revision, len(detail.History))
	if note.TitleSource == "ai" {
		builder.WriteString("title and summary by the notes AI\n")
	}
	if note.Processing.Status != "" && note.Processing.Status != "idle" {
		fmt.Fprintf(&builder, "processing %s\n", note.Processing.Status)
	}
	fmt.Fprintf(&builder, "\n%s\n", terminalSafe(strings.TrimRight(note.Document, "\n")))
	for index, attachment := range note.Attachments {
		fmt.Fprintf(&builder, "\n[%s %d] %s\n", attachment.Kind, index+1, terminalSafe(attachment.DerivedText))
	}
	for _, relation := range note.Relations {
		fmt.Fprintf(&builder, "related: %q (%s)\n", terminalSafe(relation.RelatedTitle), terminalSafe(relation.Reason))
	}
	for _, proposal := range note.Proposals {
		fmt.Fprintf(&builder, "reminder proposal (%s): %q %s\n", proposal.Status, terminalSafe(proposal.Title), proposal.LocalDate)
	}
	_, err := io.WriteString(stdout, builder.String())
	return err
}

// terminalSafe removes control characters except line breaks and tabs, so a
// note written by an agent cannot drive the terminal with escape sequences.
func terminalSafe(text string) string {
	return strings.Map(func(character rune) rune {
		if character == '\n' || character == '\t' {
			return character
		}
		if character < 0x20 || character == 0x7f || (character >= 0x80 && character < 0xa0) {
			return -1
		}
		return character
	}, text)
}
