# CLI-01: `statectl briefing` und `statectl changes`

**Welle:** 2 (erst nach CLI-00 auf `main`) · **Slug:** `briefing-changes` · **Branch:** `cli/01-briefing-changes`
**Besitzt:** `cmd/statectl/briefing.go`, neu `cmd/statectl/briefing_test.go`, neu `internal/statectl/briefing.go`, neu `internal/statectl/briefing_test.go`
**Umfang:** klein (Go)

## Goal-Prompt

> Lies `docs/plans/2026-09-23-cli-parity/README.md` vollständig und halte dich strikt an das Arbeitsprotokoll in Abschnitt 5. Setze danach `docs/plans/2026-09-23-cli-parity/CLI-01-briefing-changes.md` Task für Task mit TDD um.

## Ziel

Ein Agent ohne MCP bekommt zum Sitzungsstart denselben Überblick wie über `get_briefing` und kann Änderungen seit einem Cursor abholen wie über `get_changes`.

## Serverantworten (aus `internal/mcpserver/server.go` und `internal/state/models.go`)

- `get_briefing` Eingabe: `after_cursor` (int64, optional), `limit` (int, max 50). Antwort ist ein `state.Briefing`:
  ```json
  {"generated_at": "...", "cursor": 42, "summary": "...", "reminders": [Reminder...], "changes": [{"cursor": 41, "event": AuditEvent}...]}
  ```
- `get_changes` Eingabe: `after_cursor`, `limit` (max 100). Antwort: `{"changes": [...], "cursor": 42}`.

Prüfe beide Formen im Code, bevor du dekodierst.

## Task 1: Service-Methoden

**Dateien:** `internal/statectl/briefing.go`, `internal/statectl/briefing_test.go`

```go
// Briefing calls get_briefing. afterCursor 0 means a first briefing.
func (service *ReminderService) Briefing(ctx context.Context, afterCursor int64, limit int) (json.RawMessage, error)

// Changes calls get_changes.
func (service *ReminderService) Changes(ctx context.Context, afterCursor int64, limit int) (json.RawMessage, error)
```

Regeln:
- `afterCursor < 0` ergibt einen Fehler, der `after` enthält.
- `Briefing`: `limit <= 0` wird 20, über 50 wird auf 50 gekappt.
- `Changes`: `limit <= 0` wird 50, über 100 wird auf 100 gekappt.
- `after_cursor` nur mitschicken, wenn größer 0 (das Feld ist `omitempty`).
- Nutze die vorhandene private Hilfe `callRaw` aus `reminder.go` (gleiches Paket).

Tests zuerst:
1. Mit dem Fake-`ToolCaller` aus `reminder_test.go` (gleiches Paket, `fakeCaller`): richtige Werkzeugnamen und Argumente, Kappung der Limits, Fehler bei negativem Cursor.
2. Gegen den echten In-Memory-Server (`newReminderTestSession(t)`): einen Reminder mit `Create` anlegen, dann `Briefing(ctx, 0, 20)` aufrufen und prüfen, dass `reminders` den Titel enthält und `cursor > 0` ist. Danach `Changes(ctx, 0, 100)`: mindestens ein Eintrag, und der zurückgegebene `cursor` entspricht dem letzten Eintrag.

Commit: `git commit -m "feat: add briefing and changes to the reminder service"`

## Task 2: CLI-Befehle

**Datei:** `cmd/statectl/briefing.go` (die Rümpfe aus CLI-00 ersetzen, `errNotImplemented` hier nicht mehr verwenden, die Variable selbst aber stehen lassen, andere Pakete nutzen sie noch)

- Verbindung wie in `runReminderShow`: `connectReminderService(configPath, profile)`, Profil über `requireReminderProfile`.
- `--json`: rohe Antwort über `writeIndentedJSON`.
- Ohne `--json`, **briefing**:
  ```text
  State · 3 offene Erinnerungen · Cursor 42
  - Monatsreporting (1. Okt. 2026, 09:00, monatlich)  0199...
  - Wichtig                                            0199...
  Neu seit Cursor 40: 2 Änderungen
  ```
  Nutze für Datum und Wiederholung die vorhandene Funktion `summarizeSchedule` aus `reminder.go`. Die Reminder-ID steht am Zeilenende, damit ein Agent sie direkt weiterverwenden kann. Ist `summary` gesetzt, als erste Zeile ausgeben. Englische Ausgabetexte sind auch in Ordnung, entscheidend ist Konsistenz mit den vorhandenen Befehlen; übernimm deren Sprache.
- Ohne `--json`, **changes**: eine Zeile pro Änderung: `cursor  zeitpunkt  aktion  reminder-id  akteur`, am Ende `cursor: N` für die nächste Abfrage.

Tests in `cmd/statectl/briefing_test.go`: nur Flag-Fehler ohne Netzwerk (negativer `--after`, fehlendes Profil ohne `STATECTL_PROFILE`). Die Formatierung testest du als reine Funktion (`formatBriefing(raw json.RawMessage) (string, error)`) mit einem festen JSON-Beispiel.

Commit: `git commit -m "feat: add statectl briefing and changes"`

## Task 3: Abschluss

Pflichtprüfung, `TestEveryMappedCommandIsRoutable` muss weiter grün sein, Report `reports/CLI-01-report.md` mit Beispielausgabe beider Befehle, Draft-PR.
