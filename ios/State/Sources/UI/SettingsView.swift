import CoreImage.CIFilterBuiltins
import SwiftUI
import UIKit

struct SettingsView: View {
    @Bindable var model: AppModel
    @Binding var opensNotificationSettings: Bool
    @State private var harnessSelection = "codex"
    @State private var customHarness = ""
    @State private var harnessName = "Codex on Mac"
    @State private var pairingCode: PairingCode?
    @State private var confirmsDisconnect = false

    private var harness: String {
        harnessSelection == HarnessCatalog.customTag
            ? HarnessCatalog.normalize(customHarness)
            : harnessSelection
    }

    private var canCreatePairingCode: Bool {
        HarnessCatalog.isValid(harness)
            && !harnessName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    var body: some View {
        NavigationStack {
            List {
                Section("Connection") {
                    if let session = model.session {
                        LabeledContent(
                            "Server",
                            value: model.isDemo ? String(localized: "Local demo") : session.serverURL.host() ?? session.serverURL.absoluteString
                        )
                        LabeledContent("Identity") {
                            OriginBadge(actor: session.actor)
                        }
                    }
                    if let lastSyncAt = model.lastSyncAt {
                        LabeledContent("Last synchronized") {
                            Text(lastSyncAt, style: .relative)
                        }
                    }
                    Button {
                        Task { await model.synchronize() }
                    } label: {
                        Label("Synchronize now", systemImage: "arrow.triangle.2.circlepath")
                    }
                    .disabled(model.isSyncing)
                }

                if model.session?.actor.kind == .owner {
                    Section("Connect an agent") {
                        Picker("Harness", selection: $harnessSelection) {
                            ForEach(HarnessCatalog.presets, id: \.id) { preset in
                                Text(preset.label).tag(preset.id)
                            }
                            Text("Other").tag(HarnessCatalog.customTag)
                        }
                        if harnessSelection == HarnessCatalog.customTag {
                            TextField("Harness identifier", text: $customHarness)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled()
                            if !customHarness.isEmpty, !HarnessCatalog.isValid(harness) {
                                Text("Use 2 to 32 characters: lower case letters, digits and inner hyphens.")
                                    .font(.caption)
                                    .foregroundStyle(.red)
                            }
                        }
                        TextField("Agent name", text: $harnessName)
                        Button {
                            Task {
                                pairingCode = await model.createPairingCode(
                                    harness: harness,
                                    displayName: harnessName,
                                    deviceName: "Mac"
                                )
                            }
                        } label: {
                            Label("Create one-time code", systemImage: "link.badge.plus")
                        }
                        .disabled(!canCreatePairingCode)

                        if let pairingCode {
                            VStack(alignment: .leading, spacing: 10) {
                                Text(pairingCode.code)
                                    .font(.title3.monospaced().weight(.semibold))
                                    .textSelection(.enabled)
                                LabeledContent("Expires") {
                                    Text(pairingCode.expiresAt, style: .relative)
                                }
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                Button {
                                    UIPasteboard.general.string = pairingCommand(code: pairingCode.code)
                                } label: {
                                    Label("Copy statectl command", systemImage: "doc.on.doc")
                                }
                                if !HarnessCatalog.hasShippedIntegration(harness) {
                                    Text("statectl stores the credential and prints the MCP server entry for this agent. Add it to that agent's own configuration.")
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                }
                            }
                            .padding(.vertical, 4)
                        }
                    }

                    actorSection(title: "Agents", actors: model.agents)
                    actorSection(title: "Devices", actors: model.devices.filter { $0.id != model.session?.actor.id })
                }

                Section("Notifications") {
                    NavigationLink {
                        NotificationSettingsView()
                    } label: {
                        Label("Delivery and privacy", systemImage: "bell.badge")
                    }
                }
                .navigationDestination(isPresented: $opensNotificationSettings) {
                    NotificationSettingsView()
                }

                Section("About") {
                    LabeledContent("Version", value: appVersion)
                    Link(destination: URL(string: "https://github.com/Nicremo/state")!) {
                        Label("Source code", systemImage: "chevron.left.forwardslash.chevron.right")
                    }
                    LabeledContent("License", value: "Apache-2.0")
                }

                Section {
                    Button("Disconnect this device", role: .destructive) {
                        confirmsDisconnect = true
                    }
                }
            }
            .navigationTitle("Settings")
            .confirmationDialog("Disconnect this device?", isPresented: $confirmsDisconnect) {
                Button("Disconnect", role: .destructive) { model.disconnect() }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("The server data remains intact. This device loses its stored credential.")
            }
        }
    }

    @ViewBuilder
    private func actorSection(title: LocalizedStringKey, actors: [Actor]) -> some View {
        Section(title) {
            if actors.isEmpty {
                Text("None connected")
                    .foregroundStyle(.secondary)
            }
            ForEach(actors, id: \.id) { actor in
                HStack {
                    VStack(alignment: .leading) {
                        Text(actor.displayName ?? actor.harness ?? actor.deviceName ?? actor.kind.rawValue)
                        if let deviceName = actor.deviceName {
                            Text(deviceName)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                    Spacer()
                    Button(role: .destructive) {
                        Task { await model.revokeActor(actor) }
                    } label: {
                        Image(systemName: "xmark.circle")
                    }
                    .accessibilityLabel("Revoke access")
                }
            }
        }
    }

    private var appVersion: String {
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "1.0.0"
        let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "1"
        return "\(version) (\(build))"
    }

    private func pairingCommand(code: String) -> String {
        guard let server = model.session?.serverURL.absoluteString else { return code }
        return "statectl pair --profile \(harness) --server \(server) --code \(code) --harness \(harness)"
    }
}

struct NotificationSettingsView: View {
    @State private var status = String(localized: "Checking")
    @Environment(\.openURL) private var openURL

    var body: some View {
        List {
            Section("Status") {
                LabeledContent("System permission", value: status)
            }
            Section("How delivery works") {
                Label("Upcoming reminders are secured locally on this iPhone.", systemImage: "iphone.and.arrow.forward")
                Label("The relay receives only encrypted reminder packages and an opaque route.", systemImage: "lock.shield")
                Label("If decryption fails, State shows generic notification text.", systemImage: "exclamationmark.shield")
            }
            Button("Open iOS notification settings") {
                if let url = URL(string: UIApplication.openNotificationSettingsURLString) {
                    openURL(url)
                }
            }
        }
        .navigationTitle("Notifications")
        .task {
            let settings = await UNUserNotificationCenter.current().notificationSettings()
            status = switch settings.authorizationStatus {
            case .authorized, .provisional, .ephemeral: String(localized: "Allowed")
            case .denied: String(localized: "Denied")
            case .notDetermined: String(localized: "Not requested")
            @unknown default: String(localized: "Unknown")
            }
        }
    }
}
