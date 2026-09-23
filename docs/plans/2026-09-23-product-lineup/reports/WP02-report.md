# WP02 Report: Mac Server App stabil machen

**Status:** DONE
**Branch:** wp/02-mac-server-app
**Letzter Commit:** 34609e1 docs: add WP02 report (davor 9fcdec2 docs: document mac server logs, restarts and troubleshooting)

**Hinweis zur PR-Basis:** Dieser Branch basiert auf dem Integrationsbranch `feat/local-mac-server-and-ios-refresh` (Commit `bc42fbe`), nicht auf `origin/main`, weil `macos/**` auf `origin/main` noch fehlt. Der Draft-PR gegen `main` zeigt deshalb zusätzlich die Commits des Integrationsbranches. Sobald der Koordinator Phase 1 gemergt hat, enthält der Diff nur noch die WP02 Commits.

## Ergebnis in drei Sätzen

Die Menüleisten-App schreibt die Serverausgaben jetzt in eine rotierende Datei unter `~/Library/Logs/State Server`, startet einen unerwartet beendeten Server mit exponentiellem Backoff bis 60 Sekunden neu und gibt nie wieder auf. Ruhezustand und Aufwachen gelten als normal, ein gesunder Server wird nach dem Aufwachen nicht mehr sinnlos beendet. Die entscheidbare Logik liegt testbar in der neuen Bibliothek `StateServerCore`, die UI zeigt letzten Exit-Code und Fehlstarts, und der Go-Server erneuert bei einer Namensänderung sein Zertifikat statt ein nicht passendes wiederzuverwenden.

## Diagnose

Alle Befehle am 23.09.2026, nur lesend. Die laufende App wurde nicht beendet und nicht verändert.

```bash
pgrep -fl StateServerMac
857 /Users/nicremo/Applications/State Server.app/Contents/MacOS/StateServerMac
```

```bash
pgrep -fl "state-server desktop"     # keine Ausgabe, rc=1
lsof -nP -iTCP:9847 -sTCP:LISTEN     # keine Ausgabe, rc=1
lsof -nP -iTCP:9848 -sTCP:LISTEN     # keine Ausgabe, rc=1
ls -la ~/Library/Logs/DiagnosticReports | grep -i state   # keine Treffer
scutil --get LocalHostName           # MacBook-Pro-von-Fabian-645
```

```bash
log show --last 2d --predicate 'process == "StateServerMac"' --style compact | tail -8
2026-09-22 23:09:06  ... CGSDisplayNotifyProc: got notification kCGSDisplayWillSleep
2026-09-23 01:28:38  ... CGSDisplayNotifyProc: got notification kCGSDisplayDidWake
2026-09-23 01:28:38  ... display system state seed 1970 -> 1970
2026-09-23 01:29:07  ... NSApplication._react(to:) dock
2026-09-23 02:00:08  ... CoreAnalytics: Received configuration update from daemon (change)
```

Der Prozess läuft, aber es gibt keine Zeile über einen Serverstart, einen Exit oder einen Neustart. Genau das war der Befund 1 aus dem WP: `standardError` zeigte auf `FileHandle.nullDevice`, jede Serverausgabe war verloren. Auch ein Absturzbericht fehlt.

Datenordner, nur Zeitstempel gelesen, nichts geändert:

| Datei | Letzte Änderung |
| --- | --- |
| `data.db` | 14.09. 12:24 |
| `auxiliary.db` | 14.09. 02:12 |
| `desktop-identity.pem` | 14.09. 02:11 |

App-Bundle `~/Applications/State Server.app` vom 14.09. 02:08, Prozess PID 857 läuft seit dem 14.09. Login Item laut `sfltool dumpbtm`: `com.fabincrm.state.server`, Zustand `enabled, allowed, notified`.

Testlauf des gebauten Servers aus dem Worktree auf eigenen Ports und mit temporärem Datenordner:

