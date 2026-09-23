# CLI-00: Befehlsgerüst, Profil aus der Umgebung, Paritäts-Tests

**Welle:** 1 (allein, vor allen anderen) · **Slug:** `foundation` · **Branch:** `cli/00-foundation`
**Besitzt:** `cmd/statectl/main.go` (nur Dispatch und Usage), `cmd/statectl/reminder.go` (nur der `switch` in `runReminder` und die Profil-Auflösung), neu `cmd/statectl/briefing.go`, neu `cmd/statectl/reminder_edit.go`, neu `cmd/statectl/run.go`, neu `cmd/statectl/parity_test.go`, neu `internal/statectl/coverage.go`, neu `internal/statectl/coverage_test.go`
**Umfang:** klein bis mittel (Go)

## Goal-Prompt

> Lies `docs/plans/2026-09-23-cli-parity/README.md` vollständig und halte dich strikt an das Arbeitsprotokoll in Abschnitt 5. Setze danach `docs/plans/2026-09-23-cli-parity/CLI-00-foundation.md` Task für Task um.

## Ziel

1. Jeder Befehl aus Abschnitt 3 des Master-Plans ist erreichbar, parst seine vollständigen Flags und zeigt `--help`. Noch nicht umgesetzte Befehle geben nach dem Parsen den Fehler `errNotImplemented` zurück. Diese Rümpfe ersetzen die Pakete CLI-01 bis CLI-03.
2. `--profile` fällt auf die Umgebungsvariable `STATECTL_PROFILE` zurück.
3. Zwei Tests erzwingen die Parität.

## Task 1: Abbildung Werkzeug zu Befehl

**Dateien:** `internal/statectl/coverage.go`, `internal/statectl/coverage_test.go`

```go
package statectl

// ToolCommands maps every tool the State MCP server offers to the statectl
// command that calls it. The parity tests fail when the server gains a tool
// without a command here, so the terminal never falls behind MCP.
var ToolCommands = map[string]string{
	"get_briefing":           "briefing",
	"get_changes":            "changes",
	"search_reminders":       "reminder search",
	"get_reminder":           "reminder show",
	"create_reminder":        "reminder create",
	"update_reminder":        "reminder update",
	"add_comment":            "reminder add-context",
	"complete_occurrence":    "reminder complete",
	"snooze_occurrence":      "reminder snooze",
	"get_execution_context":  "run context",
	"claim_agent_run":        "run claim",
	"report_agent_run_event": "run event",
	"complete_agent_run":     "run complete",
	"request_agent_approval": "run approval",
}
```

Test zuerst, in `coverage_test.go` (Paket `statectl`, nutzt den vorhandenen Helfer `newReminderTestSession(t)` aus `reminder_test.go`):

```go
func TestEveryMCPToolHasACLICommand(t *testing.T) {
	session := newReminderTestSession(t)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("the test server lists no tools")
	}
	for _, tool := range tools.Tools {
		if _, ok := ToolCommands[tool.Name]; !ok {
			t.Errorf("MCP tool %q has no statectl command; add it to ToolCommands and implement it", tool.Name)
		}
	}
	for name := range ToolCommands {
		found := false
		for _, tool := range tools.Tools {
			if tool.Name == name {
				found = true
			}
		}
		if !found {
			t.Errorf("ToolCommands lists %q, which the server no longer offers", name)
		}
	}
}
```

Wichtig: Der Test zeigt, dass jedes **Werkzeug** einen Befehl hat. Dass der Befehl auch **existiert**, prüft Task 3.

`go test ./internal/statectl/ -run TestEveryMCPToolHasACLICommand` muss grün sein (die Abbildung ist vollständig, sobald sie angelegt ist).

Commit: `git commit -m "test: map every state mcp tool to a statectl command"`

## Task 2: Profil aus der Umgebung

**Datei:** `cmd/statectl/reminder.go` (nur die Stelle, an der `--profile` geprüft wird)

Lies `requireReminderProfile` und die Flag-Definitionen. Füge eine Hilfsfunktion hinzu und nutze sie in **allen** Befehlen, die `--profile` haben (auch in den neuen aus Task 3):

```go
// profileFlagDefault lets an agent export STATECTL_PROFILE once instead of
// repeating --profile on every call.
func profileFlagDefault() string {
	return strings.TrimSpace(os.Getenv("STATECTL_PROFILE"))
}
```

Überall, wo heute `flags.String("profile", "", ...)` steht (nur in den `reminder`-Befehlen, **nicht** in `pair`, `mcp`, `doctor` usw.), wird der Default zu `profileFlagDefault()`.

Test in `cmd/statectl/reminder_test.go` (Datei außerhalb deines Bereichs, im Report nennen) oder in `parity_test.go`:

