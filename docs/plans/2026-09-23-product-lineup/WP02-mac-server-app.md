# WP02: Mac Server App stabil machen

**Welle:** 1 · **Slug:** `mac-server-app` · **Branch:** `wp/02-mac-server-app`
**Besitzt:** `macos/**`, `Package.swift`, `cmd/state-server/desktop.go`, `cmd/state-server/desktop_test.go`, neu `macos/Core/**`, neu `macos/CoreTests/**`
**Geschätzter Umfang:** mittel (Swift + etwas Go)

## Goal-Prompt (so an die Worker-Session geben)

> Lies `docs/plans/2026-09-23-product-lineup/README.md` vollständig und halte dich an das Arbeitsprotokoll in Abschnitt 4. Setze danach `docs/plans/2026-09-23-product-lineup/WP02-mac-server-app.md` Task für Task um. Arbeite nur in deinem eigenen Worktree. Die laufende `State Server.app` von Fabian beendest oder änderst du nicht. Schließe mit Report und Draft-PR ab.

## Ausgangslage

Die Menüleisten-App `macos/` startet `state-server desktop` als Kindprozess und steuert ihn über stdin/stdout mit JSON-Zeilen. Sie läuft auf Fabians Mac seit dem 14.09., **aber der Server lauscht nicht mehr** (Ports 9847 und 9848 zu, kein Kindprozess). Die letzte Datenbank-Änderung ist vom 14.09. 12:24.

Befunde aus `macos/Sources/ServerController.swift`:

1. `child.standardError = FileHandle.nullDevice`: Server-Logs landen nirgends. Ein Absturz ist nicht diagnostizierbar.
2. `didExit`: Nach mehr als 3 Neustarts setzt die App `desiredRunning = false` und **gibt für immer auf**, bis jemand manuell startet.
3. `tick()`: Kommt 25 Sekunden lang keine Status-Zeile, wird der Server beendet. Nach dem Aufwachen aus dem Ruhezustand ist `lastUpdated` alt, der gesunde Server wird also sinnlos getötet. Mehrere Ruhezustände hintereinander können das Neustart-Budget aufbrauchen.
4. Der Hostname kommt aus `scutil --get LocalHostName`. Ändert sich der Name, passt das Zertifikat nicht mehr (das behandelt `desktopCertificate` in `desktop.go`, bitte prüfen).

Die Swift-Logik steckt im Executable-Target und ist deshalb nicht testbar. Diese WP zieht die entscheidbare Logik in eine testbare Bibliothek.

## Ziel

1. Server-Logs landen in einer rotierenden Datei.
2. Neustarts folgen einem Backoff ohne endgültiges Aufgeben.
3. Ruhezustand und Aufwachen werden korrekt behandelt.
4. Die UI zeigt eine Diagnose (letzter Exit-Code, Log öffnen).
5. Autostart beim Login ist dokumentiert und prüfbar.

## Pflichtlektüre

`macos/README.md`, `macos/Sources/*.swift`, `macos/build.sh`, `Package.swift`, `cmd/state-server/desktop.go`, `cmd/state-server/desktop_test.go`, `docs/LOCAL_MAC_CONNECTION.md`.

## Task 0: Diagnose des aktuellen Ausfalls (nur lesen, nichts ändern)

Führe diese Befehle aus und schreibe die Ausgaben (gekürzt) in den Report unter "Diagnose":

```bash
pgrep -fl StateServerMac
pgrep -fl "state-server desktop"
lsof -nP -iTCP:9847 -sTCP:LISTEN
lsof -nP -iTCP:9848 -sTCP:LISTEN
log show --last 2d --predicate 'process == "StateServerMac"' --style compact | tail -50
ls -la ~/Library/Logs/DiagnosticReports | grep -i state | tail -5
scutil --get LocalHostName
```

Starte danach den **gebauten** Server aus deinem Worktree auf **anderen Ports** und mit einem **temporären** Datenordner, um zu sehen, ob `desktop` grundsätzlich startet:

```bash
go build -o /tmp/wp02-state-server ./cmd/state-server
TMPDATA=$(mktemp -d)
( sleep 4; printf '{"action":"status"}\n'; sleep 2; printf '{"action":"stop"}\n' ) | \
  /tmp/wp02-state-server desktop --data "$TMPDATA" --host "$(scutil --get LocalHostName).local" \
  --https 127.0.0.1:19847 --local-http 127.0.0.1:19848
echo "exit $?"
```

Erwartet: eine JSON-Statuszeile mit `"type"` und `"server_url"`, danach Exit 0. Notiere das Ergebnis. **Niemals** den echten Datenordner `~/Library/Application Support/State Server` verwenden.

## Task 1: Testbare Kernbibliothek `StateServerCore`

**Dateien:**
- Modify: `Package.swift`
- Create: `macos/Core/RestartPolicy.swift`
- Create: `macos/Core/LogFile.swift`
- Create: `macos/CoreTests/RestartPolicyTests.swift`
- Create: `macos/CoreTests/LogFileTests.swift`

**Interfaces (Produces):**

```swift
public struct RestartPolicy: Sendable {
    public init(baseDelay: Double = 2, maxDelay: Double = 60, healthyAfter: Double = 60)
    /// Records an unexpected exit at `now` and returns the delay in seconds before the next start.
    public mutating func delayAfterExit(at now: Date, startedAt: Date) -> Double
    /// Number of consecutive failed starts since the last healthy period.
    public private(set) var consecutiveFailures: Int
    public mutating func reset()
}

public struct LogFile: Sendable {
    public init(directory: URL, name: String = "server.log", maxBytes: Int = 5 * 1_048_576, keep: Int = 3)
    public var url: URL { get }
    /// Appends bytes, rotating to name.1 ... name.<keep> when maxBytes is exceeded.
    public func append(_ data: Data) throws
}
```

Regeln für `RestartPolicy`:
- Lief der Prozess mindestens `healthyAfter` Sekunden (`now - startedAt >= healthyAfter`), wird `consecutiveFailures` auf 1 gesetzt und `baseDelay` zurückgegeben.
- Sonst `consecutiveFailures += 1`, Verzögerung `min(maxDelay, baseDelay * 2^(consecutiveFailures - 1))`.
- Es gibt **kein** Aufgeben. Die UI zeigt ab 3 Fehlern eine Warnung, startet aber weiter neu.

**Step 1: Package.swift erweitern.** Neue Targets ergänzen, bestehende unverändert lassen:

```swift
.target(name: "StateServerCore", path: "macos/Core"),
.testTarget(name: "StateServerCoreTests", dependencies: ["StateServerCore"], path: "macos/CoreTests"),
```

und `StateServerMac` bekommt `dependencies: ["StateLocalTransport", "StateServerCore"]`.

**Step 2: Fehlschlagende Tests schreiben** (`macos/CoreTests/RestartPolicyTests.swift`):

```swift
import Foundation
import Testing
@testable import StateServerCore

struct RestartPolicyTests {
    @Test func backsOffExponentiallyUpToTheCap() {
        var policy = RestartPolicy(baseDelay: 2, maxDelay: 60, healthyAfter: 60)
        let start = Date(timeIntervalSince1970: 1_000)
        let delays = (0..<7).map { _ in policy.delayAfterExit(at: start.addingTimeInterval(1), startedAt: start) }
        #expect(delays == [2, 4, 8, 16, 32, 60, 60])
        #expect(policy.consecutiveFailures == 7)
    }

    @Test func aHealthyRunResetsTheBackoff() {
        var policy = RestartPolicy(baseDelay: 2, maxDelay: 60, healthyAfter: 60)
        let start = Date(timeIntervalSince1970: 1_000)
        _ = policy.delayAfterExit(at: start.addingTimeInterval(1), startedAt: start)
        _ = policy.delayAfterExit(at: start.addingTimeInterval(1), startedAt: start)
        let delay = policy.delayAfterExit(at: start.addingTimeInterval(120), startedAt: start)
        #expect(delay == 2)
        #expect(policy.consecutiveFailures == 1)
    }

    @Test func resetClearsFailures() {
        var policy = RestartPolicy()
        let start = Date()
        _ = policy.delayAfterExit(at: start, startedAt: start)
        policy.reset()
        #expect(policy.consecutiveFailures == 0)
    }
}
```

