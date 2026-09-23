import AppKit
import CoreImage.CIFilterBuiltins
import ServiceManagement
import SwiftUI

struct ServerView: View {
    @Bindable var controller: ServerController
    @State private var relayInput = ""
    @State private var relayError: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                HStack(spacing: 14) {
                    Image(systemName: "externaldrive.connected.to.line.below")
                        .font(.system(size: 30)).foregroundStyle(.tint)
                        .frame(width: 56, height: 56)
                        .background(.tint.opacity(0.10), in: RoundedRectangle(cornerRadius: 14))
                    VStack(alignment: .leading, spacing: 4) {
                        Text("State Server").font(.title2.bold())
                        Text("Dein Mac. Deine Daten.").foregroundStyle(.secondary)
                    }
                    Spacer()
                    Circle().fill(controller.phase == .running ? Color.green : Color.orange).frame(width: 9, height: 9)
                    Text(controller.label).font(.callout)
                }

                GroupBox {
                    VStack(alignment: .leading, spacing: 14) {
                        HStack {
                            Label("Lokaler Server", systemImage: "network").font(.headline)
                            Spacer()
                            if controller.phase == .starting || controller.phase == .stopping { ProgressView().controlSize(.small) }
                            if controller.phase == .running {
                                Button("Stoppen") { controller.stop() }
                            } else {
                                Button("Starten") { controller.start() }
                                    .disabled(controller.phase == .starting || controller.phase == .stopping)
                            }
                        }
                        if let status = controller.status {
                            HStack {
                                Text(status.serverURL).font(.system(.callout, design: .monospaced)).textSelection(.enabled)
                                Spacer()
                                Button { controller.copyAddress() } label: { Image(systemName: "doc.on.doc") }
                                    .help("Serveradresse kopieren").accessibilityLabel("Serveradresse kopieren")
                            }
                            Label("Verschlüsselt im lokalen Netzwerk", systemImage: "lock.shield").foregroundStyle(.secondary).font(.caption)
                        } else {
                            Text("Der Server wird vollständig auf diesem Mac betrieben.").foregroundStyle(.secondary)
                        }
                    }.padding(10)
                }

                GroupBox {
                    VStack(alignment: .leading, spacing: 14) {
                        HStack {
                            Label("Diagnose", systemImage: "stethoscope").font(.headline)
                            Spacer()
                            Button("Log im Finder zeigen") { controller.revealLog() }
                        }
                        HStack {
                            Text("Letzter Exit-Code").foregroundStyle(.secondary)
                            Spacer()
                            Text(controller.lastExitCode.map(String.init) ?? "keiner")
                                .font(.system(.callout, design: .monospaced)).textSelection(.enabled)
                        }
                        HStack {
                            Text("Fehlstarts in Folge").foregroundStyle(.secondary)
                            Spacer()
                            Text("\(controller.consecutiveFailures)").font(.system(.callout, design: .monospaced))
                        }
                        Text("Servermeldungen stehen in ~/Library/Logs/State Server/server.log und werden bei 5 MB rotiert.")
                            .font(.caption).foregroundStyle(.secondary)
                    }.padding(10)
                }

                GroupBox {
                    VStack(alignment: .leading, spacing: 16) {
                        HStack {
                            Label("Gerät verbinden", systemImage: "qrcode").font(.headline)
                            Spacer()
                            Picker("Gerät", selection: $controller.pairingKind) {
                                Text("iPhone").tag("")
                                Text("Claude Code").tag("claude-code")
                                Text("Codex").tag("codex")
                                Text("OpenCode").tag("opencode")
                            }.labelsHidden().frame(width: 155)
                                .onChange(of: controller.pairingKind) { _, _ in
                                    if controller.pairingVisible { controller.showPairing() }
                                }
                        }
                        if controller.pairingVisible, let pairing = controller.status?.pairing,
                           (pairing.harness ?? "") == controller.pairingKind {
                            if controller.pairingKind.isEmpty {
                                HStack(alignment: .center, spacing: 22) {
                                    QRCodeView(value: pairing.url)
                                    VStack(alignment: .leading, spacing: 10) {
                                        Text("In State auf dem iPhone scannen.").font(.headline)
                                        Text("Beide Geräte müssen im selben Netzwerk sein. Verwende die iPhone-Version mit Unterstützung für lokale Server.")
                                            .foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                                        TimelineView(.periodic(from: .now, by: 1)) { context in
                                            Text("Gültig: \(max(0, Int(pairing.expiresAt.timeIntervalSince(context.date)))) Sekunden")
                                                .font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                                        }
                                        Button("QR-Code ausblenden") { controller.pairingVisible = false }
                                    }
                                }
                            } else {
                                Text("Kopiere den Befehl und führe ihn im Terminal aus. Er verbindet das gewählte Programm mit diesem lokalen Server.")
                                    .foregroundStyle(.secondary)
                                Button("Kopplungsbefehl kopieren") { controller.copyHarnessCommand() }.buttonStyle(.borderedProminent)
                            }
                        } else {
                            Text("Der Einmalcode läuft nach zehn Minuten ab. Solange diese Ansicht geöffnet ist, wird ein abgelaufener Code automatisch ersetzt.")
                                .foregroundStyle(.secondary)
                            Button(controller.pairingKind.isEmpty ? "QR-Code anzeigen" : "Kopplung vorbereiten") { controller.showPairing() }
                                .buttonStyle(.borderedProminent).disabled(controller.phase != .running)
                        }
                    }.padding(10)
                }

