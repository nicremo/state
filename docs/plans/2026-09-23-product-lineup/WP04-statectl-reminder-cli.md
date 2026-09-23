# WP04: `statectl reminder` als CLI-Ausweichweg

**Welle:** 1 · **Slug:** `statectl-reminder-cli` · **Branch:** `wp/04-statectl-reminder-cli`
**Besitzt:** `cmd/statectl/main.go` (nur `run`-Switch und Usage-Text), neu `cmd/statectl/reminder.go`, neu `cmd/statectl/reminder_test.go`, neu `internal/statectl/reminder.go`, neu `internal/statectl/reminder_test.go`
**Nicht anfassen:** `internal/statectl/rules.go`, `internal/statectl/installer.go` (gehören WP05)
**Geschätzter Umfang:** mittel (Go)

## Goal-Prompt (so an die Worker-Session geben)

> Lies `docs/plans/2026-09-23-product-lineup/README.md` vollständig und halte dich an das Arbeitsprotokoll in Abschnitt 4. Setze danach `docs/plans/2026-09-23-product-lineup/WP04-statectl-reminder-cli.md` Task für Task mit TDD um. Arbeite nur in deinem eigenen Worktree. Schließe mit Report und Draft-PR ab.

## Ziel

Agenten ohne MCP-Unterstützung (oder ein Mensch im Terminal) sollen Reminders anlegen, Kontext ergänzen, umplanen, anzeigen und suchen können:

```text
statectl reminder create      --profile P --title T --source-text S [--description D | --description-file F]
                              [--date YYYY-MM-DD [--time HH:MM] [--tz Europe/Berlin] [--prewarning 10]]
                              [--repeat daily|weekly|monthly|yearly [--interval 1] [--until YYYY-MM-DD]]
                              [--request-id UUIDv7] [--json]
statectl reminder add-context --profile P --id R --source-text S (--body B | --body-file F) [--json]
statectl reminder schedule    --profile P --id R --source-text S
                              (--clear | --date YYYY-MM-DD [--time HH:MM] [--tz ...] [--prewarning N])
                              [--repeat ... [--interval N] [--until ...] | --clear-repeat] [--json]
statectl reminder show        --profile P --id R [--json]
statectl reminder search      --profile P --query Q [--limit 20] [--json]
```

**Kernregel aus `docs/universal-agent-todo-capture.md` Abschnitt 6.4:** Die CLI ist nur ein weiterer Client. Sie ruft **exakt dieselben MCP-Tools** auf wie ein Agent (`create_reminder`, `add_comment`, `get_reminder`, `update_reminder`, `search_reminders`). Kein direkter Datenbankzugriff, keine eigene REST-Logik, kein zweites Persistenzmodell.

## Pflichtlektüre

1. `cmd/statectl/main.go` (Aufbau der Unterbefehle, `loadProfileAndCredential`, `defaultConfigPath`)
2. `internal/statectl/remote.go` (`ConnectRemote` liefert `*mcp.ClientSession`)
3. `internal/mcpserver/server.go`: die Input-Structs `createReminderInput`, `updateReminderInput`, `addCommentInput`, `searchRemindersInput`, `getReminderInput` und die Funktionen `createReminder`, `updateReminder`, `getReminder`, `reminderDetail`, `searchReminders`. Notiere die **genauen JSON-Feldnamen** und die **Form der Rückgabe** (z.B. `{"stored": true, "reminder": {...}}`).
4. `internal/state/models.go`: `Schedule` (`local_date`, `local_time`, `time_zone`, `mode`, `prewarning_minutes`), `TimeZoneMode` (`floating`, `fixed`), `RecurrenceRule` (`frequency`, `interval`, `until_date`), `Reminder` (Feld `revision`).
5. `internal/statectl/proxy_test.go` und `internal/mcpserver/server_test.go`: Wie Tests einen echten MCP-Server im Speicher starten und wie Tool-Ergebnisse gelesen werden. Suche nach `StructuredContent` im Repo (`grep -rn StructuredContent internal cmd`), um zu sehen, wie Ergebnisse dekodiert werden.

UUIDv7 erzeugst du mit `github.com/google/uuid` (`uuid.NewV7()`), das ist bereits Abhängigkeit (siehe `internal/state/service.go`).

## Architektur