```go
func TestReminderProfileFallsBackToEnvironment(t *testing.T) {
	t.Setenv("STATECTL_PROFILE", "from-env")
	var stdout, stderr bytes.Buffer
	err := run([]string{"reminder", "show", "--id", "x", "--config", filepath.Join(t.TempDir(), "none.json")}, &stdout, &stderr, discardLogger())
	if err == nil || strings.Contains(err.Error(), "profile is required") {
		t.Fatalf("expected a lookup of profile from-env, got %v", err)
	}
}
```

Erwartet ist ein Fehler wie `statectl profile not found`, also ein Beweis, dass `from-env` benutzt wurde. Prüfe den genauen Fehlertext von `LoadProfile` und passe die Bedingung an. Gibt es `discardLogger()` nicht, lege einen minimalen Helfer an: `slog.New(slog.NewTextHandler(io.Discard, nil))`.

Commit: `git commit -m "feat: read the statectl profile from STATECTL_PROFILE"`

## Task 3: Befehlsgerüst

**Dateien:** `cmd/statectl/main.go`, `cmd/statectl/reminder.go` (nur `switch`), neu `briefing.go`, `reminder_edit.go`, `run.go`

1. In `main.go` im `switch args[0]`: `case "briefing": return runBriefing(args[1:], stdout, stderr)`, `case "changes": return runChanges(...)`, `case "run": return runRun(args[1:], stdout, stderr)`. Usage-Text in der ersten Fehlermeldung um `briefing|changes|run` erweitern.
2. In `runReminder`: Fälle `update`, `complete`, `snooze` auf `runReminderUpdate`, `runReminderComplete`, `runReminderSnooze`. Usage-Text anpassen.
3. Neue Datei `cmd/statectl/briefing.go`, Beispiel für den Aufbau jedes Rumpfs:

```go
package main

import (
	"errors"
	"flag"
	"io"
)

// errNotImplemented marks a command whose flags are final but whose call is
// still being built. The parity work packages replace every use of it.
var errNotImplemented = errors.New("this statectl command is not implemented yet")

func runBriefing(args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("statectl briefing", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.String("profile", profileFlagDefault(), "statectl profile name (default $STATECTL_PROFILE)")
	flags.String("config", defaultConfigPath(), "statectl config path")
	flags.Int64("after", 0, "return changes after this cursor; 0 for the first briefing")
	flags.Int("limit", 20, "maximum reminders and changes, at most 50")
	flags.Bool("json", false, "print the raw server response")
	if err := flags.Parse(args); err != nil {
		return err
	}
	return errNotImplemented
}
```

Genauso, mit den Flags **exakt** aus Abschnitt 3 des Master-Plans:
- `runChanges` (`--after`, `--limit` Default 50, max 100, `--json`)
- in `reminder_edit.go`: `runReminderUpdate` (`--id`, `--source-text`, `--title`, `--description`, `--description-file`, `--clear-description`, `--request-id`, `--json`), `runReminderComplete` (`--id`, `--source-text`, `--occurrence`, `--request-id`, `--json`), `runReminderSnooze` (`--id`, `--source-text`, `--for`, `--until`, `--occurrence`, `--request-id`, `--json`)
- in `run.go`: `runRun` verteilt auf `context`, `claim`, `event`, `complete`, `approval`, unbekannt oder leer ergibt `usage: statectl run <context|claim|event|complete|approval>`. Gemeinsame Flags jedes `run`-Befehls: `--runner-config` (Default: leer, bedeutet Standardpfad des Runners), `--profile` (Default leer, **nicht** `STATECTL_PROFILE`, weil Runner-Arbeit eine Runner-Identität braucht), `--json`. Dazu je Befehl: `context --run`; `claim --wait` (Default 25, max 25); `event --run --event --detail`; `complete --run --outcome --exit-code --summary --artifact --failure-code --request-id`; `approval --run --capability --reason`.

Alle Rümpfe parsen nur und geben `errNotImplemented` zurück.

## Task 4: Routing-Test

**Datei:** `cmd/statectl/parity_test.go`

```go
func TestEveryMappedCommandIsRoutable(t *testing.T) {
	for tool, command := range statectl.ToolCommands {
		args := append(strings.Fields(command), "--help")
		var stdout, stderr bytes.Buffer
		err := run(args, &stdout, &stderr, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if !errors.Is(err, flag.ErrHelp) {
			t.Errorf("%s: statectl %s --help returned %v, want flag.ErrHelp", tool, command, err)
		}
	}
}
```

Prüfe, dass die bestehenden Befehle (`reminder create`, `search`, `show`, `add-context`) bei `--help` ebenfalls `flag.ErrHelp` durchreichen. Fängt `run` den Fehler irgendwo ab, korrigiere das minimal.

`GOTOOLCHAIN=local go test -race ./cmd/statectl/ ./internal/statectl/` grün.

Commit: `git commit -m "feat: route every statectl parity command and prove it with a test"`

## Task 5: Abschluss

Pflichtprüfung aus dem Master-Plan, Report `reports/CLI-00-report.md`, Draft-PR. Im Report die Liste aller Rümpfe mit Datei und Funktionsname, damit die Pakete der Welle 2 sie sofort finden.
