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
		{[]string{"note", "create", "--profile", "codex", "--source-text", "x"}, "requires --title or --document-file"},
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
