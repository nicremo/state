# WP10 Report: Runner als LaunchAgent, gesteuert aus der App

**Status:** PARTIAL
**Branch:** wp/10-runner-service
**Basis:** `origin/main` (`fe74fde` WP02 Mac Server App), zweimal rebased am 23.09.2026
**Letzter Commit:** `a398d33` feat: show runner status and the full install command in settings (dieser Report folgt als eigener Commit)

## Ergebnis in drei Sätzen

Der Runner lässt sich jetzt als per-user LaunchAgent installieren, inspizieren und wieder entfernen: `internal/runner/service.go` rendert das Plist und kapselt `launchctl` hinter einem Interface, `cmd/state-runner/service.go` liefert die Unterbefehle `service install|uninstall|status`, und `scripts/install-agent-tools.sh` baut beide CLIs nach `~/.local/bin`. Die App zeigt pro Runner einen Online-Punkt (grün unter zwei Minuten, sonst grau mit relativer Zeit) und kopiert einen Befehl, der Pairing und Dienst-Installation in einem Schritt erledigt. Alles ist mit Tests belegt, nur der im Plan geforderte Mac-Build (`-scheme StateMac`) ist nicht ausführbar, weil WP06 mit dem macOS-Target noch nicht auf `main` ist.

## Erledigte Tasks

- [x] Task 1: LaunchAgent-Plist als reine Funktion, mit `plutil -lint`, Escaping- und Validierungstests
- [x] Task 2: `Launchctl`-Interface, `ServiceManager`, echter `ExecLaunchctl`, CLI `service install|uninstall|status`, Nicht-darwin-Meldung
- [x] Task 3: `scripts/install-agent-tools.sh`, `bash -n` grün, nicht ausgeführt
- [x] Task 4: Online-Status in der Runner-Zeile, vollständiger Installationsbefehl, 5 neue Unit-Tests, deutsche Katalog-Einträge
- [x] Task 5: `docs/runner-service.md` (Englisch) mit Installation, Status, Logs, Deinstallation, systemd-User-Unit, Sicherheitsgrenzen
- [ ] Task 6: Gesamtprüfung, bis auf den Mac-Build vollständig ausgeführt (siehe Prüfungen)

## Geänderte Dateien

| Datei | Änderung |
| --- | --- |
| `internal/runner/service.go` | Neu: `ServiceLabel`, `ServiceSpec`, `LaunchAgentPlist`, `Launchctl`, `ServiceManager`, `ExecLaunchctl`, Pfad-Helfer |
| `internal/runner/service_test.go` | Neu: Plist-Tests inklusive `plutil -lint` und Escaping, Fake-`Launchctl`, Install, Idempotenz, Uninstall, Status |
| `cmd/state-runner/service.go` | Neu: Unterbefehle `install`, `uninstall`, `status`, Plattform-Guard, Pfad-Default, Config-Prüfung |
| `cmd/state-runner/service_test.go` | Neu: Flag-Fehler, Subcommand-Usage, Plattform-Guard, Config-Prüfung, Pfad-Zusammenbau |
| `cmd/state-runner/main.go` | `case "service"` im Switch, Usage um `service` erweitert |
| `scripts/install-agent-tools.sh` | Neu: baut `statectl` und `state-runner` mit Commit-Hash als Version nach `~/.local/bin` |
| `ios/State/Sources/UI/SettingsView.swift` | Abschnitt Runners: Status-Zeile mit Punkt, Hilfsfunktion `runnerStatus(for:)`; `runnerPairingCommand` liefert Pairing plus `service install`; `extension Runner` mit `isOnline` |
| `ios/StateTests/RunnerStatusTests.swift` | Neu: 5 Tests für die Online-Logik |
| `docs/runner-service.md` | Neu: Zweck, Installation, Status und Logs, Neustart, Deinstallation, Linux/systemd, Sicherheitsgrenzen |

## Dateien außerhalb meines Bereichs

| Datei | Grund |
| --- | --- |
| `ios/StateTests/RunnerStatusTests.swift` | Im WP-Text selbst als außerhalb des Bereichs benannt und dort verlangt |
| `ios/State/Resources/Localizable.xcstrings` | Im WP-Text als außerhalb des Bereichs benannt: neue Keys `Online` und der Hinweis zum Arbeitsverzeichnis, deutsche Werte ergänzt |
| `ios/State.xcodeproj/project.pbxproj` | Von `xcodegen generate` neu erzeugt, damit die neue Testdatei im Testtarget liegt; Diff gegen die Basis nur die eine Dateireferenz |
| `docs/plans/2026-09-23-product-lineup/reports/WP10-report.md` | Dieser Report, Pflicht laut Arbeitsprotokoll 4.4 |

## Prüfungen

