# WP05: Capture-Regeln für Agenten und manuelle Harness-Integration

**Welle:** 1 · **Slug:** `agent-capture-rules` · **Branch:** `wp/05-agent-capture-rules`
**Besitzt:** `internal/statectl/rules.go`, neu `internal/statectl/rules_test.go` (falls nicht vorhanden), `internal/statectl/installer.go`, `internal/statectl/installer_test.go`, `internal/mcpserver/server.go` (**nur** `Description`-Texte in `registerTools` und `jsonschema`-Tags der Input-Structs), neu `docs/agent-integration.md`
**Nicht anfassen:** `cmd/statectl/**`, `internal/statectl/reminder*.go` (gehören WP04)
**Geschätzter Umfang:** klein bis mittel (Go-Strings, Tests, Doku)

## Goal-Prompt (so an die Worker-Session geben)

> Lies `docs/plans/2026-09-23-product-lineup/README.md` vollständig und halte dich an das Arbeitsprotokoll in Abschnitt 4. Setze danach `docs/plans/2026-09-23-product-lineup/WP05-agent-capture-rules.md` Task für Task mit TDD um. Du veränderst keine echten Agent-Konfigurationen in deinem Home-Verzeichnis. Schließe mit Report und Draft-PR ab.

## Ziel

Jeder gekoppelte Agent verhält sich nach dem Capture-Protokoll aus `docs/universal-agent-todo-capture.md` Abschnitt 6 und 7: ausdrückliche Aufgaben und wiederkehrende Pflichten werden nach Klärung als State-Reminder gespeichert, Mehrdeutiges wird nachgefragt, Erfolg wird erst nach `stored=true` gemeldet. Die Regeln landen über `statectl pair`/`install` in den Instruktionsdateien von Codex, Claude Code und OpenCode und werden für alle anderen Agenten (DeepSeek Harness, Pi Agent, ...) als kopierbare Anleitung ausgegeben.

## Pflichtlektüre

1. `docs/universal-agent-todo-capture.md` (vollständig, Abschnitte 6, 7, 8 besonders genau)
2. `internal/statectl/rules.go` (aktuelle `DefaultAgentRules`)
3. `internal/statectl/installer.go` und `internal/statectl/installer_test.go`
4. `internal/mcpserver/server.go` (`registerTools`, Input-Structs)
5. `internal/state/harness.go`

## Task 1: Neue Standardregeln

**Dateien:** `internal/statectl/rules.go`, `internal/statectl/rules_test.go`

Ersetze den Rückgabewert von `DefaultAgentRules()` durch exakt diesen Text (Englisch, weil die Agenten englische Regeln am zuverlässigsten befolgen; Formatierung beibehalten):

```text
State is the durable reminder, task and context system for this user. Every paired agent shares it.

Session start
- Call State get_briefing with the last known cursor when available. Keep the returned cursor for the next session.

When to capture
- Capture only explicit intent: "remind me", "I need to", "don't let me forget", "create a task", "schedule", "every month/week/day ...", or a clearly stated obligation with a deadline.
- Do not capture hypotheticals, ideas under discussion, status updates, or things that already happened without a requested follow-up. If unsure, ask once: "Should I add this to State?"

Before writing
- Resolve what changes the meaning: date, local time, time zone, recurrence (unit, interval, day of month), and whether it is a reminder only or should later run as an agent task. Ask one short question when any of these is missing and matters. Never invent a date or recurrence.
- Default time zone and prewarning come from the user's stated preferences or the machine's local zone. Say which defaults you used.

Writing the reminder
- Call create_reminder exactly once per obligation. Title: short and imperative. Description in Markdown with these sections when known: Objective, Why it matters, Project boundary (repository or folder), Procedure and references (paths, URLs, docs), Acceptance criteria, Risk and approvals.
- Put the user's original wording in source_text.
- Use a fresh UUIDv7 client_request_id per new obligation and reuse it for retries of the same write.
- Recurring obligations use recurrence (daily, weekly, monthly, yearly with interval), not several reminders.
- Report success only after State returns stored=true, and name the reminder title and next due date. On any error, say plainly that nothing was saved.

Keeping context current
- Append new decisions, links and blockers with add_comment instead of rewriting the description.
- Before update_reminder, read the reminder and pass its current revision as expected_revision.
- Never delete or archive reminders. Never put secrets, tokens, passwords, private keys or full logs into State.

Without MCP
- If State tools are unavailable, use the terminal: statectl reminder create --profile <profile> --title ... --source-text ... [--date YYYY-MM-DD --time HH:MM --tz Area/City --repeat monthly]. Run statectl reminder --help for all options.

When launched as a task by a State runner
- The task prompt names the State reminder ID. Read context with get_reminder and get_changes. get_execution_context is reserved for the runner itself.
- When the run's execution policy handles completion, State completes the occurrence automatically on verified success. Never complete that occurrence manually.
- Finish with a short result: what changed, how it was verified, what remains open.
```

