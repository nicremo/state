package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nicremo/state/internal/statectl"
)

// maxAttachBytes matches what the server accepts for one attachment over MCP.
const maxAttachBytes = 8 << 20

var attachMimeTypes = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".heic": "image/heic",
	".m4a":  "audio/m4a",
	".mp4":  "audio/mp4",
	".aac":  "audio/aac",
	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
}

func runNoteAttach(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl note attach", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profileName := flags.String("profile", "", "statectl profile name")
	configPath := flags.String("config", defaultConfigPath(), "statectl config path")
	noteID := flags.String("id", "", "note UUIDv7")
	file := flags.String("file", "", "photo or recording the owner named: jpg, png, heic, m4a, mp4, aac, mp3, wav")
	kind := flags.String("kind", "", "image or audio; must match the file")
	ordinal := flags.Int("ordinal", 0, "position among the note's attachments")
	sourceText := flags.String("source-text", "", "original wording that caused the upload")
	requestID := flags.String("request-id", "", "stable UUID for idempotent retries")
	asJSON := flags.Bool("json", false, "print the stored attachment as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireReminderProfile(*profileName); err != nil {
		return err
	}
	if strings.TrimSpace(*noteID) == "" || strings.TrimSpace(*file) == "" {
		return errors.New("statectl note attach requires --id and --file")
	}
	if strings.TrimSpace(*sourceText) == "" {
		return errors.New("statectl note attach requires --source-text")
	}
	mimeType, content, err := readAttachment(*file, *kind)
	if err != nil {
		return err
	}
	return withNoteService(*configPath, *profileName, func(ctx context.Context, service *statectl.NoteService) error {
		raw, err := service.Attach(ctx, statectl.AttachOptions{NoteID: *noteID, MimeType: mimeType, Content: content, Ordinal: *ordinal, SourceText: *sourceText, RequestID: *requestID})
		if err != nil {
			return err
		}
		if *asJSON {
			return writeIndentedJSON(stdout, raw)
		}
		result := struct {
			Attachment struct {
				ID       string `json:"id"`
				ByteSize int64  `json:"byte_size"`
			} `json:"attachment"`
		}{}
		if err := json.Unmarshal(raw, &result); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "stored attachment %s (%d bytes); run statectl note process --id %s\n", result.Attachment.ID, result.Attachment.ByteSize, *noteID)
		return err
	})
}

// readAttachment reads exactly the file the owner named: a regular file of
// a known type within the size limit, never a directory.
func readAttachment(path string, kind string) (string, []byte, error) {
	mimeType, known := attachMimeTypes[strings.ToLower(filepath.Ext(path))]
	if !known {
		return "", nil, errors.New("unsupported file type; use jpg, png, heic, m4a, mp4, aac, mp3 or wav")
	}
	if kind != "" && !strings.HasPrefix(mimeType, kind+"/") {
		return "", nil, fmt.Errorf("--kind %s does not match %s", kind, filepath.Base(path))
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, errors.New("--file must be a regular file")
	}
	if info.Size() > maxAttachBytes {
		return "", nil, fmt.Errorf("file is larger than %d MB", maxAttachBytes>>20)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxAttachBytes+1))
	if err != nil {
		return "", nil, err
	}
	if len(content) > maxAttachBytes {
		return "", nil, fmt.Errorf("file is larger than %d MB", maxAttachBytes>>20)
	}
	return mimeType, content, nil
}

func runNoteProcess(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl note process", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profileName := flags.String("profile", "", "statectl profile name")
	configPath := flags.String("config", defaultConfigPath(), "statectl config path")
	noteID := flags.String("id", "", "note UUIDv7")
	requestID := flags.String("request-id", "", "stable UUID for idempotent retries")
	asJSON := flags.Bool("json", false, "print the processing state as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireReminderProfile(*profileName); err != nil {
		return err
	}
	if strings.TrimSpace(*noteID) == "" {
		return errors.New("statectl note process requires --id")
	}
	return withNoteService(*configPath, *profileName, func(ctx context.Context, service *statectl.NoteService) error {
		raw, err := service.Process(ctx, *noteID, *requestID)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeIndentedJSON(stdout, raw)
		}
		return writeProcessing(stdout, raw)
	})
}

func runNoteProcessing(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl note processing", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profileName := flags.String("profile", "", "statectl profile name")
	configPath := flags.String("config", defaultConfigPath(), "statectl config path")
	noteID := flags.String("id", "", "note UUIDv7")
	asJSON := flags.Bool("json", false, "print status, AI result and attachments as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireReminderProfile(*profileName); err != nil {
		return err
	}
	if strings.TrimSpace(*noteID) == "" {
		return errors.New("statectl note processing requires --id")
	}
	return withNoteService(*configPath, *profileName, func(ctx context.Context, service *statectl.NoteService) error {
		raw, err := service.Processing(ctx, *noteID)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeIndentedJSON(stdout, raw)
		}
		return writeProcessing(stdout, raw)
	})
}

