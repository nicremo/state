import AppKit
import Foundation
import Observation
import ServiceManagement
import StateLocalTransport
import StateServerCore

struct DesktopDevice: Decodable, Identifiable {
    struct Actor: Decodable { let id: String; let displayName: String; let deviceName: String }
    let actor: Actor
    let lastUsedAt: Date?
    var id: String { actor.id }
}

struct DesktopPairing: Decodable {
    let url: String
    let code: String
    let expiresAt: Date
    let harness: String?
}

struct DesktopStatus: Decodable {
    let serverURL: String
    let localURL: String
    let fingerprint: String
    let devices: [DesktopDevice]
    let version: String
    let pairing: DesktopPairing?
    let error: String?

    enum CodingKeys: String, CodingKey {
        case serverURL = "server_url", localURL = "local_url", fingerprint, devices, version, pairing, error
    }
}

@MainActor
@Observable
final class ServerController {
    enum Phase { case stopped, starting, running, stopping, failed }
    var phase: Phase = .stopped
    var status: DesktopStatus?
    var message: String?
    var lastUpdated: Date?
    var loginEnabled = SMAppService.mainApp.status == .enabled
    var loginNeedsApproval = SMAppService.mainApp.status == .requiresApproval
    var pairingVisible = false
    var pairingKind = ""
    private(set) var lastExitCode: Int32?
    let logFile = LogFile(directory: FileManager.default.urls(for: .libraryDirectory, in: .userDomainMask)[0]
        .appendingPathComponent("Logs/State Server", isDirectory: true))
    private var process: Process?
    private var input: Pipe?
    private var output: Pipe?
    private var errorPipe: Pipe?
    private var monitor: Task<Void, Never>?
    private var restartTask: Task<Void, Never>?
    private var restartPolicy = RestartPolicy()
    private var desiredRunning = false
    private var sleeping = false
    private var lastTick = Date()
    private var startingAt = Date()
    private var quitting = false
    private var launched = false

    var dataDirectory: URL {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("State Server", isDirectory: true)
    }

    var label: String {
        switch phase {
        case .stopped: "Server gestoppt"
        case .starting: "Server startet"
        case .running: "Lokal verbunden"
        case .stopping: "Server stoppt"
        case .failed: "Server nicht erreichbar"
        }
    }

    var consecutiveFailures: Int { restartPolicy.consecutiveFailures }