```
cmd/statectl/reminder.go          Flag-Parsing, Ausgabe (Text oder JSON)
        │ ruft
internal/statectl/reminder.go     ReminderService: baut Tool-Argumente, ruft Tools, dekodiert Ergebnisse
        │ über Interface
ToolCaller (erfüllt von *mcp.ClientSession)
```

Die Logik in `internal/statectl` ist ohne Netzwerk testbar, weil sie nur das Interface `ToolCaller` sieht.

## Task 1: Argumente bauen und validieren

**Dateien:** `internal/statectl/reminder.go`, `internal/statectl/reminder_test.go`

**Interfaces (Produces):**

```go
// ToolCaller is the subset of *mcp.ClientSession the reminder commands need.
type ToolCaller interface {
    CallTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error)
}

type ScheduleOptions struct {
    Date       string // YYYY-MM-DD, empty means no schedule
    Time       string // HH:MM, optional
    TimeZone   string // IANA name, empty means the local zone of the machine
    Prewarning int    // minutes, 0 means none
    Repeat     string // "", daily, weekly, monthly, yearly
    Interval   int    // >= 1 when Repeat is set, default 1
    Until      string // YYYY-MM-DD, optional
}

type CreateReminderOptions struct {
    Title       string
    Description string
    SourceText  string
    RequestID   string // optional, generated when empty
    Schedule    ScheduleOptions
}

func BuildSchedule(options ScheduleOptions, localZone string) (*state.Schedule, *state.RecurrenceRule, error)
func BuildCreateReminderArguments(options CreateReminderOptions, localZone string, newID func() (string, error)) (map[string]any, error)
```

Validierungsregeln (jede mit eigenem Testfall):

| Eingabe | Erwartung |
| --- | --- |
| `Title` leer oder nur Leerzeichen | Fehler `title is required` |
| `SourceText` leer | Fehler `source text is required` |
| `Date` = `2026-13-01` | Fehler, der `date` enthält |
| `Time` ohne `Date` | Fehler `time requires a date` |
| `Time` = `25:00` | Fehler, der `time` enthält |
| `TimeZone` = `Mars/Base` | Fehler, der `time zone` enthält (prüfen mit `time.LoadLocation`) |
| `Repeat` = `hourly` | Fehler, der `repeat` enthält |
| `Repeat` gesetzt, `Date` leer | Fehler `repeat requires a date` |
| `Interval` = 0 bei gesetztem `Repeat` | wird zu 1 |
| `Interval` < 0 | Fehler |
| `Until` vor `Date` | Fehler, der `until` enthält |
| `Date` gesetzt, `TimeZone` leer | `time_zone` = `localZone`, `mode` = `floating` |
| `TimeZone` explizit gesetzt | `mode` = `fixed` |
| `RequestID` leer | `client_request_id` = Ergebnis von `newID()` |

Erwartete Argument-Map für einen vollen Aufruf (Test mit `reflect.DeepEqual` oder Vergleich der JSON-Kodierung):

```go
map[string]any{
    "title":             "Monthly reporting",
    "description":       "Prepare the report for the board.",
    "source_text":       "I need to do the monthly report every month",
    "client_request_id": "01990000-0000-7000-8000-000000000001",
    "schedule":          &state.Schedule{LocalDate: "2026-10-01", LocalTime: "09:00", TimeZone: "Europe/Berlin", Mode: state.TimeZoneModeFixed, PrewarningMinutes: 30},
    "recurrence":        &state.RecurrenceRule{Frequency: state.RecurrenceMonthly, Interval: 1},
}
```

Ohne Schedule fehlen die Schlüssel `schedule` und `recurrence` ganz. Leere `description` fehlt ebenfalls.

**Hinweis:** Prüfe in `internal/state/models.go`, ob `mode` beim Erstellen serverseitig Pflicht ist und welche Werte erlaubt sind. Prüfe in `internal/state/service.go` (Validierung beim Erstellen), ob die Zeit ohne Datum oder andere Kombinationen schon serverseitig abgelehnt werden. Die CLI validiert trotzdem selbst, damit Fehler vor dem Netzwerkaufruf kommen.

Schritte:
1. Tabellengetriebenen Test `TestBuildCreateReminderArguments` schreiben (alle Zeilen oben).
2. `go test ./internal/statectl/ -run TestBuildCreateReminderArguments` → FAIL.
3. Implementieren.
4. Test grün.
5. `git commit -m "feat: validate and build reminder arguments for statectl"`