`macos/CoreTests/LogFileTests.swift`:

```swift
import Foundation
import Testing
@testable import StateServerCore

struct LogFileTests {
    private func temporaryDirectory() throws -> URL {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
        return url
    }

    @Test func appendsAndRotates() throws {
        let directory = try temporaryDirectory()
        let log = LogFile(directory: directory, name: "server.log", maxBytes: 10, keep: 2)
        try log.append(Data("12345678\n".utf8))
        try log.append(Data("abcdefgh\n".utf8))
        try log.append(Data("ABCDEFGH\n".utf8))
        let current = try String(contentsOf: log.url, encoding: .utf8)
        let first = try String(contentsOf: directory.appendingPathComponent("server.log.1"), encoding: .utf8)
        let second = try String(contentsOf: directory.appendingPathComponent("server.log.2"), encoding: .utf8)
        #expect(current == "ABCDEFGH\n")
        #expect(first == "abcdefgh\n")
        #expect(second == "12345678\n")
    }

    @Test func createsTheFileWithOwnerOnlyPermissions() throws {
        let directory = try temporaryDirectory()
        let log = LogFile(directory: directory)
        try log.append(Data("x\n".utf8))
        let attributes = try FileManager.default.attributesOfItem(atPath: log.url.path)
        #expect((attributes[.posixPermissions] as? NSNumber)?.intValue == 0o600)
    }
}
```

**Step 3:** `swift test --filter StateServerCoreTests`. Erwartet: FAIL, weil die Typen fehlen.

**Step 4: Implementieren.**

`macos/Core/RestartPolicy.swift`:

```swift
import Foundation

/// Decides how long the menu bar app waits before restarting a server
/// process that exited unexpectedly. It never gives up: a Mac server that
/// silently stays down is worse than one that keeps retrying slowly.
public struct RestartPolicy: Sendable {
    public private(set) var consecutiveFailures = 0
    private let baseDelay: Double
    private let maxDelay: Double
    private let healthyAfter: Double

    public init(baseDelay: Double = 2, maxDelay: Double = 60, healthyAfter: Double = 60) {
        self.baseDelay = baseDelay
        self.maxDelay = maxDelay
        self.healthyAfter = healthyAfter
    }

    public mutating func delayAfterExit(at now: Date, startedAt: Date) -> Double {
        if now.timeIntervalSince(startedAt) >= healthyAfter {
            consecutiveFailures = 1
        } else {
            consecutiveFailures += 1
        }
        let exponent = Double(max(consecutiveFailures - 1, 0))
        return min(maxDelay, baseDelay * pow(2, exponent))
    }

    public mutating func reset() {
        consecutiveFailures = 0
    }
}
```

`macos/Core/LogFile.swift`:

```swift
import Foundation

/// A small size-rotated log file for the server's structured stderr. The
/// server never logs request bodies, credentials or pairing codes, so its
/// stderr is safe to keep; the file is still readable by the owner only.
public struct LogFile: Sendable {
    public let url: URL
    private let directory: URL
    private let name: String
    private let maxBytes: Int
    private let keep: Int

    public init(directory: URL, name: String = "server.log", maxBytes: Int = 5 * 1_048_576, keep: Int = 3) {
        self.directory = directory
        self.name = name
        self.maxBytes = maxBytes
        self.keep = keep
        self.url = directory.appendingPathComponent(name)
    }

    public func append(_ data: Data) throws {
        let manager = FileManager.default
        try manager.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let currentSize = (try? manager.attributesOfItem(atPath: url.path)[.size] as? NSNumber)?.intValue ?? 0
        if currentSize > 0, currentSize + data.count > maxBytes {
            try rotate()
        }
        if !manager.fileExists(atPath: url.path) {
            manager.createFile(atPath: url.path, contents: nil, attributes: [.posixPermissions: 0o600])
        }
        let handle = try FileHandle(forWritingTo: url)
        defer { try? handle.close() }
        try handle.seekToEnd()
        try handle.write(contentsOf: data)
    }

    private func rotate() throws {
        let manager = FileManager.default
        let oldest = directory.appendingPathComponent("\(name).\(keep)")
        if manager.fileExists(atPath: oldest.path) { try manager.removeItem(at: oldest) }
        if keep > 1 {
            for index in stride(from: keep - 1, through: 1, by: -1) {
                let source = directory.appendingPathComponent("\(name).\(index)")
                let target = directory.appendingPathComponent("\(name).\(index + 1)")
                if manager.fileExists(atPath: source.path) { try manager.moveItem(at: source, to: target) }
            }
        }
        try manager.moveItem(at: url, to: directory.appendingPathComponent("\(name).1"))
    }
}
```