```bash
go build -o /tmp/wp02-state-server ./cmd/state-server
TMPDATA=$(mktemp -d)
( sleep 4; printf '{"action":"status"}\n'; sleep 2; printf '{"action":"stop"}\n' ) | \
  /tmp/wp02-state-server desktop --data "$TMPDATA" --host "$(scutil --get LocalHostName).local" \
  --https 127.0.0.1:19847 --local-http 127.0.0.1:19848
{"type":"status","server_url":"https://MacBook-Pro-von-Fabian-645.local:19847","local_url":"http://127.0.0.1:19848","fingerprint":"adaff0d5b0512901892a668a573625a6bd2557c6a74532398320e13d281ae683","devices":[],"version":"dev"}
exit 0
```

Der Desktop-Modus startet also grundsätzlich, drei Statuszeilen, sauberer Exit 0. Auffällig war die vollständig leere stderr Ausgabe, der Server meldete seinen Start nirgends.

### Vermutung zur Ursache

Der Ausfall ist mit hoher Wahrscheinlichkeit genau die Kombination aus Befund 2 und Befund 3 des WPs:

1. `tick()` beendete den Server, wenn 25 Sekunden keine Statuszeile ankam. Während der Ruhephase kommen keine Zeilen. Beim Aufwachen war `lastUpdated` Stunden alt, der erste Tick tötete damit den völlig gesunden Server.
2. Jeder so ausgelöste Neustart verbrauchte einen der drei erlaubten Versuche. Nach dem vierten Ruhezyklus setzte `didExit` `desiredRunning = false` und gab dauerhaft auf. Der App-Prozess lebt seitdem ohne Kindprozess weiter.
3. Da Serverausgaben ins Nichts gingen, war der Ausfall nicht diagnostizierbar. Die App zeigte nur "Server nicht erreichbar".

Der letzte Datenbank-Schreibvorgang am 14.09. 12:24 markiert damit den Zeitpunkt, ab dem der Server nicht mehr lief. Ein Zertifikatsproblem als Ursache ist unwahrscheinlich: das gespeicherte Zertifikat trägt bereits den aktuellen Namen `MacBook-Pro-von-Fabian-645.local` in `DNSNames`.

## Erledigte Tasks

- [x] Task 0: Diagnose des Ausfalls (nur lesend) und Testlauf auf eigenen Ports
- [x] Task 1: Testbare Kernbibliothek `StateServerCore` mit TDD (erst roter Test, dann Implementierung)
- [x] Task 2: `ServerController` nutzt Rotation-Log und Backoff, kein endgültiges Aufgeben mehr
- [x] Task 3: Ruhezustand und Aufwachen werden korrekt behandelt
- [x] Task 4: Diagnosebereich in der UI und Menüeintrag "Log anzeigen"
- [x] Task 5: Go-Seite geprüft, Hostname-Wechsel abgesichert, Startzeile auf stderr ergänzt
- [x] Task 6: Autostart und Troubleshooting dokumentiert
- [x] Task 7: Gesamtprüfung, Report und Draft-PR

## Geänderte Dateien

| Datei | Änderung |
| --- | --- |
| `Package.swift` | Neue Targets `StateServerCore` und `StateServerCoreTests`, `StateServerMac` hängt an `StateServerCore` |
| `macos/Core/RestartPolicy.swift` | Neu: Backoff ohne Aufgeben, gesunde Laufzeit ab 60 s setzt den Zähler zurück |
| `macos/Core/LogFile.swift` | Neu: größenrotiertes Logfile, Rechte 0700 für den Ordner und 0600 für die Datei |
| `macos/CoreTests/RestartPolicyTests.swift` | Neu: 3 Tests für Backoff, gesunden Lauf und Reset |
| `macos/CoreTests/LogFileTests.swift` | Neu: 2 Tests für Rotation und Dateirechte |
| `macos/Sources/ServerController.swift` | stderr geht in eine Pipe und von dort ins Logfile, Backoff statt Neustartbudget, Schlaf- und Aufwachbehandlung, `revealLog()`, `lastExitCode`, `consecutiveFailures` |
| `macos/Sources/ServerView.swift` | Neuer Bereich "Diagnose" mit Exit-Code, Fehlstarts und Log-Knopf |
| `macos/Sources/StateServerApp.swift` | Menüeintrag "Log anzeigen" |
| `cmd/state-server/desktop.go` | Zertifikat wird bei abweichendem Hostnamen ersetzt, Startzeile mit Adresse und Version auf stderr |
| `cmd/state-server/desktop_test.go` | 2 neue Tests: Namenswechsel erneuert das Zertifikat, Startzeile enthält Adresse und Version |
| `macos/README.md` | Abschnitt `## Troubleshooting`, neues Neustart- und Ruheverhalten, Hinweis zum Zertifikatswechsel |