                GroupBox {
                    VStack(alignment: .leading, spacing: 14) {
                        Label("Push unterwegs (optional)", systemImage: "antenna.radiowaves.left.and.right").font(.headline)
                        TextField("https://relay.example.com", text: $relayInput)
                            .textFieldStyle(.roundedBorder)
                            .autocorrectionDisabled()
                        Text("Mit einem öffentlichen State-Relay, z.B. auf deinem VPS, bekommt dein iPhone Mitteilungen auch außerhalb des WLANs. Die Inhalte bleiben Ende-zu-Ende verschlüsselt.")
                            .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                        HStack(spacing: 10) {
                            Button("Übernehmen") { applyRelay() }
                                .disabled(controller.phase == .stopping)
                            if controller.relayURL.isEmpty {
                                Text("Kein Relay: Mitteilungen nur lokal und im WLAN.")
                                    .font(.caption).foregroundStyle(.secondary)
                            } else {
                                Text(controller.relayURL)
                                    .font(.caption.monospaced()).foregroundStyle(.secondary)
                                    .textSelection(.enabled).lineLimit(1).truncationMode(.middle)
                            }
                            Spacer()
                        }
                        if let relayError {
                            Label(relayError, systemImage: "exclamationmark.triangle")
                                .font(.caption).foregroundStyle(.orange).fixedSize(horizontal: false, vertical: true)
                        }
                    }.padding(10)
                }
                .onAppear { relayInput = controller.relayURL }

                if let devices = controller.status?.devices, !devices.isEmpty {
                    VStack(alignment: .leading, spacing: 10) {
                        Text("Verbundene Geräte").font(.headline)
                        ForEach(devices) { device in
                            HStack {
                                Label(device.actor.deviceName, systemImage: "iphone")
                                Spacer()
                                if let date = device.lastUsedAt {
                                    Text(date, style: .relative).font(.caption).foregroundStyle(.secondary)
                                } else { Text("Gekoppelt").foregroundStyle(.secondary) }
                            }
                        }
                    }
                }

                VStack(alignment: .leading, spacing: 12) {
                    Toggle("Bei der Anmeldung starten", isOn: Binding(get: { controller.loginEnabled }, set: { controller.setLoginEnabled($0) }))
                    if controller.loginNeedsApproval {
                        Button("Autostart in Systemeinstellungen erlauben") { SMAppService.openSystemSettingsLoginItems() }
                    }
                    Text("Beim Schließen des Fensters läuft State in der Menüleiste weiter. Synchronisierung ist möglich, solange der Mac wach und erreichbar ist.")
                        .font(.caption).foregroundStyle(.secondary)
                    HStack {
                        Button("Datenordner öffnen") { NSWorkspace.shared.open(controller.dataDirectory) }
                        Button("Lokale MCP-Adresse kopieren") { controller.copyAddress(local: true) }
                            .disabled(controller.phase != .running)
                        Spacer()
                        if let date = controller.lastUpdated { Text(date, style: .time).font(.caption).foregroundStyle(.secondary) }
                    }
                }

                if let message = controller.message {
                    Label(message, systemImage: "exclamationmark.triangle")
                        .font(.callout).foregroundStyle(.orange).textSelection(.enabled)
                }
            }.padding(26)
        }
        .frame(width: 620, height: 700)
        .background(Color(nsColor: .windowBackgroundColor))
        .onDisappear { controller.pairingVisible = false }
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

private struct QRCodeView: View {
    let value: String
    var body: some View {
        Group {
            if let image = qrImage {
                Image(nsImage: image).interpolation(.none).resizable().scaledToFit()
            } else { Image(systemName: "qrcode").font(.largeTitle) }
        }
        .frame(width: 188, height: 188)
        .padding(12).background(.white, in: RoundedRectangle(cornerRadius: 10))
        .accessibilityLabel("Einmaliger Kopplungs-QR-Code für dein iPhone")
    }

    private var qrImage: NSImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(value.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage,
              let image = CIContext().createCGImage(output.transformed(by: CGAffineTransform(scaleX: 8, y: 8)), from: output.extent.applying(CGAffineTransform(scaleX: 8, y: 8))) else { return nil }
        return NSImage(cgImage: image, size: NSSize(width: image.width, height: image.height))
    }
}
