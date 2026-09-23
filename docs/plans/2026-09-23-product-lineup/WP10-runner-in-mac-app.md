# WP10: Runner als LaunchAgent, gesteuert aus der App

**Welle:** 2 (erst starten, wenn WP06 auf `main` ist) · **Slug:** `runner-service` · **Branch:** `wp/10-runner-service`
**Besitzt:** `cmd/state-runner/main.go` (nur neuer Unterbefehl `service`), neu `cmd/state-runner/service.go`, neu `cmd/state-runner/service_test.go`, neu `internal/runner/service.go`, neu `internal/runner/service_test.go`, neu `scripts/install-agent-tools.sh`, `ios/State/Sources/UI/SettingsView.swift` (**nur** Abschnitt "Runners" und die Funktion `runnerPairingCommand`), `docs/runner-service.md`
**Geschätzter Umfang:** mittel (Go + wenig Swift)

## Goal-Prompt (so an die Worker-Session geben)

> Lies `docs/plans/2026-09-23-product-lineup/README.md` vollständig und halte dich an das Arbeitsprotokoll in Abschnitt 4. Setze danach `docs/plans/2026-09-23-product-lineup/WP10-runner-in-mac-app.md` Task für Task mit TDD um. Du installierst keinen echten LaunchAgent auf diesem Mac und rufst `launchctl` nur in Tests über ein Fake auf. Schließe mit Report und Draft-PR ab.

## Warum so und nicht im App-Bundle

Die Mac-App aus WP06 wird über TestFlight und den App Store verteilt und läuft deshalb in der **App Sandbox**. Ein sandboxed Prozess darf weder `claude`, `codex` oder `opencode` in Projektordnern starten noch `~/Library/LaunchAgents` beschreiben. Der Runner braucht aber genau das. Deshalb:

- `state-runner` läuft als **eigener LaunchAgent** des Benutzers (`~/Library/LaunchAgents/com.fabincrm.state.runner.plist`), installiert per `state-runner service install`.
- Die App (iPhone, iPad, Mac) zeigt die Runner mit Online-Status (die Daten kommen schon heute per Sync: `model.runners`, Feld `lastSeenAt`) und erzeugt Pairing-Code plus den vollständigen Installationsbefehl zum Kopieren.

## Pflichtlektüre

1. `cmd/state-runner/main.go` (Unterbefehle `pair`, `run`, `version`, Flag-Stil)
2. `internal/runner/config.go` (`DefaultConfigPath`, `RunnerConfig`)
3. `ios/State/Sources/UI/SettingsView.swift` Abschnitt "Runners" und Funktion `runnerPairingCommand(code:)`
4. `ios/State/Sources/Models/Models.swift`: Typ `Runner` (Felder `displayName`, `lastSeenAt`, `adapters`, `projects`)
5. `docs/agent-execution-implementation-plan.md` (Runner-Abschnitte)

## Task 1: LaunchAgent-Plist erzeugen (reine Funktion)

**Dateien:** `internal/runner/service.go`, `internal/runner/service_test.go`

**Interfaces (Produces):**

```go
const ServiceLabel = "com.fabincrm.state.runner"

type ServiceSpec struct {
    Executable string // absolute path of state-runner
    ConfigPath string // absolute path of runner.json
    LogDir     string // absolute directory for stdout/stderr logs
    Path       string // PATH for the agent, so adapters find claude, codex, opencode, pi
    Home       string
}

// LaunchAgentPlist renders the property list for a per-user LaunchAgent that
// keeps `state-runner run --config <ConfigPath>` alive.
func LaunchAgentPlist(spec ServiceSpec) ([]byte, error)
```

Anforderungen an das Plist (XML, per `encoding/xml` oder sorgfältig per Template mit Escaping aller Werte):
- `Label` = `ServiceLabel`
- `ProgramArguments` = `[Executable, "run", "--config", ConfigPath]`
- `RunAtLoad` = true, `KeepAlive` = true, `ProcessType` = `Background`
- `ThrottleInterval` = 30
- `StandardOutPath` = `LogDir/runner.out.log`, `StandardErrorPath` = `LogDir/runner.err.log`
- `EnvironmentVariables` = `PATH`, `HOME`
- Validierung: alle Pfade absolut, sonst Fehler. `Path` nicht leer.