## Dateien außerhalb meines Bereichs

- `docs/plans/2026-09-23-product-lineup/reports/WP02-report.md`: Der Pfad ist im Master-Plan Abschnitt 4.4 vorgegeben. Die Plandokumente selbst liegen im Basis-Commit noch nicht in Git, deshalb enthält dieser Branch nur den Report und nicht die Plandateien.

Sonst keine Dateien außerhalb des zugewiesenen Bereichs geändert.

## Prüfungen

| Befehl | Ergebnis |
| --- | --- |
| `swift test --filter StateServerCoreTests` vor der Implementierung | rot, `cannot find 'LogFile' in scope`, `cannot find 'RestartPolicy' in scope` |
| `swift test` | grün, 7 Tests in 3 Suites |
| `swift build` nach jedem Swift-Task | grün |
| `go test -race -run TestDesktopCertificateFollowsHostnameChange\|TestDesktopLogsAddressAndVersion` vor dem Fix | rot, `stored identity kept the old hostname: [alt.local localhost]` und `stderr log lacks address or version: ""` |
| `go test -race ./cmd/state-server/` | grün |
| `gofmt -l ./cmd ./internal` | leer |
| `go vet ./...` | grün |
| `go test -race ./...` | grün, 15 Pakete |
| `bash macos/build.sh` | grün, `dist/State Server.app` erzeugt |
| `codesign --verify --strict "dist/State Server.app"` | `SIGNED_OK`, Signatur adhoc |
| `python3 macos/verify-local.py` | grün, echter TLS Aufbau, Ablehnung bei falschem Zertifikat und falscher Herkunft, sauberer Shutdown. Das Skript nutzt einen temporären Datenordner und die Ports 0, also keine Kollision mit 9847 und 9848 |
| Gebauter Server auf eigenen Ports, stderr geprüft | `{"level":"INFO","msg":"state-server desktop listening","address":"https://test.local:57330","local_address":"http://127.0.0.1:57331","version":"9fcdec2"}` |

Die gebaute App wurde nicht nach `~/Applications` kopiert, die laufende App wurde nicht beendet.

## Abweichungen vom Plan