    func launch() {
        guard !launched else { return }
        launched = true
        start()
        let center = NSWorkspace.shared.notificationCenter
        center.addObserver(forName: NSWorkspace.willSleepNotification, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.sleeping = true }
        }
        center.addObserver(forName: NSWorkspace.didWakeNotification, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.didWake() }
        }
    }

    func start() {
        guard process == nil else { return }
        if !desiredRunning { restartPolicy.reset() }
        desiredRunning = true
        restartTask?.cancel()
        phase = .starting
        message = nil
        status = nil
        startingAt = Date()
        lastUpdated = nil
        lastTick = Date()
        guard let executable = Bundle.main.url(forResource: "state-server", withExtension: nil) else {
            fail("Die Serverdatei fehlt. Bitte die App mit macos/build.sh neu bauen.")
            return
        }
        do {
            try FileManager.default.createDirectory(at: dataDirectory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
            let hostname = try localHostname()
            let child = Process()
            let stdin = Pipe()
            let stdout = Pipe()
            let stderr = Pipe()
            child.executableURL = executable
            child.arguments = ["desktop", "--data", dataDirectory.path, "--host", hostname]
            child.standardInput = stdin
            child.standardOutput = stdout
            // The server logs structured events only, never bodies or secrets.
            child.standardError = stderr
            child.environment = ["PATH": "/usr/bin:/bin:/usr/sbin:/sbin", "HOME": NSHomeDirectory()]
            child.terminationHandler = { [weak self] finished in
                Task { @MainActor in self?.didExit(code: finished.terminationStatus) }
            }
            try child.run()
            process = child
            input = stdin
            output = stdout
            errorPipe = stderr
            let handle = stdout.fileHandleForReading
            Task.detached { [weak self] in
                // Each line belongs to the private child pipe. Never log it.
                var buffer = Data()
                while true {
                    let chunk = handle.availableData
                    if chunk.isEmpty { break }
                    buffer.append(chunk)
                    if buffer.count > 1_048_576 { break }
                    while let newline = buffer.firstIndex(of: 10) {
                        let line = Data(buffer[..<newline])
                        buffer.removeSubrange(...newline)
                        await self?.receive(line)
                    }
                }
            }
            let errorHandle = stderr.fileHandleForReading
            let logFile = logFile
            Task.detached {
                var pending = Data()
                while true {
                    let chunk = errorHandle.availableData
                    if chunk.isEmpty { break }
                    pending.append(chunk)
                    while let newline = pending.firstIndex(of: 10) {
                        let line = pending[...newline]
                        pending.removeSubrange(...newline)
                        try? logFile.append(line)
                    }
                    if pending.count > 64 * 1024 {
                        try? logFile.append(pending)
                        pending.removeAll(keepingCapacity: true)
                    }
                }
                if !pending.isEmpty { try? logFile.append(pending) }
            }
            monitor?.cancel()
            monitor = Task { [weak self] in
                while !Task.isCancelled {
                    try? await Task.sleep(for: .seconds(5))
                    guard !Task.isCancelled, let self else { return }
                    self.tick()
                }
            }
        } catch {
            fail("Serverstart fehlgeschlagen: \(error.localizedDescription)")
        }
    }

    func stop() {
        desiredRunning = false
        restartTask?.cancel()
        pairingVisible = false
        status = nil
        guard let child = process else { phase = .stopped; return }
        phase = .stopping
        send(action: "stop")
        Task {
            try? await Task.sleep(for: .seconds(12))
            if child.isRunning { child.terminate() }
        }
    }

    func prepareToQuit() {
        quitting = true
        stop()
        try? input?.fileHandleForWriting.close()
    }

    func showPairing() {
        pairingVisible = true
        send(action: "pair", harness: pairingKind)
    }

    func copyAddress(local: Bool = false) {
        guard let status else { return }
        copy(local ? status.localURL + "/mcp" : status.serverURL)
    }

    func copyHarnessCommand() {
        guard let status, let pairing = status.pairing, let harness = pairing.harness,
              let cli = Bundle.main.url(forResource: "statectl", withExtension: nil) else { return }
        func quote(_ value: String) -> String { "'" + value.replacingOccurrences(of: "'", with: "'\\''") + "'" }
        copy("\(quote(cli.path)) pair --server \(quote(status.localURL)) --code \(quote(pairing.code)) --harness \(quote(harness)) --profile \(quote(harness))")
    }

    func revealLog() {
        NSWorkspace.shared.activateFileViewerSelecting([logFile.url])
    }

    func refreshLoginStatus() {
        loginEnabled = SMAppService.mainApp.status == .enabled
        loginNeedsApproval = SMAppService.mainApp.status == .requiresApproval
    }

    func setLoginEnabled(_ enabled: Bool) {
        do {
            if enabled { try SMAppService.mainApp.register() }
            else { try SMAppService.mainApp.unregister() }
            refreshLoginStatus()
        } catch {
            refreshLoginStatus()
            message = "Autostart konnte nicht geändert werden: \(error.localizedDescription)"
        }
    }

    private func copy(_ value: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(value, forType: .string)
    }

    private func send(action: String, harness: String = "") {
        guard let input, process?.isRunning == true,
              var bytes = try? JSONSerialization.data(withJSONObject: ["action": action, "harness": harness]) else { return }
        bytes.append(10)
        do { try input.fileHandleForWriting.write(contentsOf: bytes) }
        catch { message = "Die Verbindung zum Server wurde unterbrochen." }
    }

    private func receive(_ line: Data) {
        guard desiredRunning else { return }
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { decoder in
            let text = try decoder.singleValueContainer().decode(String.self)
            let format = ISO8601DateFormatter()
            format.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let date = format.date(from: text) { return date }
            format.formatOptions = [.withInternetDateTime]
            guard let date = format.date(from: text) else { throw URLError(.cannotParseResponse) }
            return date
        }
        // Only nested actor fields need snake-case translation. Top-level URL
        // keys have explicit spellings to preserve Swift's URL initialism.
        guard let update = try? decoder.decode(DesktopStatus.self, from: line) else {
            fail("Die Serverantwort konnte nicht gelesen werden.")
            return
        }
        let previousCount = status?.devices.count ?? 0
        status = update
        phase = .running
        lastUpdated = Date()
        message = update.error?.isEmpty == false ? update.error : nil
        if update.devices.count > previousCount, pairingKind.isEmpty { pairingVisible = false }
    }

    private func didWake() {
        sleeping = false
        // A sleeping Mac sends no status lines; that silence is not a hang.
        lastUpdated = Date()
        lastTick = Date()
        if desiredRunning, process == nil { start() }
        send(action: "status")
    }

    private func tick() {
        guard desiredRunning else { return }
        if sleeping { return }
        let now = Date()
        // A tick that arrives long after the previous one means this Mac was
        // asleep, even if the wake notification has not been delivered yet.
        let elapsed = now.timeIntervalSince(lastTick)
        lastTick = now
        if elapsed < 15, now.timeIntervalSince(lastUpdated ?? startingAt) > 25 {
            phase = .failed
            message = "Der Server antwortet nicht. Ein Neustart wird versucht."
            process?.terminate()
            return
        }
        if pairingVisible && status?.pairing == nil { send(action: "pair", harness: pairingKind) }
        send(action: "status")
    }

    private func didExit(code: Int32) {
        monitor?.cancel()
        try? input?.fileHandleForWriting.close()
        input = nil
        output = nil
        errorPipe = nil
        process = nil
        status = nil
        if quitting { NSApplication.shared.reply(toApplicationShouldTerminate: true); return }
        guard desiredRunning else { phase = .stopped; return }
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
    }

    private func fail(_ text: String) { phase = .failed; message = text }

    private func localHostname() throws -> String {
        let task = Process()
        let pipe = Pipe()
        task.executableURL = URL(fileURLWithPath: "/usr/sbin/scutil")
        task.arguments = ["--get", "LocalHostName"]
        task.standardOutput = pipe
        task.standardError = FileHandle.nullDevice
        try task.run()
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        task.waitUntilExit()
        guard task.terminationStatus == 0,
              let name = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines), !name.isEmpty else {
            throw NSError(domain: "StateDesktop", code: 1, userInfo: [NSLocalizedDescriptionKey: "Der lokale Name dieses Macs fehlt."])
        }
        return name + ".local"
    }
}

extension DesktopDevice.Actor {
    enum CodingKeys: String, CodingKey { case id, displayName = "display_name", deviceName = "device_name" }
}
extension DesktopDevice {
    enum CodingKeys: String, CodingKey { case actor, lastUsedAt = "last_used_at" }
}
extension DesktopPairing {
    enum CodingKeys: String, CodingKey { case url, code, expiresAt = "expires_at", harness }
}
