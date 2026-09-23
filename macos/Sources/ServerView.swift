import AppKit
import CoreImage.CIFilterBuiltins
import ServiceManagement
import StateServerCore
import SwiftUI

/// The server window: who is running, how to reach it, and how to pair
/// something with it. Built like a System Settings pane, because that is what
/// a Mac owner expects from a background service.
struct ServerView: View {
    @Bindable var controller: ServerController
    @State private var relayInput = ""
    @State private var relayError: String?

    var body: some View {
        VStack(spacing: 0) {
            header
                .padding(.horizontal, 28)
                .padding(.top, 26)
                .padding(.bottom, 14)

            if let message = controller.message {
                Label(message, systemImage: "exclamationmark.triangle.fill")
                    .font(.callout)
                    .foregroundStyle(.orange)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 28)
                    .padding(.bottom, 6)
            }

            Form {
                addressSection
                pairingSection
                devicesSection
                relaySection
                generalSection
                diagnosticsSection
            }
            .formStyle(.grouped)
            .scrollContentBackground(.hidden)
        }
        .frame(width: 580, height: 720)
        .background(ServerTheme.ground.ignoresSafeArea())
        .onAppear { relayInput = controller.relayURL }
        .onDisappear { controller.pairingVisible = false }
    }

    // MARK: Header

    private var header: some View {
        HStack(spacing: 16) {
            Image(nsImage: NSApp.applicationIconImage)
                .resizable()
                .frame(width: 64, height: 64)
                .accessibilityHidden(true)

            VStack(alignment: .leading, spacing: 4) {
                Text("State Server")
                    .font(.title2.weight(.bold))
                HStack(spacing: 7) {
                    Circle()
                        .fill(statusColor)
                        .frame(width: 8, height: 8)
                    Text(controller.label)
                        .font(.callout)
                        .foregroundStyle(.secondary)
                    if controller.phase == .starting || controller.phase == .stopping {
                        ProgressView().controlSize(.mini)
                    }
                }
            }

            Spacer()

            if controller.phase == .running {
                Button("Stoppen") { controller.stop() }
                    .buttonStyle(SoftButtonStyle())
            } else {
                Button("Starten") { controller.start() }
                    .buttonStyle(InkButtonStyle())
                    .disabled(controller.phase == .starting || controller.phase == .stopping)
            }
        }
    }

    private var statusColor: Color {
        switch controller.phase {
        case .running: .green
        case .starting, .stopping: .yellow
        case .stopped: .secondary
        case .failed: .red
        }
    }

    // MARK: Sections

    private var addressSection: some View {
        Section {
            if let status = controller.status {
                AddressRow(title: "Im Netzwerk", value: status.serverURL) { controller.copyAddress() }
                AddressRow(title: "Für Agenten", value: status.localURL + "/mcp") { controller.copyAddress(local: true) }
            } else {
                Text("Sobald der Server läuft, stehen hier seine Adressen.")
                    .foregroundStyle(.secondary)
            }
        } header: {
            Text("Adresse")
        } footer: {
            Text("Verschlüsselt im lokalen Netzwerk. Erreichbar, solange dieser Mac wach ist.")
        }
    }

    private var pairingSection: some View {
        Section {
            Picker("Verbinden mit", selection: $controller.pairingKind) {
                Text("iPhone oder iPad").tag("")
                Divider()
                Text("Claude Code").tag("claude-code")
                Text("Codex").tag("codex")
                Text("OpenCode").tag("opencode")
                Text("DeepSeek Harness").tag("deepseek-harness")
                Text("Pi").tag("pi")
                Divider()
                Text("Runner für Agent-Aufgaben").tag(PairingCommand.runnerSelection)
            }
            .onChange(of: controller.pairingKind) { _, _ in
                if controller.pairingVisible { controller.showPairing() }
            }

            pairingContent
                .frame(maxWidth: .infinity)
                .padding(.vertical, 10)
        } header: {
            Text("Verbinden")
        } footer: {
            Text("Jeder Code gilt einmal und zehn Minuten lang. Solange er angezeigt wird, erneuert er sich von selbst.")
        }
    }

    @ViewBuilder
    private var pairingContent: some View {
        if controller.pairingVisible, let pairing = controller.status?.pairing,
           controller.pairingMatchesSelection(pairing) {
            if controller.pairingKind.isEmpty {
                VStack(spacing: 14) {
                    QRCodeView(value: pairing.url)
                    VStack(spacing: 4) {
                        Text("In State auf dem iPhone oder iPad scannen")
                            .font(.headline)
                        TimelineView(.periodic(from: .now, by: 1)) { context in
                            Text("Noch \(max(0, Int(pairing.expiresAt.timeIntervalSince(context.date)))) Sekunden gültig")
                                .font(.callout.monospacedDigit())
                                .foregroundStyle(.secondary)
                        }
                    }
                    Button("Ausblenden") { controller.pairingVisible = false }
                        .buttonStyle(SoftButtonStyle())
                }
            } else {
                VStack(spacing: 12) {
                    Text(PairingCommand.isRunner(controller.pairingKind)
                         ? "Der Befehl koppelt state-runner und installiert ihn als Hintergrunddienst. Passe --work-root vorher an deinen Projektordner an."
                         : "Der Befehl verbindet das Programm mit diesem Server. Einfach im Terminal ausführen.")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                        .fixedSize(horizontal: false, vertical: true)
                    Button {
                        controller.copyHarnessCommand()
                    } label: {
                        Label("Befehl kopieren", systemImage: "doc.on.doc")
                    }
                    .buttonStyle(InkButtonStyle())
                }
                .frame(maxWidth: 380)
            }
        } else {
            Button(controller.pairingKind.isEmpty ? "QR-Code anzeigen" : "Kopplungsbefehl erzeugen") {
                controller.showPairing()
            }
            .buttonStyle(InkButtonStyle())
            .disabled(controller.phase != .running)
        }
    }

    @ViewBuilder
    private var devicesSection: some View {
        if let devices = controller.status?.devices, !devices.isEmpty {
            Section("Verbundene Geräte") {
                ForEach(devices) { device in
                    LabeledContent {
                        if let date = device.lastUsedAt {
                            Text(date, style: .relative).foregroundStyle(.secondary)
                        } else {
                            Text("Gekoppelt").foregroundStyle(.secondary)
                        }
                    } label: {
                        Label(device.actor.deviceName, systemImage: "iphone")
                    }
                }
            }
        }
    }

    private var relaySection: some View {
        Section {
            HStack(spacing: 10) {
                TextField("Relay-Adresse", text: $relayInput, prompt: Text(verbatim: "https://relay.example.com"))
                    .labelsHidden()
                    .autocorrectionDisabled()
                    .onSubmit { applyRelay() }
                Button("Übernehmen") { applyRelay() }
                    .buttonStyle(SoftButtonStyle())
                    .disabled(controller.phase == .stopping || relayInput == controller.relayURL)
            }
            if let relayError {
                Label(relayError, systemImage: "exclamationmark.triangle")
                    .font(.callout)
                    .foregroundStyle(.orange)
            }
        } header: {
            Text("Mitteilungen unterwegs")
        } footer: {
            Text(controller.relayURL.isEmpty
                 ? "Ohne Relay kommen Mitteilungen nur im WLAN an. Mit einem State-Relay, zum Beispiel auf deinem VPS, auch unterwegs. Inhalte bleiben Ende-zu-Ende verschlüsselt."
                 : "Aktiv über \(controller.relayURL). Inhalte bleiben Ende-zu-Ende verschlüsselt.")
        }
    }

    private var generalSection: some View {
        Section("Allgemein") {
            Toggle("Bei der Anmeldung starten", isOn: Binding(
                get: { controller.loginEnabled },
                set: { controller.setLoginEnabled($0) }
            ))
            if controller.loginNeedsApproval {
                Button("In den Systemeinstellungen erlauben") { SMAppService.openSystemSettingsLoginItems() }
            }
            LabeledContent("Daten") {
                Button("Im Finder zeigen") { NSWorkspace.shared.open(controller.dataDirectory) }
                    .buttonStyle(SoftButtonStyle())
            }
        }
    }

    private var diagnosticsSection: some View {
        Section {
            LabeledContent("Letzter Exit-Code") {
                Text(controller.lastExitCode.map(String.init) ?? "keiner")
                    .monospacedDigit()
                    .textSelection(.enabled)
            }
            LabeledContent("Fehlstarts in Folge") {
                Text("\(controller.consecutiveFailures)").monospacedDigit()
            }
            LabeledContent("Server-Log") {
                Button("Im Finder zeigen") { controller.revealLog() }
                    .buttonStyle(SoftButtonStyle())
            }
        } header: {
            Text("Diagnose")
        } footer: {
            if let date = controller.lastUpdated {
                Text("Zuletzt geprüft \(date.formatted(date: .omitted, time: .standard)).")
            }
        }
    }

    private func applyRelay() {
        if controller.applyRelay(relayInput) {
            relayInput = controller.relayURL
            relayError = nil
        } else {
            relayError = "Bitte eine vollständige https:// Adresse eintragen oder das Feld leer lassen."
        }
    }
}