Alle Befehle liefen im Worktree `~/Desktop/state-worktrees/wp10-runner-service`. Go-Prüfungen und iOS-Tests liefen nach dem letzten Rebase auf `origin/main` (`fe74fde`), die Go-Prüfungen zusätzlich nach dem Merge von `#43` erneut. Der `ios/`-Tree-Hash ist vor und nach diesem Merge identisch (`4e8fb11`), die iOS-Ergebnisse gelten also unverändert.

| Befehl | Ergebnis |
| --- | --- |
| `gofmt -l ./cmd ./internal` | leer |
| `go vet ./...` | keine Ausgabe, also sauber |
| `go test -count=1 -race ./...` | grün, 15 Pakete, darunter `cmd/state-runner` und `internal/runner` |
| `bash -n scripts/install-agent-tools.sh` | grün, Skript bewusst nicht ausgeführt |
| `cd ios && xcodegen generate` | grün |
| `xcodebuild -scheme State -destination 'platform=iOS Simulator,name=iPhone 16 Pro,OS=18.5' test` | `** TEST SUCCEEDED **`, 46 Tests, 0 Fehler, 0 übersprungen, davon 5 neue aus `RunnerStatusTests` |
| `xcodebuild -scheme StateMac -destination 'platform=macOS' build` | im eigenen Branch nicht ausführbar (`The project named "State" does not contain a scheme named "StateMac"`), siehe Abweichungen; mit WP06 in einem Wegwerf-Worktree vorab integriert und dort `** BUILD SUCCEEDED **`, siehe Abschnitt "Mac-Build-Prüfung mit WP06" |
| `ls ~/Library/LaunchAgents \| grep -i state \|\| echo "NO_REAL_AGENT_INSTALLED"` | `NO_REAL_AGENT_INSTALLED` |
| `plutil -lint` auf das erzeugte Plist | läuft im Test `TestLaunchAgentPlistPassesPlutilLint` und ist grün (`/usr/bin/plutil` ist vorhanden, der Skip greift hier nicht) |

Kein echter LaunchAgent wurde installiert, `launchctl` wurde nur über das Fake im Test aufgerufen. Auf der Kommandozeile wurden ausschließlich die Fehlerpfade ohne Systemzugriff geprüft (`service` ohne Unterbefehl, unbekannter Unterbefehl, unbekanntes Flag, fehlende Config), jeweils mit erwarteter Meldung und Exit-Code 1.

Zusätzlich geprüft, weil `cmd/state-runner` auch auf Linux laufen soll:

| Befehl | Ergebnis |
| --- | --- |
| `GOOS=linux GOARCH=amd64 go build ./cmd/state-runner` | grün |
| `GOOS=linux GOARCH=amd64 go vet ./cmd/state-runner ./internal/runner` | grün |
| `go build -trimpath -ldflags "-X main.version=deadbee" -o /tmp/... ./cmd/state-runner && /tmp/... version` | gibt `deadbee` aus, die `-ldflags`-Zeile aus `scripts/install-agent-tools.sh` wirkt also |

## Mac-Build-Prüfung mit WP06 (Vorabintegration)

WP06 liegt inzwischen als Branch `wp/06-macos-client-target` (`30e407c`) vor, aber noch nicht auf `main`. Um die letzte offene Prüfung aus Task 6 trotzdem zu belegen, habe ich einen Wegwerf-Worktree `~/Desktop/state-worktrees/wp10-mac-check` erstellt, ihn auf WP06 gestellt und dort ausschließlich meine vier iOS-Änderungen eingespielt (`runnerStatus(for:)` statt der "Last seen"-Zeile, der Hinweis zum Arbeitsverzeichnis, `runnerPairingCommand` mit allen Pflicht-Flags plus `service install`, die `Runner`-Extension, die zwei String-Katalog-Einträge und `ios/StateTests/RunnerStatusTests.swift`). Danach:

| Befehl | Ergebnis |
| --- | --- |
| `cd ios && xcodegen generate` | grün, das StateMac-Scheme existiert dort |
| `xcodebuild -scheme StateMac -destination 'platform=macOS' -derivedDataPath build/DerivedData-maccheck CODE_SIGNING_ALLOWED=NO build` | `** BUILD SUCCEEDED **` |
| `xcodebuild -scheme State -destination 'platform=iOS Simulator,name=iPhone 16 Pro,OS=18.5' test` | 45 Tests grün, 1 Fehler: `StateScreenshots.testAppStoreScreenshots()` mit `Test crashed with signal kill` |