func runNoteRelated(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl note related", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profileName := flags.String("profile", "", "statectl profile name")
	configPath := flags.String("config", defaultConfigPath(), "statectl config path")
	noteID := flags.String("id", "", "note UUIDv7")
	asJSON := flags.Bool("json", false, "print related notes as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireReminderProfile(*profileName); err != nil {
		return err
	}
	if strings.TrimSpace(*noteID) == "" {
		return errors.New("statectl note related requires --id")
	}
	return withNoteService(*configPath, *profileName, func(ctx context.Context, service *statectl.NoteService) error {
		raw, err := service.Related(ctx, *noteID)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeIndentedJSON(stdout, raw)
		}
		related := struct {
			Relations []struct {
				RelatedNoteID string `json:"related_note_id"`
				RelatedTitle  string `json:"related_title"`
				Reason        string `json:"reason"`
			} `json:"relations"`
		}{}
		if err := json.Unmarshal(raw, &related); err != nil {
			return fmt.Errorf("decode related notes: %w", err)
		}
		if len(related.Relations) == 0 {
			_, err := fmt.Fprintln(stdout, "no related notes")
			return err
		}
		for _, relation := range related.Relations {
			if _, err := fmt.Fprintf(stdout, "%s %q  %s\n", relation.RelatedNoteID, terminalSafe(relation.RelatedTitle), terminalSafe(relation.Reason)); err != nil {
				return err
			}
		}
		return nil
	})
}

func writeProcessing(stdout io.Writer, raw json.RawMessage) error {
	detail := struct {
		Processing struct {
			Status string `json:"status"`
			Error  string `json:"error"`
			Model  string `json:"model"`
		} `json:"processing"`
		AI *struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
			Model   string `json:"model"`
		} `json:"ai"`
		Attachments []struct {
			ID          string            `json:"id"`
			Kind        string            `json:"kind"`
			DerivedKind string            `json:"derived_kind"`
			DerivedText string            `json:"derived_text"`
			RawText     string            `json:"raw_text"`
			Segments    []json.RawMessage `json:"segments"`
		} `json:"attachments"`
	}{}
	if err := json.Unmarshal(raw, &detail); err != nil {
		return fmt.Errorf("decode processing: %w", err)
	}
	line := "processing " + detail.Processing.Status
	if detail.Processing.Error != "" {
		line += ": " + terminalSafe(detail.Processing.Error)
	}
	if _, err := fmt.Fprintln(stdout, line); err != nil {
		return err
	}
	if detail.AI != nil {
		if _, err := fmt.Fprintf(stdout, "AI title %q, summary %q (%s)\n", terminalSafe(detail.AI.Title), terminalSafe(detail.AI.Summary), terminalSafe(detail.AI.Model)); err != nil {
			return err
		}
	}
	for _, attachment := range detail.Attachments {
		text := attachment.DerivedText
		if text == "" {
			text = "(no text yet)"
		}
		label := attachment.DerivedKind
		if len(attachment.Segments) > 0 {
			label = fmt.Sprintf("%s (%d segments)", label, len(attachment.Segments))
		}
		if _, err := fmt.Fprintf(stdout, "%s %s %s: %s\n", attachment.ID, attachment.Kind, label, terminalSafe(text)); err != nil {
			return err
		}
		if attachment.RawText != "" && attachment.RawText != attachment.DerivedText {
			if _, err := fmt.Fprintf(stdout, "  raw: %s\n", terminalSafe(attachment.RawText)); err != nil {
				return err
			}
		}
	}
	return nil
}

func runNoteDictionary(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl note dictionary", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profileName := flags.String("profile", "", "statectl profile name")
	configPath := flags.String("config", defaultConfigPath(), "statectl config path")
	asJSON := flags.Bool("json", false, "print the dictionary as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireReminderProfile(*profileName); err != nil {
		return err
	}
	return withNoteService(*configPath, *profileName, func(ctx context.Context, service *statectl.NoteService) error {
		raw, err := service.Dictionary(ctx)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeIndentedJSON(stdout, raw)
		}
		return writeDictionary(stdout, raw)
	})
}

// writeDictionary prints one entry per line in the same format the app
// imports: "word X", "always A -> B", "context A -> B".
func writeDictionary(stdout io.Writer, raw json.RawMessage) error {
	dictionary := struct {
		Words       []string `json:"words"`
		Corrections []struct {
			From string `json:"from"`
			To   string `json:"to"`
			Mode string `json:"mode"`
		} `json:"corrections"`
		Revision int64 `json:"revision"`
	}{}
	if err := json.Unmarshal(raw, &dictionary); err != nil {
		return fmt.Errorf("decode dictionary: %w", err)
	}
	if _, err := fmt.Fprintf(stdout, "dictionary revision %d: %d words, %d corrections\n", dictionary.Revision, len(dictionary.Words), len(dictionary.Corrections)); err != nil {
		return err
	}
	for _, word := range dictionary.Words {
		if _, err := fmt.Fprintf(stdout, "word %s\n", terminalSafe(word)); err != nil {
			return err
		}
	}
	for _, correction := range dictionary.Corrections {
		if _, err := fmt.Fprintf(stdout, "%s %s -> %s\n", terminalSafe(correction.Mode), terminalSafe(correction.From), terminalSafe(correction.To)); err != nil {
			return err
		}
	}
	return nil
}