**Step 5:** `swift test`. Erwartet: alle Tests grün (auch die bestehenden `LocalServerTrustTests`).

**Step 6: Commit**

```bash
git add Package.swift macos/Core macos/CoreTests
git commit -m "feat: add testable restart policy and rotating log for the mac server"
```

## Task 2: ServerController nutzt Log und Backoff

**Datei:** `macos/Sources/ServerController.swift`

Änderungen (Deutsche UI-Texte beibehalten, Umlaute korrekt, keine Gedankenstriche):

1. `import StateServerCore` ergänzen.
2. Neue Properties:
   ```swift
   private var restartPolicy = RestartPolicy()
   private(set) var lastExitCode: Int32?
   let logFile = LogFile(directory: FileManager.default.urls(for: .libraryDirectory, in: .userDomainMask)[0]
       .appendingPathComponent("Logs/State Server", isDirectory: true))
   ```
3. `restarts` und die Logik `restarts <= 3` **entfernen**. In `didExit`:
   ```swift
   lastExitCode = code
   let delay = restartPolicy.delayAfterExit(at: Date(), startedAt: startingAt)
   phase = .starting
   message = restartPolicy.consecutiveFailures >= 3
       ? "Der Server startet wiederholt nicht (Exit \(code)). Nächster Versuch in \(Int(delay)) s. Details im Log."
       : "Server wird neu gestartet."
   restartTask = Task { [weak self] in
       try? await Task.sleep(for: .seconds(delay))
       guard !Task.isCancelled else { return }
       self?.start()
   }
   ```
4. In `start()`: `if !desiredRunning { restartPolicy.reset() }` statt `restarts = 0`.
5. `child.standardError` auf eine eigene `Pipe` setzen. Deren Ausgabe in einem `Task.detached` zeilenweise lesen und mit `try? logFile.append(chunk)` schreiben (gleiche Schleife wie für stdout, Obergrenze pro Chunk 64 KB). Den Kommentar `// Do not persist request bodies...` ersetzen durch: `// The server logs structured events only, never bodies or secrets.`
6. Öffentliche Funktion für die UI:
   ```swift
   func revealLog() {
       NSWorkspace.shared.activateFileViewerSelecting([logFile.url])
   }
   ```

Build prüfen: `swift build`. Erwartet: ohne Fehler.

**Commit:** `git commit -am "fix: keep restarting the mac server with backoff and persist its log"`

## Task 3: Ruhezustand und Aufwachen

**Datei:** `macos/Sources/ServerController.swift`

1. In `launch()` nach `start()` Beobachter registrieren:
   ```swift
   let center = NSWorkspace.shared.notificationCenter
   center.addObserver(forName: NSWorkspace.willSleepNotification, object: nil, queue: .main) { [weak self] _ in
       MainActor.assumeIsolated { self?.sleeping = true }
   }
   center.addObserver(forName: NSWorkspace.didWakeNotification, object: nil, queue: .main) { [weak self] _ in
       MainActor.assumeIsolated { self?.didWake() }
   }
   ```
2. Neue Property `private var sleeping = false` und Funktion:
   ```swift
   private func didWake() {
       sleeping = false
       // A sleeping Mac sends no status lines; that silence is not a hang.
       lastUpdated = Date()
       if desiredRunning, process == nil { start() }
       send(action: "status")
   }
   ```
3. In `tick()` als erste Zeile nach dem `guard`: `if sleeping { return }`.

Build: `swift build`.

**Commit:** `git commit -am "fix: treat sleep and wake as normal for the mac server watchdog"`

## Task 4: Diagnose in der UI

**Datei:** `macos/Sources/ServerView.swift` (vorher lesen und den vorhandenen Stil übernehmen)