/// An address the owner copies more often than reads.
private struct AddressRow: View {
    let title: String
    let value: String
    let copy: () -> Void

    var body: some View {
        LabeledContent {
            HStack(spacing: 8) {
                Text(value)
                    .font(.callout.monospaced())
                    .textSelection(.enabled)
                    .lineLimit(1)
                    .truncationMode(.middle)
                Button(action: copy) {
                    Image(systemName: "doc.on.doc")
                }
                .buttonStyle(.borderless)
                .help("\(title) kopieren")
                .accessibilityLabel("\(title) kopieren")
            }
        } label: {
            Text(title)
        }
    }
}

private struct QRCodeView: View {
    let value: String

    var body: some View {
        Group {
            if let image = qrImage {
                Image(nsImage: image).interpolation(.none).resizable().scaledToFit()
            } else {
                Image(systemName: "qrcode").font(.largeTitle)
            }
        }
        .frame(width: 184, height: 184)
        .padding(14)
        .background(.white, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .shadow(color: .black.opacity(0.08), radius: 10, y: 4)
        .accessibilityLabel("Einmaliger Kopplungs-QR-Code")
    }

    private var qrImage: NSImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(value.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage,
              let image = CIContext().createCGImage(
                output.transformed(by: CGAffineTransform(scaleX: 8, y: 8)),
                from: output.extent.applying(CGAffineTransform(scaleX: 8, y: 8))
              ) else { return nil }
        return NSImage(cgImage: image, size: NSSize(width: image.width, height: image.height))
    }
}
