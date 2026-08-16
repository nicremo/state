import SwiftUI
import UIKit
import UserNotifications

struct SettingsView: View {
    @Bindable var model: AppModel
    @Binding var opensNotificationSettings: Bool

    @State private var harnessSelection = "codex"
    @State private var customHarness = ""
    @State private var harnessName = HarnessCatalog.suggestedName(for: "codex")
    @State private var editedName = false
    @State private var pairingCode: PairingCode?
    @State private var isCreatingCode = false
    @State private var copiedCommand = false
    @State private var confirmsDisconnect = false

    private var harness: String {
        harnessSelection == HarnessCatalog.customTag
            ? HarnessCatalog.normalize(customHarness)
            : harnessSelection
    }

    private var canCreatePairingCode: Bool {
        HarnessCatalog.isValid(harness)
            && !harnessName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !isCreatingCode
    }

    var body: some View {
        NavigationStack {
            List {
                Section {
                    NavigationLink {
                        DocumentationView()
                    } label: {
                        Label("Documentation", systemImage: "book")
                    }
                }

                connectionSection
                syncSection

                if model.session?.actor.kind == .owner {
                    agentSection
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
                    Link(destination: StateLinks.repository) {
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
            .stateBackground()
            .confirmationDialog("Disconnect this device?", isPresented: $confirmsDisconnect) {
                Button("Disconnect", role: .destructive) { model.disconnect() }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("The server data remains intact. This device loses its stored credential.")
            }
        }
    }

    // MARK: Connection

    @ViewBuilder
    private var connectionSection: some View {
        Section {
            if let session = model.session {
                HStack(spacing: StateTheme.Space.group) {
                    Image(systemName: model.isDemo ? "sparkles" : "server.rack")
                        .font(.system(size: 15, weight: .medium))
                        .foregroundStyle(StateTheme.accent)
                        .frame(width: 34, height: 34)
                        .background(StateTheme.accentSoft, in: RoundedRectangle(cornerRadius: 10, style: .continuous))

                    VStack(alignment: .leading, spacing: StateTheme.Space.hairline) {
                        Text(serverLabel(session))
                            .font(.subheadline.weight(.semibold))
                            .foregroundStyle(StateTheme.graphite)
                            .lineLimit(1)
                            .truncationMode(.middle)
                        Text(model.isDemo
                            ? String(localized: "Sample data, nothing leaves this iPhone")
                            : String(localized: "Connected"))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }

                    Spacer(minLength: StateTheme.Space.inner)

                    OriginBadge(actor: session.actor)
                }
                .padding(.vertical, StateTheme.Space.tight)
            }
        } header: {
            Text("Connection")
        } footer: {
            if let lastSyncAt = model.lastSyncAt {
                Text(
                    String(
                        format: String(localized: "Last synchronized %@"),
                        lastSyncAt.formatted(.relative(presentation: .named))
                    )
                )
            }
        }
    }

    private var syncSection: some View {
        Section {
            Button {
                Task { await model.synchronize() }
            } label: {
                HStack(spacing: StateTheme.Space.group) {
                    if model.isSyncing {
                        ProgressView()
                            .controlSize(.small)
                            .frame(width: 20)
                    } else {
                        Image(systemName: "arrow.triangle.2.circlepath")
                            .frame(width: 20)
                    }
                    Text(model.isSyncing ? "Synchronizing" : "Synchronize now")
                    Spacer()
                }
            }
            .disabled(model.isSyncing || model.isDemo)
            .animation(StateTheme.stateChange, value: model.isSyncing)
        }
    }

    // MARK: Agents

    private var agentSection: some View {
        Section {
            Picker("Harness", selection: $harnessSelection) {
                ForEach(HarnessCatalog.presets, id: \.id) { preset in
                    Text(preset.label).tag(preset.id)
                }
                Text("Other").tag(HarnessCatalog.customTag)
            }
            .onChange(of: harnessSelection) { _, _ in refreshSuggestedName() }

            if harnessSelection == HarnessCatalog.customTag {
                TextField("Harness identifier", text: $customHarness)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .onChange(of: customHarness) { _, _ in refreshSuggestedName() }
                if !customHarness.isEmpty, !HarnessCatalog.isValid(harness) {
                    Label(
                        String(localized: "Use 2 to 32 characters: lower case letters, digits and inner hyphens."),
                        systemImage: "exclamationmark.circle"
                    )
                    .labelStyle(.tight)
                    .font(.caption)
                    .foregroundStyle(.red)
                }
            }

            LabeledContent("Agent name") {
                TextField(
                    "Agent name",
                    text: Binding(
                        get: { harnessName },
                        set: { value in
                            harnessName = value
                            editedName = true
                        }
                    )
                )
                .multilineTextAlignment(.trailing)
            }

            Button {
                createPairingCode()
            } label: {
                HStack(spacing: StateTheme.Space.group) {
                    if isCreatingCode {
                        ProgressView()
                            .controlSize(.small)
                            .frame(width: 20)
                    } else {
                        Image(systemName: "link.badge.plus")
                            .frame(width: 20)
                    }
                    Text("Create one-time code")
                    Spacer()
                }
            }
            .disabled(!canCreatePairingCode)
            .animation(StateTheme.stateChange, value: isCreatingCode)

            if let pairingCode {
                pairingResult(pairingCode)
            }
        } header: {
            Text("Connect an agent")
        } footer: {
            Text("Every agent gets its own credential, so the history keeps them apart and revoking one leaves the others alone.")
        }
        .animation(StateTheme.contentChange, value: pairingCode?.code)
    }

    private func pairingResult(_ code: PairingCode) -> some View {
        VStack(alignment: .leading, spacing: StateTheme.Space.group) {
            // Menlo instead of the system monospace: SF Mono turns the hyphens
            // of a grouped code into long dashes, and this code gets typed.
            Text(code.code)
                .font(.custom("Menlo-Bold", size: 19, relativeTo: .title3))
                .foregroundStyle(StateTheme.graphite)
                .textSelection(.enabled)

            MetaLabel(
                text: String(
                    format: String(localized: "Expires %@"),
                    code.expiresAt.formatted(.relative(presentation: .named))
                ),
                systemImage: "clock"
            )

            Button {
                UIPasteboard.general.string = pairingCommand(code: code.code)
                withAnimation(StateTheme.stateChange) { copiedCommand = true }
            } label: {
                Label(
                    copiedCommand ? String(localized: "Copied") : String(localized: "Copy statectl command"),
                    systemImage: copiedCommand ? "checkmark" : "doc.on.doc"
                )
                .labelStyle(.tight)
                .font(.subheadline.weight(.medium))
            }
            .buttonStyle(.plain)
            .foregroundStyle(copiedCommand ? Color.green : StateTheme.accent)

            if !HarnessCatalog.hasShippedIntegration(harness) {
                Text("statectl stores the credential and prints the MCP server entry for this agent. Add it to that agent's own configuration.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, StateTheme.Space.snug)
    }

    @ViewBuilder
    private func actorSection(title: LocalizedStringKey, actors: [Actor]) -> some View {
        Section(title) {
            if actors.isEmpty {
                Text("None connected")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            ForEach(actors, id: \.id) { actor in
                HStack(spacing: StateTheme.Space.group) {
                    VStack(alignment: .leading, spacing: StateTheme.Space.hairline) {
                        Text(actor.displayName ?? actor.harness ?? actor.deviceName ?? actor.kind.rawValue)
                            .font(.subheadline)
                            .foregroundStyle(StateTheme.graphite)
                        if let deviceName = actor.deviceName {
                            Text(deviceName)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                    Spacer(minLength: StateTheme.Space.inner)
                    Button(role: .destructive) {
                        Task { await model.revokeActor(actor) }
                    } label: {
                        Image(systemName: "xmark.circle")
                            .font(.body)
                            .frame(width: 44, height: 44)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(.red)
                    .accessibilityLabel("Revoke access")
                }
                .padding(.vertical, -StateTheme.Space.snug)
            }
        }
    }

    // MARK: Behavior

    /// Keeps the agent name in step with the chosen harness until the owner
    /// types their own. A blocked button because a required field is empty is
    /// the worst kind of empty field.
    private func refreshSuggestedName() {
        guard !editedName else { return }
        let label = harnessSelection == HarnessCatalog.customTag
            ? HarnessCatalog.normalize(customHarness)
            : harnessSelection
        harnessName = HarnessCatalog.isValid(label) ? HarnessCatalog.suggestedName(for: label) : ""
    }

    private func createPairingCode() {
        isCreatingCode = true
        copiedCommand = false
        Task {
            let created = await model.createPairingCode(
                harness: harness,
                displayName: harnessName.trimmingCharacters(in: .whitespacesAndNewlines),
                deviceName: "Mac"
            )
            isCreatingCode = false
            if let created {
                pairingCode = created
            }
        }
    }

    private func serverLabel(_ session: ServerSession) -> String {
        model.isDemo
            ? String(localized: "Local demo")
            : session.serverURL.host() ?? session.serverURL.absoluteString
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
                NotificationFact(
                    systemImage: "iphone.and.arrow.forward",
                    text: String(localized: "Upcoming reminders are secured locally on this iPhone.")
                )
                NotificationFact(
                    systemImage: "lock.shield",
                    text: String(localized: "The relay receives only encrypted reminder packages and an opaque route.")
                )
                NotificationFact(
                    systemImage: "exclamationmark.shield",
                    text: String(localized: "If decryption fails, State shows generic notification text.")
                )
            }
            Section {
                Button("Open iOS notification settings") {
                    if let url = URL(string: UIApplication.openNotificationSettingsURLString) {
                        openURL(url)
                    }
                }
            }
        }
        .navigationTitle("Notifications")
        .navigationBarTitleDisplayMode(.inline)
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

private struct NotificationFact: View {
    let systemImage: String
    let text: String

    var body: some View {
        HStack(alignment: .top, spacing: StateTheme.Space.group) {
            Image(systemName: systemImage)
                .font(.system(size: 14, weight: .medium))
                .foregroundStyle(StateTheme.accent)
                .frame(width: 22)
            Text(text)
                .font(.footnote)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(.vertical, StateTheme.Space.hairline)
        .accessibilityElement(children: .combine)
    }
}