Tests zuerst:
- Erzeugtes Plist mit `plutil -lint` prüfbar: Im Test in eine Temp-Datei schreiben und, **nur wenn** `/usr/bin/plutil` existiert, `exec.Command("/usr/bin/plutil", "-lint", file).Run()` muss nil liefern.
- Enthält `<string>com.fabincrm.state.runner</string>` und die vier `ProgramArguments`.
- Pfad mit `&` oder `<` wird korrekt escaped (`&amp;`, `&lt;`).
- Relativer `Executable` ergibt Fehler.

Commit: `git commit -m "feat: render a launch agent plist for the state runner"`

## Task 2: `state-runner service install|uninstall|status`

**Dateien:** `internal/runner/service.go`, `cmd/state-runner/service.go`, `cmd/state-runner/service_test.go`, `cmd/state-runner/main.go`

**Interfaces:**

```go
// Launchctl abstracts the launchctl calls so tests never touch the real system.
type Launchctl interface {
    Bootstrap(domain string, plistPath string) error // launchctl bootstrap gui/<uid> <plist>
    Bootout(domain string, label string) error       // launchctl bootout gui/<uid>/<label>
    Print(domain string, label string) (string, error) // launchctl print gui/<uid>/<label>
}

type ServiceManager struct { /* launchAgentsDir string; launchctl Launchctl; uid int */ }

func NewServiceManager(launchAgentsDir string, launchctl Launchctl, uid int) *ServiceManager
func (manager *ServiceManager) Install(spec ServiceSpec) (plistPath string, err error)
func (manager *ServiceManager) Uninstall() error
func (manager *ServiceManager) Status() (running bool, detail string, err error)
```

Verhalten:
- `Install`: Plist schreiben (`0644`, Verzeichnis anlegen), vorher `Bootout` (Fehler "nicht geladen" ignorieren), dann `Bootstrap`. Idempotent.
- `Uninstall`: `Bootout`, Plist löschen. Fehlt beides, kein Fehler.
- `Status`: `Print`. Enthält die Ausgabe `state = running`, ist `running` wahr.
- Die echte Implementierung `execLaunchctl` ruft `/bin/launchctl` mit `exec.Command` und festen Argumenten auf (kein Shell-String).

Tests mit einem Fake-`Launchctl`, das Aufrufe protokolliert, und `t.TempDir()` als `launchAgentsDir`:
- Install schreibt die Datei und ruft Bootout vor Bootstrap.
- Zweimal Install ist ok.
- Uninstall entfernt die Datei.
- Status erkennt `state = running`.

CLI in `cmd/state-runner`:
- `main.go`: im Switch `case "service": return runService(args[1:], stdout, stderr)` und Usage-Text erweitern.
- `service.go`: `state-runner service install [--config PATH] [--path PATH]`, `uninstall`, `status`. Default `--path`: aktueller `PATH` der Shell, **plus** `~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`, doppelte Einträge entfernen. Executable über `os.Executable()` + `filepath.EvalSymlinks`. Log-Verzeichnis `~/Library/Logs/State Runner`. Vor `install` prüfen, dass die Config-Datei existiert (`state-runner pair` muss vorher gelaufen sein), sonst Fehler `runner is not paired yet, run state-runner pair first`.
- Auf Nicht-macOS (`runtime.GOOS != "darwin"`): `service` liefert Fehler `service management is only implemented for macOS; use systemd on Linux (see docs/runner-service.md)`.
- CLI-Tests nur für Flag-Fehler und die Nicht-darwin-Meldung (ohne echtes launchctl).

Commit: `git commit -m "feat: manage the state runner as a macos launch agent"`

## Task 3: Installationsskript für Agent-Werkzeuge