Der eine Fehler kommt nicht aus diesem WP. Gegenprobe: derselbe Testlauf auf WP06 allein, ohne eine Zeile WP10-Code, zeigt exakt dasselbe Bild (40 grün, derselbe `StateScreenshots`-Absturz). Auf dem WP10-Branch allein, gegen `main` ohne WP06, ist dieselbe Suite dagegen vollständig grün (46 Tests, 0 Fehler, inklusive StateUITests). Der Absturz gehört also zu WP06 oder zur lokalen Simulatorumgebung, nicht zu diesem Arbeitspaket.

Damit ist belegt: der Code dieses WP kompiliert für das macOS-Target, und er verträgt sich mit der Plattform-Abstraktion aus WP06. Der Wegwerf-Worktree wurde danach entfernt, es wurde nichts davon committet oder gepusht. Für den Koordinator heißt das: beim Merge von WP06 sind nur der Abschnitt Runners und die `Runner`-Extension in `SettingsView.swift` betroffen, dazu der String-Katalog und die von `xcodegen` erzeugte Projektdatei. Wichtig ist, beim Auflösen `Platform.copyToPasteboard(...)` von WP06 zu behalten, mein `runnerStatus(for:)` kommt eine Zeile darüber.

## Abweichungen vom Plan

1. **`origin/main` hat sich während der Arbeit dreimal bewegt.** Der Branch startete auf `ae6ab23`. Danach hat der Koordinator `#38` (Integration), `#42` (Dependencies), WP01 (`#39`), WP08 (`#41`), WP03 (`#35`) und WP05 (`#37`) gemergt (Stand `f9f511d`) und später WP04 (`#40`) und WP02 (`#36`) (Stand `fe74fde`). Ich habe beide Male rebased, damit der Draft-PR keine fremde Arbeit löscht. Der zweite Rebase lief konfliktfrei, weil WP02 und WP04 andere Dateien berühren. Zuletzt kam `#43` (APNs-Fehlerprotokollierung, Stand `63e0895`) über einen Merge-Commit `origin/main` in diesen Branch; dieser Merge hat ausschließlich `cmd/state-relay/**` und `internal/relay/**` berührt, meine Dateien nicht. WP06 ist weiterhin offen und bleibt die Voraussetzung dieses WP.
2. **Der erste Rebase hatte drei Konflikte**, alle im Swift-Teil, weil `main` inzwischen die integrierte `SettingsView.swift` und einen neu formatierten String-Katalog enthält: `ios/State/Sources/UI/SettingsView.swift`, `ios/State/Resources/Localizable.xcstrings`, `ios/State.xcodeproj/project.pbxproj`. Aufgelöst wurde, indem ich die Basis-Version genommen und meine Änderungen darauf neu angewendet habe (die vier Swift-Änderungen, die zwei Katalog-Einträge im Format der neuen Datei, `xcodegen generate` für die Projektdatei). Der Diff gegen `main` enthält danach genau die zwölf WP10-Dateien. Der zweite Rebase auf `fe74fde` war konfliktfrei; der `ios/`-Tree-Hash blieb dabei identisch (`4e8fb11`), die iOS-Prüfung gilt also für beide Stände unverändert.
3. **`runnerPairingCommand` hatte zu wenige Flags.** Die Funktion lieferte nur `--server` und `--code`, aber `state-runner pair` verlangt zusätzlich `--name` und `--work-root` und bricht sonst mit `state-runner pair requires --server, --code, --name and --work-root` ab. Der kopierte Befehl war also nie lauffähig. Ergänzt wurden `--name` (der eingegebene Runner-Name, Rückfall `mac-runner`), `--adapters claude-code,codex` und `--work-root "$HOME/Projects"` sowie der vom WP geforderte Zusatz `&& state-runner service install`. Alle Flags wurden gegen `cmd/state-runner/main.go` geprüft, es sind keine neuen erfunden. Der Hinweis unter dem Befehl nennt die anzupassenden Stellen.
4. **`Install` legt auch das Log-Verzeichnis an.** Der Plan verlangt es nicht, aber launchd kann `StandardOutPath` und `StandardErrorPath` nur schreiben, wenn das Verzeichnis existiert. Ein Test prüft das.
5. **`service status` auf einem nicht geladenen Agenten ist ein Fehler.** `Status()` gibt den `launchctl print`-Fehler weiter, die CLI beendet sich mit Exit-Code 1 und der Meldung `launch agent com.fabincrm.state.runner is not loaded`. Der Plan ließ das offen; für einen Operator ist ein sichtbarer Fehler beim Nachsehen besser als ein stilles "läuft nicht".
6. **Nicht-darwin-Prüfung als reine Funktion.** `checkServicePlatform(goos)` macht die geforderte Meldung ohne echtes `launchctl` und ohne `runtime.GOOS`-Trick testbar; `runService` ruft sie als Erstes mit `runtime.GOOS` auf.
7. **`isOnline` behandelt Zeitstempel aus der Zukunft als online.** Ein Heartbeat, der durch Uhrenversatz wenige Sekunden in der Zukunft liegt, darf den Punkt nicht grau machen. `nil` gilt als offline.
8. **Die `Runner`-Extension liegt am Ende von `SettingsView.swift`.** Das WP nennt als Beispiel eine Extension auf `Runner`, erlaubt mir aber nur diese eine Swift-Datei. Ein Umzug nach `Models.swift` ist eine Aufgabe für einen späteren WP.
9. **Die echte `launchctl`-Implementierung heißt `ExecLaunchctl`, nicht `execLaunchctl`.** Der Plan nennt den Typ `execLaunchctl`, aber `cmd/state-runner` ist ein eigenes Paket und kann einen unexportierten Typ aus `internal/runner` nicht konstruieren. `NewExecLaunchctl()` ist der Konstruktor, den die CLI benutzt; die Testbarkeit kommt über das `Launchctl`-Interface, nicht über die Sichtbarkeit des Typs.