## Task 2: ReminderService mit Fake-ToolCaller

**Dateien:** `internal/statectl/reminder.go`, `internal/statectl/reminder_test.go`

**Interfaces (Produces):**

```go
type StoredReminder struct {
    ID       string          `json:"id"`
    Title    string          `json:"title"`
    Revision int64           `json:"revision"`
    Schedule *state.Schedule `json:"schedule,omitempty"`
    Raw      json.RawMessage `json:"-"` // full reminder object as returned
}

type ReminderService struct { /* caller ToolCaller; localZone string; newID func() (string, error) */ }

func NewReminderService(caller ToolCaller, localZone string, newID func() (string, error)) *ReminderService
func (service *ReminderService) Create(ctx context.Context, options CreateReminderOptions) (StoredReminder, error)
func (service *ReminderService) AddContext(ctx context.Context, reminderID, body, sourceText string) error
func (service *ReminderService) Reschedule(ctx context.Context, reminderID string, options ScheduleOptions, clearSchedule, clearRepeat bool, sourceText string) (StoredReminder, error)
func (service *ReminderService) Show(ctx context.Context, reminderID string) (json.RawMessage, error)
func (service *ReminderService) Search(ctx context.Context, query string, limit int) (json.RawMessage, error)
```

Verhalten:

1. **Create** ruft Tool `create_reminder` mit den Argumenten aus Task 1. Ergebnis dekodieren: Ist `result.IsError` wahr, Fehler mit dem Text aus `result.Content` zurückgeben. Sonst `stored` muss `true` sein, sonst Fehler `server did not confirm the reminder`. **Nie Erfolg melden ohne `stored: true`** (Regel aus der Spezifikation).
2. **AddContext** ruft `add_comment` mit `reminder_id`, `body`, `source_text`, neuer `client_request_id`. Leerer Body ist ein Fehler.
3. **Reschedule**: erst `get_reminder`, daraus `revision` lesen, dann `update_reminder` mit `expected_revision`, neuem `client_request_id`, `source_text` und entweder `schedule`/`recurrence` oder `clear_schedule: true` bzw. `clear_recurrence: true`. `--clear` und `--date` gleichzeitig ist ein Fehler. Achtung: Prüfe in `updateReminderInput`, ob `title` weggelassen werden darf (Pointer-Feld, `omitempty`). Nur die Felder senden, die sich ändern.
4. **Show** ruft `get_reminder` und gibt das rohe JSON zurück.
5. **Search** ruft `search_reminders` mit `query` und `limit` (Default 20, maximal 100).

Wie die Ergebnisse dekodiert werden, entnimmst du den Tests in `internal/mcpserver/server_test.go` (dort wird `StructuredContent` oder der Text-Inhalt gelesen). Kapsle das in einer Hilfsfunktion:

```go
func decodeToolResult(result *mcp.CallToolResult, target any) error
```

Test-Fake (in `reminder_test.go`):

```go
type fakeCaller struct {
    calls   []*mcp.CallToolParams
    results map[string]*mcp.CallToolResult // key: tool name
}

func (fake *fakeCaller) CallTool(_ context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
    fake.calls = append(fake.calls, params)
    result, ok := fake.results[params.Name]
    if !ok {
        return nil, fmt.Errorf("unexpected tool %s", params.Name)
    }
    return result, nil
}
```

Baue die Fake-Ergebnisse genau in der Form, die der echte Server liefert (siehe Pflichtlektüre Punkt 3 und 5). Tests:

- `TestCreateRequiresStoredConfirmation`: Ergebnis mit `stored: false` → Fehler.
- `TestCreateReturnsReminder`: korrektes Ergebnis → ID, Titel, Revision gesetzt, genau ein Aufruf `create_reminder`.
- `TestCreateSurfacesToolErrors`: `IsError: true` mit Text `validation failed` → Fehler enthält `validation failed`.
- `TestRescheduleUsesCurrentRevision`: `get_reminder` liefert Revision 7 → `update_reminder`-Argumente enthalten `expected_revision: 7`.
- `TestRescheduleRejectsClearWithDate`.
- `TestAddContextRejectsEmptyBody`.

Commit: `git commit -m "feat: add a reminder service that speaks the state mcp tools"`

## Task 3: Integrationstest gegen einen echten In-Memory-Server

**Datei:** `internal/statectl/reminder_test.go`