**Hinweis:** Der Abschnitt "Without MCP" verweist auf den Befehl aus WP04, der parallel entsteht. Das ist gewollt. Der Koordinator mergt beide.

**Tests zuerst** (`rules_test.go`; existiert die Datei nicht, neu anlegen, Paket `statectl`):

```go
func TestDefaultAgentRulesCoverCaptureProtocol(t *testing.T) {
    rules := DefaultAgentRules()
    for _, required := range []string{
        "get_briefing",
        "create_reminder",
        "stored=true",
        "source_text",
        "client_request_id",
        "expected_revision",
        "add_comment",
        "Never invent a date or recurrence",
        "statectl reminder create",
        "get_execution_context is reserved for the runner",
        "Never complete that occurrence manually",
    } {
        if !strings.Contains(rules, required) {
            t.Errorf("rules are missing %q", required)
        }
    }
}

func TestDefaultAgentRulesStayBounded(t *testing.T) {
    if length := len(DefaultAgentRules()); length > 4000 {
        t.Fatalf("rules grew to %d bytes; every agent pays for them on each turn", length)
    }
}

func TestDefaultAgentRulesHaveNoDashPunctuation(t *testing.T) {
    rules := DefaultAgentRules()
    if strings.ContainsAny(rules, "\u2013\u2014") {
        t.Fatal("rules must not contain en or em dashes")
    }
}

func TestUpsertRuleBlockIsIdempotent(t *testing.T) {
    once := UpsertRuleBlock("# Existing\n", DefaultAgentRules())
    twice := UpsertRuleBlock(once, DefaultAgentRules())
    if once != twice {
        t.Fatal("upserting the same rules twice changed the file")
    }
}
```

Ablauf: Tests schreiben → `go test ./internal/statectl/ -run 'Rules|RuleBlock'` → FAIL (außer Idempotenz) → Text ersetzen → grün. Bestehende Installer-Tests, die den alten Regeltext wörtlich erwarten, auf `DefaultAgentRules()` umstellen statt den Text zu duplizieren.

Commit: `git commit -m "feat: teach paired agents the state capture protocol"`

## Task 2: Bessere manuelle Anleitung für unbekannte Agenten

**Dateien:** `internal/statectl/installer.go`, `internal/statectl/installer_test.go`

`ManualInstructions(harness, profile)` gibt heute eine generische JSON-Definition aus. Erweitere sie um harness-spezifische Hinweise für die zwei Agenten, die Fabian nutzt, **ohne** deren Konfigurationsdateien zu schreiben (Architektur-Entscheidung: keine Konfig-Änderung an unbekannten Produkten):

| Harness-Label | Zusätzlicher Hinweisblock |
| --- | --- |
| `pi-agent` oder `pi` | `Pi Agent: add the server to the "mcpServers" object of the profile's MCP configuration and the rules to the profile's AGENTS.md or system prompt file. Each Pi profile needs its own statectl profile.` |
| `deepseek-harness` | `DeepSeek Harness: add the server to the MCP client plugin configuration of each preset that should see State, and the rules to that preset's persona or agent instructions.` |
| alle anderen | kein Zusatzblock |

Füge außerdem immer eine Prüfzeile an: `Verify with: <command> doctor --profile <profile>`.

Tests zuerst:

```go
func TestManualInstructionsForPiMentionPiSpecifics(t *testing.T) {
    installer := NewInstaller(InstallPaths{}, "/usr/local/bin/statectl", nil)
    text := installer.ManualInstructions("pi-agent", "pi-main")
    for _, required := range []string{"Pi Agent:", `"mcp", "--profile", "pi-main"`, "doctor --profile pi-main", DefaultAgentRules()} {
        if !strings.Contains(text, required) {
            t.Errorf("manual instructions miss %q", required)
        }
    }
}

func TestManualInstructionsForDeepSeekHarness(t *testing.T) {
    installer := NewInstaller(InstallPaths{}, "/usr/local/bin/statectl", nil)
    text := installer.ManualInstructions("deepseek-harness", "deepseek")
    if !strings.Contains(text, "DeepSeek Harness:") {
        t.Fatal("missing DeepSeek Harness hint")
    }
}

func TestManualInstructionsForUnknownHarnessHaveNoProductHint(t *testing.T) {
    installer := NewInstaller(InstallPaths{}, "/usr/local/bin/statectl", nil)
    text := installer.ManualInstructions("my-agent", "my-agent")
    if strings.Contains(text, "Pi Agent:") || strings.Contains(text, "DeepSeek Harness:") {
        t.Fatal("unknown harness must not get product specific hints")
    }
}
```

Achtung: Die JSON-Kodierung der Args kann Leerzeichen anders setzen als im Test erwartet. Prüfe die tatsächliche Ausgabe von `encodeJSONObject` und passe den erwarteten String im Test an die echte Formatierung an (nicht umgekehrt).

Commit: `git commit -m "feat: give manual harness setups product specific guidance"`

## Task 3: Tool-Beschreibungen schärfen

**Datei:** `internal/mcpserver/server.go`

Agenten lesen die Tool-Beschreibungen bei jedem Aufruf. Ändere **nur** diese `Description`-Strings in `registerTools`:

| Tool | Neue Beschreibung |
| --- | --- |
| `get_briefing` | `Start every session here. Returns bounded current reminders and changes since a cursor; keep the returned cursor.` |
| `create_reminder` | `Store exactly one explicit user obligation as a reminder. Ask first when date, time zone or recurrence is ambiguous. Report success only when the result says stored=true.` |
| `update_reminder` | `Change a reminder with optimistic revision checking. Read it first and pass its revision as expected_revision. Agents cannot archive reminders.` |
| `add_comment` | `Append new context, decisions, links or blockers to a reminder without rewriting it.` |

Und im Struct `createReminderInput` den `jsonschema`-Tag von `Description` auf:
`Markdown context. Use sections when known: Objective, Why it matters, Project boundary, Procedure and references, Acceptance criteria, Risk and approvals. Never include secrets.`

Prüfe mit `grep -rn "Create exactly one idempotent reminder" internal`, ob ein Test den alten Text erwartet. Falls ja, den Test auf den neuen Text umstellen.

Prüfung: `go test -race ./internal/mcpserver/ ./internal/statectl/`

Commit: `git commit -m "docs: make state mcp tool descriptions carry the capture rules"`

## Task 4: Anleitung `docs/agent-integration.md`

Englisch. Gliederung:

1. **Overview:** Jeder Agent bekommt eine eigene State-Identität. Tabelle Agent | Harness label | Integration: `codex` (automatisch), `claude-code` (automatisch), `opencode` (automatisch), `deepseek-harness` (manuell), `pi-agent` (manuell).
2. **Pair an agent:** Pairing-Code in der App (Einstellungen, Agenten) oder in der Mac Server App erzeugen, dann `statectl pair --server <url> --code <code> --harness <label> --profile <name>`.
3. **What statectl changes:** Pro Agent die Konfig-Datei und die Regeldatei (aus `DefaultInstallPaths()` in `installer.go` ablesen), markierter Block, Backup-Datei `*.state-backup-<timestamp>`, `statectl uninstall --harness <label>` entfernt beides.
4. **Manual agents:** Ausgabe von `statectl pair` bei unbekannten Agenten, was wohin kopiert wird, Hinweise für Pi Agent und DeepSeek Harness (wie in Task 2).
5. **The capture protocol:** Kurzfassung der Regeln mit Link auf `universal-agent-todo-capture.md`.
6. **Verify:** `statectl doctor --profile <name>` zeigt Server, Protokoll, Actor und Tools.

Verlinke die neue Datei aus `docs/universal-agent-todo-capture.md` Abschnitt 6.3 mit einem Satz (Datei außerhalb deines Bereichs, im Report nennen).

Commit: `git commit -m "docs: explain how every agent is paired with state"`

## Task 5: Gesamtprüfung

```bash
gofmt -l ./cmd ./internal
go vet ./...
go test -race ./...
```

Report und Draft-PR nach Master-Plan Abschnitt 4.4 und 4.5.
