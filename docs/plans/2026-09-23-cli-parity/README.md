# CLI-Parität: statectl kann alles, was der MCP kann

**Stand:** 23.09.2026
**Koordinator:** Hauptsession (Review, Fixes, Merges, Installation, Freigabe)
**Worker:** eine Session pro Arbeitspaket, jeweils im eigenen Git-Worktree

Jeder Worker liest diese Datei vollständig, bevor er anfängt.

## 1. Ziel

`statectl` wird mindestens so mächtig wie der MCP-Server "state". Für **jedes** MCP-Werkzeug gibt es einen CLI-Befehl, der exakt dasselbe Werkzeug aufruft. Fällt bei einem Agenten die MCP-Anbindung aus (Konfiguration kaputt, Harness ohne MCP, Sitzung vor der Installation gestartet), erledigt er alles über das Terminal.

Ein Test erzwingt die Parität dauerhaft: Er startet einen echten MCP-Server im Speicher, listet dessen Werkzeuge und schlägt fehl, sobald ein Werkzeug keinen CLI-Befehl hat.

**Grenze, die bleibt:** Die CLI spricht intern ebenfalls mit dem Endpunkt `/mcp` des Servers. Ist der Server nicht erreichbar, scheitern MCP und CLI gleichermaßen. Das ist gewollt: ein Server, eine Wahrheit, keine zweite Persistenz.

## 2. Ausgangslage (Stand `main`)

Vorhanden in `cmd/statectl/reminder.go` und `internal/statectl/reminder.go`:

| MCP-Werkzeug | CLI heute |
| --- | --- |
| `create_reminder` | `statectl reminder create` |
| `search_reminders` | `statectl reminder search` |
| `get_reminder` | `statectl reminder show` |
| `add_comment` | `statectl reminder add-context` |
| `update_reminder` (nur Termin) | `statectl reminder schedule` |

Es fehlen: `get_briefing`, `get_changes`, `update_reminder` für Titel und Beschreibung, `complete_occurrence`, `snooze_occurrence` sowie die fünf Runner-Werkzeuge `get_execution_context`, `claim_agent_run`, `report_agent_run_event`, `complete_agent_run`, `request_agent_approval`.

Wichtige Bausteine, die wiederverwendet werden:

- `statectl.ToolCaller` (Interface, erfüllt von `*mcp.ClientSession`), `statectl.ReminderService`, `decodeToolResult`, `decodeConfirmation`, `decodeStoredReminder`, `toolErrorText` in `internal/statectl/reminder.go`.
- `connectReminderService`, `loadProfileAndCredential`, `newUUIDv7`, `detectLocalTimeZone`, `resolveTextInput`, `writeIndentedJSON` in `cmd/statectl/`.
- Testhelfer `newReminderTestSession(t)` und `newReminderTestHandler(t)` in `internal/statectl/reminder_test.go`: echter MCP-Server im Speicher mit einer Harness-Identität.
- Die MCP-Input-Structs mit exakten JSON-Feldnamen stehen in `internal/mcpserver/server.go` (`getBriefingInput` bis `requestAgentApprovalInput`). **Feldnamen immer dort nachlesen, nie raten.**

**Import-Falle:** `internal/runner` importiert `internal/statectl`. `internal/statectl` darf deshalb **nie** `internal/runner` importieren (Zyklus). Alles, was die Runner-Konfiguration oder den Runner-Schlüssel liest, gehört nach `cmd/statectl/`.

## 3. Verbindlicher Befehlsvertrag

Alle Pakete halten sich exakt an diese Oberfläche. Gemeinsame Regeln für **jeden** Befehl:

- `--profile P` (Pflicht, außer bei `run`), Default aus der Umgebungsvariable `STATECTL_PROFILE`, wenn gesetzt (Paket CLI-00).
- `--config PATH` wie bei den bestehenden Befehlen.
- `--json` gibt die rohe Server-Antwort eingerückt aus. Ohne `--json` eine kurze, lesbare Ausgabe.
- Schreibende Befehle verlangen `--source-text` (Originalwortlaut) und erzeugen eine frische UUIDv7 als `client_request_id`. Optional `--request-id` für Wiederholungen.
- Alle Eingaben werden **vor** dem Verbindungsaufbau validiert.
- Exit-Code 0 bei Erfolg, 1 bei jedem Fehler. Fehlertext nach stderr.
- Ein Kontext mit 30 Sekunden Timeout pro Aufruf, bei `run claim` Wartezeit plus 10 Sekunden.

