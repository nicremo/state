package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestNoteCommandsValidateBeforeConnecting(t *testing.T) {
	t.Parallel()

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"note"}, "usage: statectl note"},
		{[]string{"note", "list"}, "profile is required"},
		{[]string{"note", "show", "--profile", "codex"}, "requires --id"},
		{[]string{"note", "create", "--profile", "codex", "--title", "x"}, "requires --source-text"},
		{[]string{"note", "create", "--profile", "codex", "--source-text", "x"}, "requires --title, --document-file or --capture"},
		{[]string{"note", "update", "--profile", "codex", "--id", "n", "--source-text", "x"}, "requires --title, --summary or --document-file"},
	}
	for _, testCase := range cases {
		var stdout, stderr bytes.Buffer
		err := run(testCase.args, &stdout, &stderr, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("run(%v) error = %v, want %q", testCase.args, err, testCase.want)
		}
	}
}

func TestNoteDocumentsMayExceedTheReminderFileLimit(t *testing.T) {
	t.Parallel()

	path := t.TempDir() + "/note.md"
	content := strings.Repeat("a", 200*1024)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	text, err := readTextFile(path, maxNoteDocumentBytes)
	if err != nil || len(text) != len(content) {
		t.Fatalf("readTextFile() = %d bytes, %v", len(text), err)
	}
	if _, err := readTextFile(path, maxReminderFileBytes); err == nil {
		t.Fatal("the reminder limit no longer applies to reminder files")
	}
}

func TestTerminalSafeDropsEscapeSequences(t *testing.T) {
	t.Parallel()

	if got := terminalSafe("Titel\x1b[31m rot\nzweite\tZeile\x07"); got != "Titel[31m rot\nzweite\tZeile" {
		t.Fatalf("terminalSafe() = %q", got)
	}
}

func TestNoteAICommandsValidateBeforeConnecting(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	large := directory + "/large.jpg"
	if err := os.WriteFile(large, bytes.Repeat([]byte("x"), maxAttachBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	photo := directory + "/seite.jpg"
	if err := os.WriteFile(photo, []byte{0xFF, 0xD8, 0xFF}, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"note", "attach", "--profile", "codex", "--id", "n"}, "requires --id and --file"},
		{[]string{"note", "attach", "--profile", "codex", "--id", "n", "--file", photo}, "requires --source-text"},
		{[]string{"note", "attach", "--profile", "codex", "--id", "n", "--file", directory + "/notes.pdf", "--source-text", "x"}, "unsupported file type"},
		{[]string{"note", "attach", "--profile", "codex", "--id", "n", "--file", photo, "--kind", "audio", "--source-text", "x"}, "does not match"},
		{[]string{"note", "attach", "--profile", "codex", "--id", "n", "--file", large, "--source-text", "x"}, "larger than 8 MB"},
		{[]string{"note", "attach", "--profile", "codex", "--id", "n", "--file", directory, "--source-text", "x"}, "unsupported file type"},
		{[]string{"note", "process", "--profile", "codex"}, "requires --id"},
		{[]string{"note", "processing", "--profile", "codex"}, "requires --id"},
		{[]string{"note", "related", "--profile", "codex"}, "requires --id"},
	}
	for _, testCase := range cases {
		var stdout, stderr bytes.Buffer
		err := run(testCase.args, &stdout, &stderr, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("run(%v) error = %v, want %q", testCase.args, err, testCase.want)
		}
	}
}

func TestProcessingOutputIsReadableAndSafe(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"processing":{"status":"ready","model":"deepseek/deepseek-v4.1-flash"},"ai":{"title":"Seite\u001b[31m","summary":"S","model":"deepseek/deepseek-v4.1-flash"},"attachments":[{"id":"a1","kind":"audio","derived_kind":"transcript","derived_text":"Milch kaufen"}]}`)
	var stdout bytes.Buffer
	if err := writeProcessing(&stdout, raw); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if !strings.Contains(output, "processing ready") || !strings.Contains(output, "a1 audio transcript: Milch kaufen") || strings.Contains(output, "\x1b") {
		t.Fatalf("output = %q", output)
	}
}

func TestProcessingOutputCountsSegments(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"processing":{"status":"ready"},"attachments":[{"id":"a1","kind":"audio","derived_kind":"transcript","derived_text":"CLAUDE.md angepasst","raw_text":"Cloud.md angepasst","segments":[{"start_ms":0,"end_ms":1200,"text":"CLAUDE.md angepasst"}]}]}`)
	var stdout bytes.Buffer
	if err := writeProcessing(&stdout, raw); err != nil {
		t.Fatal(err)
	}
	if output := stdout.String(); !strings.Contains(output, "a1 audio transcript (1 segments): CLAUDE.md angepasst") || !strings.Contains(output, "raw: Cloud.md angepasst") {
		t.Fatalf("output = %q", output)
	}
}

func TestDictionaryOutputIsReadableAndSafe(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"words":["Supabase","Wispr\u001b[31m Flow"],"corrections":[{"from":"ZEVDISK","to":"sevDesk","mode":"always"},{"from":"Note","to":"Node","mode":"context"}],"revision":3}`)
	var stdout bytes.Buffer
	if err := writeDictionary(&stdout, raw); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	for _, wanted := range []string{"revision 3", "word Supabase", "always ZEVDISK -> sevDesk", "context Note -> Node"} {
		if !strings.Contains(output, wanted) {
			t.Fatalf("output lacks %q: %q", wanted, output)
		}
	}
	if strings.Contains(output, "\x1b") {
		t.Fatalf("control character in output: %q", output)
	}
}