Suche in `internal/statectl/proxy_test.go` den Helfer, der einen Test-Server mit `mcpserver.NewHandler` und einem Speicher-Repository startet und eine Harness-Session anlegt. Nutze ihn (oder kopiere das Muster), um:

1. Einen Reminder mit Datum, Uhrzeit, Zeitzone `Europe/Berlin` und `monthly` zu erstellen.
2. Ihn mit `Show` zu lesen und zu prüfen, dass `recurrence.frequency == "monthly"`.
3. `AddContext` aufzurufen und zu prüfen, dass `Show` den Kommentar enthält.
4. `Reschedule` auf ein anderes Datum und zu prüfen, dass die Revision gestiegen ist.
5. Denselben `Create` mit derselben `RequestID` zweimal auszuführen und zu prüfen, dass dieselbe Reminder-ID zurückkommt (Idempotenz).

Wenn es keinen wiederverwendbaren Helfer gibt und du nach drei Versuchen keinen Server aufsetzen kannst: Task als nicht erledigt markieren, im Report begründen, weiter mit Task 4.

Commit: `git commit -m "test: exercise statectl reminders against an in-memory server"`

## Task 4: CLI-Befehle

**Dateien:** `cmd/statectl/reminder.go`, `cmd/statectl/reminder_test.go`, `cmd/statectl/main.go`

1. In `main.go` im `switch args[0]` einen Fall ergänzen: `case "reminder": return runReminder(args[1:], stdout, stderr)`. Usage-Text in der ersten `errors.New("usage: ...")` um `reminder` erweitern. **Sonst nichts in `main.go` ändern.**
2. `cmd/statectl/reminder.go`:
   - `runReminder` verteilt auf `create`, `add-context`, `schedule`, `show`, `search`. Unbekannt oder leer: `usage: statectl reminder <create|add-context|schedule|show|search>`.
   - Jeder Unterbefehl nutzt `flag.NewFlagSet` wie die bestehenden Befehle, mit `--profile` (Pflicht) und `--config` (Default `defaultConfigPath()`).
   - Verbindung: `loadProfileAndCredential`, dann `statectl.ConnectRemote(ctx, profile, token, version)`, `defer session.Close()`, dann `statectl.NewReminderService(session, localZone, newUUIDv7)`.
   - `localZone`: `time.Local.String()`. Wenn das `"Local"` ergibt, `TZ`-Variable prüfen, sonst Fehler `cannot determine local time zone, pass --tz`.
   - `--description-file` / `--body-file`: Datei lesen, maximal 64 KB, sonst Fehler. `-` bedeutet stdin.
   - Ausgabe ohne `--json`: eine Zeile, z.B. `stored reminder 0199... "Monthly reporting" (2026-10-01 09:00 Europe/Berlin, monthly)`. Mit `--json`: das rohe Reminder-JSON, eingerückt.
   - Ein Kontext mit 30 Sekunden Timeout für jeden Aufruf.
3. `cmd/statectl/reminder_test.go`: Teste nur Flag-Fehler ohne Netzwerk, über `run([]string{...}, &stdout, &stderr, logger)`:
   - `reminder` ohne Unterbefehl → Fehler mit `usage`.
   - `reminder create` ohne `--profile` → Fehler.
   - `reminder schedule --profile x --id y --source-text s --clear --date 2026-10-01` → Fehler, bevor eine Verbindung versucht wird. (Dafür muss die Flag-Validierung **vor** `loadProfileAndCredential` passieren.)

Commit: `git commit -m "feat: add statectl reminder commands as a terminal fallback"`

## Task 5: Doku

Ergänze in `README.md` (Datei außerhalb deines Bereichs, im Report nennen) im Abschnitt zum Pairing einen kurzen Unterabschnitt `### Terminal fallback` mit einem Beispiel:

```bash
statectl reminder create --profile codex \
  --title "Monthly reporting" \
  --source-text "I need to do the monthly report every month" \
  --date 2026-10-01 --time 09:00 --tz Europe/Berlin --repeat monthly
```

Commit: `git commit -m "docs: show the statectl reminder fallback"`

## Task 6: Gesamtprüfung

```bash
gofmt -l ./cmd ./internal
go vet ./...
go test -race ./...
go build -o /tmp/wp04-statectl ./cmd/statectl && /tmp/wp04-statectl reminder 2>&1 | head -3
```

Report und Draft-PR nach Master-Plan Abschnitt 4.4 und 4.5.