| MCP-Werkzeug | CLI-Befehl | Paket |
| --- | --- | --- |
| `get_briefing` | `statectl briefing [--after N] [--limit N]` | CLI-01 |
| `get_changes` | `statectl changes [--after N] [--limit N]` | CLI-01 |
| `search_reminders` | `statectl reminder search` (vorhanden) | |
| `get_reminder` | `statectl reminder show` (vorhanden) | |
| `create_reminder` | `statectl reminder create` (vorhanden) | |
| `add_comment` | `statectl reminder add-context` (vorhanden) | |
| `update_reminder` | `statectl reminder update --id R --source-text S [--title T] [--description D \| --description-file F \| --clear-description]` und `statectl reminder schedule` (vorhanden, Termin) | CLI-02 |
| `complete_occurrence` | `statectl reminder complete --id R --source-text S [--occurrence O]` | CLI-02 |
| `snooze_occurrence` | `statectl reminder snooze --id R --source-text S (--for 10m\|2h\|1d \| --until RFC3339) [--occurrence O]` | CLI-02 |
| `get_execution_context` | `statectl run context --run R` | CLI-03 |
| `claim_agent_run` | `statectl run claim [--wait 25]` | CLI-03 |
| `report_agent_run_event` | `statectl run event --run R --event started\|progress\|heartbeat [--detail D]` | CLI-03 |
| `complete_agent_run` | `statectl run complete --run R --outcome succeeded\|failed --exit-code N [--summary S] [--artifact A] [--failure-code adapter_unavailable]` | CLI-03 |
| `request_agent_approval` | `statectl run approval --run R --capability C [--reason T]` | CLI-03 |

Ohne `--occurrence` wirken `complete` und `snooze` auf das **nächste offene Vorkommen** des Reminders (Status `pending` oder `snoozed`, frühestes `local_date` und `local_time`). Die nötige `expected_revision` holen die Befehle selbst über `get_reminder`, genauso wie `reminder schedule` es heute tut.

Die `run`-Befehle melden sich standardmäßig mit dem **Runner-Schlüssel** an (Konfiguration aus `runner.DefaultConfigPath()`, Schlüssel aus dem Schlüsselbund unter `RunnerConfig.CredentialAccount()`). Alternativ nimmt `--profile P` ein statectl-Profil. Der Server erlaubt `get_execution_context` auch dem Owner, die übrigen Runner-Werkzeuge nur einem Runner.

## 4. Pakete und Wellen

| Welle | Paket | Datei | Inhalt |
| --- | --- | --- | --- |
| 1 (allein) | CLI-00 | `CLI-00-foundation.md` | Befehlsgerüst, `STATECTL_PROFILE`, Paritäts-Tests |
| 2 (parallel) | CLI-01 | `CLI-01-briefing-changes.md` | `briefing`, `changes` |
| 2 (parallel) | CLI-02 | `CLI-02-reminder-update-complete-snooze.md` | `reminder update`, `complete`, `snooze` |
| 2 (parallel) | CLI-03 | `CLI-03-runner-commands.md` | alle `run`-Befehle |
| 3 (allein) | CLI-04 | `CLI-04-docs-rules-e2e.md` | Hilfe, Doku, Regeltext, End-to-End-Test |

Welle 2 startet erst, wenn CLI-00 auf `main` ist. Jedes Paket der Welle 2 besitzt eigene Dateien und ersetzt nur **seine** Platzhalter aus CLI-00. So entstehen keine Merge-Konflikte.

## 5. Arbeitsprotokoll (verpflichtend)

Es gilt das Protokoll aus `docs/plans/2026-09-23-product-lineup/README.md` Abschnitt 4 unverändert, mit diesen Anpassungen:

- Worktree: `git worktree add ~/Desktop/state-worktrees/cli<nn>-<slug> -b cli/<nn>-<slug> origin/main`
- Report: `docs/plans/2026-09-23-cli-parity/reports/CLI-<nn>-report.md`
- Draft-PR mit Titel `CLI-<nn>: <Titel>`, **kein Merge**.
- Pflichtprüfung vor dem Abschluss:
  ```bash
  gofmt -l ./cmd ./internal      # muss leer sein
  go vet ./...
  GOTOOLCHAIN=local go test -race ./...
  go build -o /tmp/statectl-cli<nn> ./cmd/statectl && /tmp/statectl-cli<nn> --help
  ```
- Du veränderst **nie** die installierte `~/.local/bin/statectl`, keine echten Profile und keinen echten Schlüsselbund-Eintrag. Tests nutzen ausschließlich den In-Memory-Server und temporäre Verzeichnisse.
- Texte und Reports auf Deutsch mit korrekten Umlauten, keine Gedankenstriche. Code-Kommentare und Commits auf Englisch, keine KI-Attribution.

## 6. Abnahme

Die Aufgabe ist erfüllt, wenn:

1. `TestEveryMCPToolHasACLICommand` grün ist und jedes der 14 Werkzeuge abdeckt.
2. Jeder Befehl aus Abschnitt 3 existiert, `--help` liefert, gegen den In-Memory-Server getestet ist und mit `--json` die Server-Antwort ausgibt.
3. `statectl --help` und `docs/cli.md` alle Befehle mit Beispiel zeigen.
4. Der Koordinator auf Fabians Mac per Hand zeigt: Reminder anlegen, umbenennen, abhaken, zurückstellen, Briefing lesen, alles ausschließlich per CLI.

## 7. Goal-Prompts zum Kopieren

```text
Lies docs/plans/2026-09-23-cli-parity/README.md vollständig und halte dich strikt an das Arbeitsprotokoll in Abschnitt 5 (eigener Worktree, nur eigene Dateien, TDD, Report, Draft-PR, kein Merge). Setze danach docs/plans/2026-09-23-cli-parity/CLI-00-foundation.md Task für Task um.
```

Für die anderen Pakete denselben Text mit dem jeweiligen Dateinamen: `CLI-01-briefing-changes.md`, `CLI-02-reminder-update-complete-snooze.md`, `CLI-03-runner-commands.md`, `CLI-04-docs-rules-e2e.md`.