1. **Worktree-Basis:** Der Plan legt `origin/main` als Basis fest. Auf `origin/main` (ae6ab23) existieren `macos/**` und `cmd/state-server/desktop.go` noch nicht, weil der Koordinator Phase 1 (Integration von `feat/local-mac-server-and-ios-refresh`) noch nicht gemergt hat. Der Branch wurde deshalb von `bc42fbe` angelegt, dem HEAD des Integrationsbranches, also genau dem Stand, den das WP beschreibt.
2. **Task 2, Schritt 4:** Zusätzlich zu `restarts = 0` in `start()` entfällt in `receive()` die Zeile `if Date().timeIntervalSince(startingAt) > 60 { restarts = 0 }`. Diese Aufgabe übernimmt jetzt `RestartPolicy.delayAfterExit`, das eine Laufzeit ab `healthyAfter` selbst als gesund wertet.
3. **Task 2, Schritt 5:** Der Lesekreis für stderr schreibt vollständige Zeilen ins Log und schiebt einen unvollständigen Rest ab 64 KB trotzdem raus, damit auch eine sehr lange Zeile nicht beliebig gepuffert wird. Beim Schließen der Pipe wird der Rest geschrieben.
4. **Task 3, `tick()`:** Zusätzlich zu `if sleeping { return }` merkt sich `tick()` den Zeitpunkt des letzten Durchlaufs. Ist die Lücke größer als 15 Sekunden, war der Mac zwischenzeitlich im Ruhezustand, auch wenn die Aufwachbenachrichtigung noch nicht zugestellt wurde. Ohne diese Prüfung bleibt ein Zeitfenster, in dem der erste Timer nach dem Aufwachen vor `didWakeNotification` läuft und den gesunden Server doch noch tötet. Die normale Prüfung greift ab dem nächsten Durchlauf wieder.
5. **Task 5, Punkt 4:** Für die geforderte Startzeile auf stderr wurde zusätzlich ein Test ergänzt, damit die Zusage prüfbar bleibt und nicht nur im Code steht. Der Plan verlangte hier nur "sicherstellen".
6. **Task 6:** Der neue Abschnitt nutzt die im Programm sichtbaren Beschriftungen "Bei der Anmeldung starten", "Diagnose", "Log im Finder zeigen" und "Log anzeigen". Die Prüfung mit `sfltool dumpbtm | grep -i state` wurde auf diesem Mac ausgeführt und zeigt den Eintrag `com.fabincrm.state.server`.

## Offene Fragen und Risiken

- **Der Fix wirkt erst nach einem Neustart der App.** Der laufende Prozess PID 857 hat `desiredRunning = false` und startet von sich aus keinen Server mehr. Das ist ein manueller Schritt für den Koordinator.
- **Kein Rollback-Risiko für den Datenordner:** Alle Änderungen betreffen Prozesssteuerung und Logging. Der Datenordner und die Datenbank werden nicht verändert.
- **Zertifikat bei Namensänderung:** Ändert sich der Bonjour-Name des Macs, erzeugt der Server jetzt ein neues Zertifikat. Der Fingerprint ändert sich, gekoppelte iPhones müssen einmal neu koppeln. Auf Fabians Mac ist das aktuell nicht nötig, das gespeicherte Zertifikat passt zum aktuellen Namen.
- **Zwei unabhängige Schutznetze:** Selbst wenn ein Tick den Server fälschlich beendet, startet ihn der Backoff neu. Der Ausfall vom 14.09. kann sich damit nicht wiederholen.
- **Logwachstum:** Pro Datei 5 MB, drei rotierte Dateien plus die aktuelle, also höchstens rund 20 MB. Der Desktop-Server schreibt nur Ereignisse, keine Anfrageinhalte.

## Manuelle Schritte für Fabian oder den Koordinator

1. Die neue App bauen und nach `~/Applications/State Server.app` kopieren, danach die laufende Instanz (PID 857) beenden und neu starten. Vorher lohnt ein Blick auf `~/Library/Logs/State Server`, der Ordner entsteht mit dem ersten Start.
2. Nach dem Neustart prüfen:
   ```bash
   lsof -nP -iTCP:9847 -sTCP:LISTEN
   lsof -nP -iTCP:9848 -sTCP:LISTEN
   tail -5 ~/Library/Logs/State\ Server/server.log
   ```
3. Im Fenster unter "Diagnose" prüfen, dass "Fehlstarts in Folge" bei 0 steht und "Letzter Exit-Code" auf "keiner" oder einen alten Wert zeigt. Der Knopf "Log im Finder zeigen" muss `~/Library/Logs/State Server/server.log` markieren.
4. Autostart ist bereits registriert (`com.fabincrm.state.server`), es ist keine weitere Aktion nötig. Nach dem Austausch der App liest macOS das Login Item aus dem neuen Bundle.
5. Kein erneutes Koppeln des iPhones nötig, solange der Mac-Name unverändert bleibt.
