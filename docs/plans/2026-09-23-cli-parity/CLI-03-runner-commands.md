# CLI-03: `statectl run` für alle Runner-Werkzeuge

**Welle:** 2 (erst nach CLI-00 auf `main`) · **Slug:** `runner-commands` · **Branch:** `cli/03-runner-commands`
**Besitzt:** `cmd/statectl/run.go`, neu `cmd/statectl/run_test.go`, neu `internal/statectl/run.go`, neu `internal/statectl/run_test.go`
**Umfang:** mittel bis groß (Go)

## Goal-Prompt

> Lies `docs/plans/2026-09-23-cli-parity/README.md` vollständig und halte dich strikt an das Arbeitsprotokoll in Abschnitt 5. Setze danach `docs/plans/2026-09-23-cli-parity/CLI-03-runner-commands.md` Task für Task mit TDD um.

## Ziel

Jedes Runner-Werkzeug des MCP-Servers ist per CLI aufrufbar: Lauf-Kontext lesen, Lauf beanspruchen, Ereignisse melden, Lauf abschließen, Freigabe anfordern. Damit lässt sich ein Runner-Ablauf komplett im Terminal nachvollziehen, testen oder notfalls von Hand durchführen.

## Serverregeln (nachlesen in `internal/mcpserver/server.go`)

| Werkzeug | Wer darf | Eingabe |
| --- | --- | --- |
| `get_execution_context` | Runner oder Owner | `run_id` |
| `claim_agent_run` | nur Runner | `wait_seconds` (max 25) |
| `report_agent_run_event` | nur Runner | `run_id`, `event` (`started`, `progress`, `heartbeat`), `detail`, `expected_revision` |
| `complete_agent_run` | nur Runner | `run_id`, `outcome` (`succeeded`, `failed`), `exit_code`, `result_summary` (max 2000 Zeichen), `result_artifact_ref`, `failure_code` (nur `adapter_unavailable`), `expected_revision`, `client_request_id`, `source_text` |
| `request_agent_approval` | nur Runner | `run_id`, `capability`, `reason`, `expected_revision` |

`get_execution_context` liefert den Lauf unter dem Schlüssel `run` samt `revision`. Die Befehle `event`, `complete` und `approval` holen die aktuelle Revision darüber selbst, der Nutzer gibt sie nie an.

## Anmeldung

**Import-Falle:** `internal/runner` importiert `internal/statectl`. Deshalb liegt die Runner-Anmeldung in `cmd/statectl/run.go`, nie in `internal/statectl`.

Reihenfolge:
1. `--profile P` gesetzt: statectl-Profil wie bei den anderen Befehlen (`loadProfileAndCredential`). Sinnvoll nur für `run context` mit einem Owner-Profil. Für die übrigen `run`-Befehle lehnt der Server es mit `forbidden` ab. Diese Meldung übersetzt der Befehl in: `run <befehl> needs a runner credential; omit --profile to use the paired state-runner`.
2. Sonst Runner-Konfiguration: `runner.LoadRunnerConfig(pfad)` mit `--runner-config` oder `runner.DefaultConfigPath()`. Schlüssel aus dem Schlüsselbund über `statectl.KeyringSecretStore{}.Get(config.CredentialAccount())`. Verbindung mit `statectl.ConnectRemote(ctx, statectl.Profile{ServerURL: config.ServerURL}, token, version)`. Prüfe, welche Felder `ConnectRemote` wirklich aus `Profile` liest.

Die Anmeldelogik ist eine eigene Funktion, die im Test durch eine Fake-Variante ersetzt werden kann:

```go
// runCredential resolves the server URL and bearer token for a run command.
type runCredential struct {
	ServerURL string
	Token     string
}

var loadRunCredential = func(configPath, runnerConfigPath, profileName string) (runCredential, error) { ... }
```

## Task 1: Service

**Dateien:** `internal/statectl/run.go`, `internal/statectl/run_test.go`

```go
type RunService struct{ /* caller ToolCaller; newID func() (string, error) */ }

func NewRunService(caller ToolCaller, newID func() (string, error)) *RunService
func (service *RunService) Context(ctx context.Context, runID string) (json.RawMessage, error)
func (service *RunService) Claim(ctx context.Context, waitSeconds int) (json.RawMessage, error)
func (service *RunService) Event(ctx context.Context, runID, event, detail string) (json.RawMessage, error)
func (service *RunService) Complete(ctx context.Context, options CompleteRunOptions) (json.RawMessage, error)
func (service *RunService) RequestApproval(ctx context.Context, runID, capability, reason string) (json.RawMessage, error)

type CompleteRunOptions struct {
	RunID         string
	Outcome       string // succeeded or failed
	ExitCode      int
	Summary       string
	ArtifactRef   string
	FailureCode   string
	SourceText    string
	RequestID     string
}
```

Validierung vor jedem Aufruf, jede mit Testfall:
- leere `runID`: Fehler.
- `waitSeconds < 0`: Fehler; über 25: auf 25 gekappt.
- `event` nicht in `started`, `progress`, `heartbeat`: Fehler.
- `Outcome` nicht in `succeeded`, `failed`: Fehler. `Summary` länger als 2000 Zeichen: Fehler. `FailureCode` nicht leer und nicht `adapter_unavailable`: Fehler.
- leere `capability`: Fehler.

Revision: private Hilfe `currentRunRevision(ctx, runID)`, die `get_execution_context` aufruft und `run.revision` liest.

Ein leeres Claim-Ergebnis (kein Lauf fällig) ist **kein** Fehler. Prüfe in `claimAgentRun`, wie der Server "nichts zu tun" meldet, und gib das unverändert zurück.

Tests:
1. Fake-Caller aus `reminder_test.go`: Validierung, Werkzeugnamen, Argumente, Revision aus dem Kontext.
2. In-Memory-Server mit Runner-Identität: Übernimm das Muster aus `internal/mcpserver/execution_test.go` (Owner anlegen, Runner koppeln, Projekt, Policy, Reminder mit Policy, manuellen Lauf anlegen). Lege dafür einen eigenen Helfer `newRunnerTestSession(t)` in `run_test.go` an. Durchlauf: `Claim` liefert den Lauf, `Event(started)`, `Event(heartbeat)`, `Complete(succeeded, exit 0)`, danach zeigt `Context` den Status `succeeded`. Gelingt der Aufbau nach drei ernsthaften Versuchen nicht, im Report genau beschreiben, woran es scheitert, und nur die Fake-Tests abgeben.

Commit: `git commit -m "feat: add a run service for every runner tool"`

## Task 2: CLI

**Datei:** `cmd/statectl/run.go` (Rümpfe aus CLI-00 ersetzen)

- Flags exakt wie in CLI-00 angelegt.
- Ausgabe ohne `--json`:
  - `context`: `run <id>  <status>  reminder "<titel>"  adapter <adapter>  revision <n>`
  - `claim`: `claimed run <id> for reminder "<titel>"` oder `no run is due`
  - `event`: `reported <event> for run <id>`
  - `complete`: `completed run <id> as <outcome>`
  - `approval`: `requested approval for <capability> on run <id>`
- Mit `--json` die rohe Antwort.

Tests in `cmd/statectl/run_test.go`: Flag-Fehler ohne Netzwerk, und mit überschriebenem `loadRunCredential` die Übersetzung der `forbidden`-Meldung bei `--profile`.

Commit: `git commit -m "feat: add statectl run commands"`

## Task 3: Abschluss

Pflichtprüfung, Report `reports/CLI-03-report.md` mit Beispielausgaben, Draft-PR.