**Datei:** `scripts/install-agent-tools.sh` (ausführbar, `set -euo pipefail`)

Baut `statectl` und `state-runner` aus dem Repo und legt sie nach `~/.local/bin` (Verzeichnis anlegen). Version aus `git rev-parse --short HEAD` per `-ldflags "-X main.version=..."`. Gibt am Ende aus, ob `~/.local/bin` im `PATH` ist, und wenn nicht, die Zeile für `~/.zshrc` (nicht selbst eintragen).

Prüfung: `bash -n scripts/install-agent-tools.sh`. **Nicht** ausführen (würde in Fabians Home schreiben).

Commit: `git commit -m "chore: add a script that installs statectl and state-runner locally"`

## Task 4: App zeigt Runner-Status und kompletten Befehl

**Datei:** `ios/State/Sources/UI/SettingsView.swift` (nur Abschnitt "Runners" und `runnerPairingCommand`)

1. `runnerPairingCommand(code:)` liefert künftig einen Befehl, der Pairing **und** Dienst-Installation verbindet:
   ```text
   state-runner pair --server <server> --code <code> --name <name> --adapters claude-code,codex --work-root <root> && state-runner service install
   ```
   Lies die heutige Funktion: Übernimm deren vorhandene Parameter (Server-URL, Name, ggf. Work-Root) und hänge nur `&& state-runner service install` an. Erfinde keine Parameter, die `state-runner pair` nicht kennt (Flags in `cmd/state-runner/main.go` prüfen).
2. In jeder Runner-Zeile einen Online-Punkt ergänzen: grün "Online", wenn `lastSeenAt` weniger als 2 Minuten zurückliegt, sonst grau "Zuletzt gesehen <relative Zeit>". Die Logik als reine Funktion, z.B. `static func isOnline(lastSeenAt: Date?, now: Date) -> Bool` in einer Extension auf `Runner`, mit Unit-Test in `ios/StateTests/` (neue Datei `RunnerStatusTests.swift`; Datei außerhalb deines Bereichs, im Report nennen).
3. Deutsche Texte mit Umlauten, Keys im String-Katalog ergänzen (Datei außerhalb deines Bereichs, im Report nennen).

iPhone-Tests und Mac-Build (Befehle aus WP06).

Commit: `git commit -m "feat: show runner status and the full install command in settings"`

## Task 5: Doku

**Datei:** `docs/runner-service.md` (Englisch): Zweck, Installation (`scripts/install-agent-tools.sh`, Pairing-Code aus der App, kopierter Befehl), Status (`state-runner service status`, Logs unter `~/Library/Logs/State Runner`), Deinstallation, Linux-Hinweis mit einer Beispiel-systemd-User-Unit (`~/.config/systemd/user/state-runner.service`, `ExecStart=%h/.local/bin/state-runner run`, `Restart=always`), Sicherheitsgrenzen (nur ausgehend, eigene Credential, Work-Root).

Commit: `git commit -m "docs: explain installing the runner as a background service"`

## Task 6: Gesamtprüfung

```bash
gofmt -l ./cmd ./internal
go vet ./...
go test -race ./...
bash -n scripts/install-agent-tools.sh
cd ios && xcodegen generate && cd ..
xcodebuild -project ios/State.xcodeproj -scheme State -destination 'platform=iOS Simulator,name=iPhone 16 Pro,OS=18.5' -derivedDataPath build/DerivedData-wp10 CODE_SIGNING_ALLOWED=NO test 2>&1 | tail -5
xcodebuild -project ios/State.xcodeproj -scheme StateMac -destination 'platform=macOS' -derivedDataPath build/DerivedData-wp10 CODE_SIGNING_ALLOWED=NO build 2>&1 | tail -3
ls ~/Library/LaunchAgents | grep -i state || echo "NO_REAL_AGENT_INSTALLED"
```

Die letzte Zeile muss `NO_REAL_AGENT_INSTALLED` ausgeben.

Report und Draft-PR nach Master-Plan Abschnitt 4.4 und 4.5.