Ergänze einen Bereich "Diagnose" (unterhalb der vorhandenen Statusanzeige):
- Zeile "Letzter Exit-Code": `controller.lastExitCode.map(String.init) ?? "keiner"`
- Zeile "Fehlstarts in Folge": Anzahl aus `RestartPolicy.consecutiveFailures`. Dafür in `ServerController` eine berechnete Property `var consecutiveFailures: Int { restartPolicy.consecutiveFailures }` ergänzen.
- Button "Log im Finder zeigen" ruft `controller.revealLog()` auf.

Im Menü (`StateServerApp.swift`, `MenuContent`) einen Eintrag `Button("Log anzeigen") { controller.revealLog() }` vor dem letzten `Divider()` ergänzen.

Build: `swift build`.

**Commit:** `git commit -am "feat: show restart diagnostics and the server log in the mac app"`

## Task 5: Go-Seite prüfen, Hostname-Wechsel absichern

**Dateien:** `cmd/state-server/desktop.go`, `cmd/state-server/desktop_test.go`

1. Lies `desktopCertificate` in `desktop.go`. Prüfe, ob ein gespeichertes Zertifikat für einen **anderen** Hostnamen neu erzeugt oder wiederverwendet wird.
2. Falls es wiederverwendet wird, obwohl der Hostname nicht passt: Schreibe zuerst einen Test in `desktop_test.go`, der `desktopCertificate(path, "alt.local")` und danach `desktopCertificate(path, "neu.local")` aufruft und erwartet, dass das Zertifikat den neuen Namen in `DNSNames` enthält. Dann minimal fixen. Beachte: Ein neues Zertifikat ändert den Fingerprint, gekoppelte iPhones müssen dann neu koppeln. Das in `macos/README.md` im Abschnitt "Local connections" in einem Satz dokumentieren.
3. Falls es schon korrekt ist: nichts ändern, im Report festhalten.
4. Stelle sicher, dass beim Start im Desktop-Modus eine Log-Zeile mit Adresse und Version auf stderr geschrieben wird (wie in `runServe`: `logger.Info("state-server listening", ...)`). Falls sie fehlt, ergänzen.

Prüfen: `go test -race ./cmd/state-server/`.

**Commit:** `git commit -am "fix: ..."` (passend zur tatsächlichen Änderung) oder kein Commit, wenn nichts zu tun war.

## Task 6: Autostart und Doku

**Datei:** `macos/README.md`

Ergänze einen Abschnitt `## Troubleshooting` mit:
- Log-Ort: `~/Library/Logs/State Server/server.log` (rotiert, 3 Dateien à 5 MB).
- Neustart-Verhalten: exponentieller Backoff bis 60 s, gibt nie auf.
- Ruhezustand: Der Server pausiert mit dem Mac, nach dem Aufwachen wird der Status neu abgefragt.
- Autostart: Schalter "Bei der Anmeldung starten" registriert die App über `SMAppService.mainApp`. Prüfen mit `sfltool dumpbtm | grep -i state` (Ausgabe kann Admin-Rechte verlangen).
- Ports prüfen: `lsof -nP -iTCP:9847 -sTCP:LISTEN`.

Im Abschnitt `## Behavior` die veraltete Aussage "Retries an unexpected process exit up to three times" ersetzen durch die neue Backoff-Beschreibung.

**Commit:** `git commit -am "docs: document mac server logs, restarts and troubleshooting"`

## Task 7: Gesamtprüfung

```bash
swift test
go test -race ./cmd/state-server/
bash macos/build.sh
codesign --verify --strict "dist/State Server.app" && echo SIGNED_OK
```

`python3 macos/verify-local.py` ebenfalls ausführen, **aber nur, wenn das Skript einen temporären Datenordner und eigene Ports nutzt.** Lies das Skript vorher. Nutzt es feste Ports 9847/9848, dann nicht ausführen und im Report vermerken.

**Nicht** die gebaute App nach `~/Applications` kopieren und **nicht** die laufende App beenden. Die Installation macht der Koordinator.

Report und Draft-PR nach Master-Plan Abschnitt 4.4 und 4.5. Im Report unbedingt die Diagnose aus Task 0 und eine Vermutung zur Ursache des Ausfalls.