## Offene Fragen und Risiken

1. **WP06 ist gemergt noch nicht auf `main`, liegt aber fertig auf `wp/06-macos-client-target` (`30e407c`).** Ohne den Merge gibt es in diesem Branch kein `StateMac`-Target. Die Vorabintegration im Wegwerf-Worktree (Abschnitt oben) zeigt, dass der Code dieses WP für macOS baut; für den Endnachweis muss der Koordinator nach dem Merge von WP06 `cd ios && xcodegen generate`, die iOS-Tests und den Mac-Build im WP10-Worktree erneut laufen lassen. Der Zusatz aus diesem WP ist plattformneutral: `runnerStatus(for:)` und die `Runner`-Extension nutzen nur SwiftUI (`HStack`, `Circle`, `Text`, `Color`) und Foundation, kein UIKit. Achtung: der WP06-Branch basiert auf dem Integrationsbranch vor `#38` und kollidiert deshalb mit `main` in vielen UI-Dateien und in `macos/**`; das sind WP06-gegen-`main`-Konflikte, nicht WP10-gegen-WP06.
2. **`--work-root "$HOME/Projects"` ist ein Vorschlag.** Der Befehl ist kopierfertig, aber der Pfad muss zu Fabians Checkout-Ordner passen. Falls die Projekte woanders liegen, ist die Zeichenkette in `runnerPairingCommand` die eine Stelle zum Anpassen.
3. **Der Agent erbt `PATH` und `HOME`, sonst nichts.** Adapter, die weitere Umgebungsvariablen brauchen (etwa ein Token im Environment), funktionieren im Agenten nicht. Das ist bewusst so und in `docs/runner-service.md` beschrieben; Erweiterungen gehören in die Runner- oder Adapter-Konfiguration, nicht in das Plist.
4. **Logrotation fehlt.** launchd rotiert nicht, die Dateien unter `~/Library/Logs/State Runner` wachsen unbegrenzt. Die Doku nennt das, ein `newsyslog`-Eintrag wäre ein eigenes kleines WP.
5. **Der Runner-Status in der App hängt an der Sync-Aktualität.** Der Punkt wird beim Rendern aus `lastSeenAt` berechnet; ohne neuen Sync bleibt ein tatsächlich laufender Runner grau. Das ist die im WP gewünschte reine Ableitung, eine laufende Uhr oder ein Timer wäre eine spätere Verfeinerung.
6. **WP06 lässt `StateScreenshots.testAppStoreScreenshots()` lokal abstürzen** (`Test crashed with signal kill`), sowohl mit als auch ohne WP10-Code. Das ist eine Beobachtung für die WP06-Session und den Koordinator, kein Befund dieses WP: auf dem WP10-Branch allein läuft die komplette Suite inklusive `StateUITests` grün. Mögliche Ursachen sind der Screenshot-Pfad `~/Library/Caches/tools.fastlane/screenshots/` oder Simulatorressourcen.

## Manuelle Schritte für Fabian oder den Koordinator

1. WP06 mergen, danach diesen Branch mit `main` abgleichen, `cd ios && xcodegen generate`, iOS-Tests und den Mac-Build (`-scheme StateMac`) erneut laufen lassen.
2. Auf einem echten Mac einmal durchspielen: `scripts/install-agent-tools.sh`, Pairing-Code in der App erzeugen, kopierten Befehl ausführen, `state-runner service status` prüfen, `launchctl print gui/$(id -u)/com.fabincrm.state.runner` ansehen. In dieser Session wurde das bewusst nicht getan.
3. Probelauf der Deinstallation (`state-runner service uninstall`) und Kontrolle, dass `~/Library/LaunchAgents/com.fabincrm.state.runner.plist` verschwindet.
