package main

import (
	"bytes"
	"io"
	"log/slog"
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
