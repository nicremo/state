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
    @State private var runnerName = ""
    @State private var runnerPairingCode: PairingCode?
    @State private var editingPolicy: ExecutionPolicy?
    @State private var createsPolicy = false
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

                    Section("Runners") {
                        if model.runners.isEmpty {
                            Text("None connected")
                                .foregroundStyle(.secondary)
                        }
                        ForEach(model.runners) { runner in
                            HStack {
                                VStack(alignment: .leading, spacing: 6) {
                                    Text(runner.displayName)
                                    if !runner.projects.isEmpty || !runner.adapters.isEmpty {
                                        HStack {
                                            ForEach(runner.projects, id: \.self) { projectID in
                                                chip(projectName(for: projectID))
                                            }
                                            ForEach(runner.adapters, id: \.self) { adapter in
                                                chip(adapter)
                                            }
                                        }
                                    }
                                    (Text(String(localized: "Last seen")) + Text(" ") + Text(runner.lastSeenAt, style: .relative))
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                }
                                Spacer()
                                Button(role: .destructive) {
                                    Task { await model.revokeRunner(runner) }
                                } label: {
                                    Image(systemName: "xmark.circle")
                                }
                                .accessibilityLabel("Revoke access")
                            }
                            .accessibilityIdentifier("runner-\(runner.id)")
                        }
                        TextField("Runner name", text: $runnerName)
                        Button {
                            Task {
                                runnerPairingCode = await model.createRunnerPairingCode(
                                    displayName: runnerName.trimmingCharacters(in: .whitespacesAndNewlines)
                                )
                            }
                        } label: {
                            Label("Create one-time code", systemImage: "link.badge.plus")
                        }
                        .disabled(runnerName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)

                        if let runnerPairingCode {
                            VStack(alignment: .leading, spacing: 10) {
                                Text(runnerPairingCode.code)
                                    .font(.title3.monospaced().weight(.semibold))
                                    .textSelection(.enabled)
                                LabeledContent("Expires") {
                                    Text(runnerPairingCode.expiresAt, style: .relative)
                                }
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                Button {
                                    UIPasteboard.general.string = runnerPairingCommand(code: runnerPairingCode.code)
                                } label: {
                                    Label("Copy state-runner command", systemImage: "doc.on.doc")
                                }
                                Text("Run the command on the machine that should execute runs. state-runner stores the credential there.")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                            .padding(.vertical, 4)
                        }
                    }

                    Section("Execution policies") {
                        if model.policies.isEmpty {
                            Text("None created yet")
                                .foregroundStyle(.secondary)
                        }
                        ForEach(model.policies) { policy in
                            HStack {
                                Button {
                                    editingPolicy = policy
                                } label: {
                                    HStack {
                                        VStack(alignment: .leading, spacing: 4) {
                                            Text(policy.name)
                                                .foregroundStyle(StateTheme.graphite)
                                            Text(policySubtitle(policy))
                                                .font(.caption)
                                                .foregroundStyle(.secondary)
                                        }
                                        Spacer(minLength: 8)
                                    }
                                    .contentShape(Rectangle())
                                }
                                .buttonStyle(.plain)
                                Toggle("Enabled", isOn: policyEnabledBinding(policy))
                                    .labelsHidden()
                            }
                            .accessibilityIdentifier("policy-\(policy.id)")
                        }
                        Button {
                            createsPolicy = true
                        } label: {
                            Label("New policy…", systemImage: "plus")
                        }
                    }
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
            .sheet(item: $editingPolicy) { policy in
                PolicyEditorView(model: model, policy: policy)
            }
            .sheet(isPresented: $createsPolicy) {
                PolicyEditorView(model: model)
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

    private func runnerPairingCommand(code: String) -> String {
        guard let server = model.session?.serverURL.absoluteString else { return code }
        return "state-runner pair --server \(server) --code \(code)"
    }

    private func chip(_ text: String) -> some View {
        Text(text)
            .font(.caption2.weight(.medium))
            .foregroundStyle(.secondary)
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(Color.secondary.opacity(0.12), in: Capsule())
    }

    private func projectName(for id: String) -> String {
        model.projects.first { $0.id == id }?.name ?? id
    }

    private func policySubtitle(_ policy: ExecutionPolicy) -> String {
        let mode = policy.mode == .supervised
            ? String(localized: "Supervised")
            : String(localized: "Unattended (low risk)")
        return "\(projectName(for: policy.projectID)) · \(policy.adapter) · \(mode)"
    }

    private func policyEnabledBinding(_ policy: ExecutionPolicy) -> Binding<Bool> {
        Binding(
            get: { policy.enabled },
            set: { enabled in
                var draft = PolicyDraft(policy: policy)
                draft.enabled = enabled
                Task { await model.updatePolicy(policy, draft: draft) }
            }
        )
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
