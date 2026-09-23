import SwiftUI
import UserNotifications

struct SettingsView: View {
    @Bindable var model: AppModel
    @Binding var opensNotificationSettings: Bool

    @State private var harnessSelection = "codex"
    @State private var customHarness = ""
    @State private var harnessName = HarnessCatalog.suggestedName(for: "codex")
    @State private var editedName = false
    @State private var pairingCode: PairingCode?
    @State private var runnerName = ""
    @State private var runnerPairingCode: PairingCode?
    @State private var editingPolicy: ExecutionPolicy?
    @State private var createsPolicy = false
    @State private var isCreatingCode = false
    @State private var copiedCommand = false
    @State private var confirmsDisconnect = false
    @State private var relayInput = ""
    @State private var relayError: String?

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
                connectionSection
                syncSection

                if model.session?.actor.kind == .owner {
                    Section("Agents") {
                        NavigationLink {
                            List {
                                agentSection
                                actorSection(title: "Connected agents", actors: model.agents)
                            }
                            .navigationTitle("Agents")
                            .stateInlineNavigationTitle()
                            .stateBackground()
                        } label: {
                            SettingsRowLabel(title: "Agents", systemImage: "terminal", count: model.agents.count)
                        }

                        NavigationLink {
                            List {
                                actorSection(title: "Devices", actors: model.devices.filter { $0.id != model.session?.actor.id })
                            }
                            .navigationTitle("Devices")
                            .stateInlineNavigationTitle()
                            .stateBackground()
                        } label: {
                            SettingsRowLabel(title: "Devices", systemImage: "iphone", count: model.devices.count)
                        }

                        NavigationLink {
                            List { executionSections }
                                .navigationTitle("Agent tasks")
                                .stateInlineNavigationTitle()
                                .stateBackground()
                        } label: {
                            SettingsRowLabel(title: "Agent tasks", systemImage: "play.circle", count: model.policies.count)
                        }
                    }
                }

                Section("Notifications") {
                    NavigationLink {
                        NotificationSettingsView()
                    } label: {
                        Label("Delivery and privacy", systemImage: "bell.badge")
                    }
                    #if os(iOS)
                    NavigationLink {
                        List { pushRelaySection }
                            .navigationTitle("Push relay")
                            .stateInlineNavigationTitle()
                            .stateBackground()
                    } label: {
                        Label("Push relay", systemImage: "antenna.radiowaves.left.and.right")
                    }
                    #endif
                }
                .navigationDestination(isPresented: $opensNotificationSettings) {
                    NotificationSettingsView()
                }

                Section("Help") {
                    NavigationLink {
                        DocumentationView()
                    } label: {
                        Label("Documentation", systemImage: "book")
                    }
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
            .sheet(item: $editingPolicy) { policy in
                PolicyEditorView(model: model, policy: policy)
            }
            .sheet(isPresented: $createsPolicy) {
                PolicyEditorView(model: model)
            }
        }
    }

    /// Runners and execution policies: the machinery behind scheduled agent
    /// tasks, one level down because most days nobody touches it.
    @ViewBuilder
    private var executionSections: some View {
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
                            runnerStatus(for: runner)
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
                            Platform.copyToPasteboard(runnerPairingCommand(code: runnerPairingCode.code))
                        } label: {
                            Label("Copy state-runner command", systemImage: "doc.on.doc")
                        }
                        Text("Run the command on the machine that should execute runs. state-runner stores the credential there.")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        Text("Adjust $HOME/Projects to the folder that holds your checkouts. The command pairs the runner and installs the launch agent.")
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

    // MARK: Push relay

    #if os(iOS)
    /// The relay address belongs to the server session, so this section only
    /// edits the stored session and then asks APNs for the device token again.
    @ViewBuilder
    private var pushRelaySection: some View {
        if let session = model.session, !model.isDemo {
            Section {
                HStack(spacing: StateTheme.Space.group) {
                    Image(systemName: session.relayURL == nil ? "wifi.slash" : "antenna.radiowaves.left.and.right")
                        .font(.system(size: 15, weight: .medium))
                        .foregroundStyle(session.relayURL == nil ? Color.secondary : StateTheme.accent)
                        .frame(width: 34, height: 34)
                        .background(StateTheme.accentSoft, in: RoundedRectangle(cornerRadius: 10, style: .continuous))

                    VStack(alignment: .leading, spacing: StateTheme.Space.hairline) {
                        Text(session.relayURL?.absoluteString
                            ?? String(localized: "No relay: notifications only locally and in the home network"))
                            .font(.subheadline)
                            .foregroundStyle(StateTheme.graphite)
                            .lineLimit(2)
                            .truncationMode(.middle)
                        relayStateText
                    }
                }
                .padding(.vertical, StateTheme.Space.tight)

                TextField("https://relay.example.com", text: $relayInput)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .keyboardType(.URL)
                    .onAppear { relayInput = session.relayURL?.absoluteString ?? "" }

                Button("Save") { saveRelay() }
                    .disabled(relayInput.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)

                if session.relayURL != nil {
                    Button("Remove", role: .destructive) { removeRelay() }
                }

                if let relayError {
                    Text(relayError)
                        .font(.caption)
                        .foregroundStyle(.orange)
                }
            } header: {
                Text("Push relay")
            } footer: {
                Text("With a public State relay, for example on your VPS, your iPhone receives notifications outside the home network. The content stays end to end encrypted.")
            }
        }
    }

    @ViewBuilder
    private var relayStateText: some View {
        switch model.pushStatus {
        case .registered:
            Text("Notifications arrive through this relay.")
                .font(.caption)
                .foregroundStyle(.secondary)
        case .unavailable:
            Text("Push registration is not available on this device.")
                .font(.caption)
                .foregroundStyle(.secondary)
        case let .failed(message):
            Text(message)
                .font(.caption)
                .foregroundStyle(.orange)
        case .unknown, .noRelay:
            // The header line already names the missing relay.
            EmptyView()
        }
    }

    private func saveRelay() {
        let trimmed = relayInput.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: trimmed), PushRegistrationService.isUsableRelay(url) else {
            relayError = String(localized: "Enter a complete https:// address.")
            return
        }
        relayError = nil
        relayInput = url.absoluteString
        model.updateRelayURL(url)
        registerForPush()
    }

    private func removeRelay() {
        relayError = nil
        relayInput = ""
        model.updateRelayURL(nil)
        registerForPush()
    }

    /// The relay changed, so the route has to be registered again. APNs only
    /// hands the token to the app delegate, so asking for it repeats that flow.
    private func registerForPush() {
        UIApplication.shared.registerForRemoteNotifications()
    }
    #endif

    // MARK: Agents

    private var agentSection: some View {
        Section {
            Picker("Agent", selection: $harnessSelection) {
                ForEach(HarnessCatalog.presets, id: \.id) { preset in
                    Text(preset.label).tag(preset.id)
                }
                Text("Other").tag(HarnessCatalog.customTag)
            }
            .onChange(of: harnessSelection) { _, _ in refreshSuggestedName() }

            if harnessSelection == HarnessCatalog.customTag {
                TextField("Harness identifier", text: $customHarness)
                    .stateNoAutocapitalization()
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
                Platform.copyToPasteboard(pairingCommand(code: code.code))
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

    private func runnerPairingCommand(code: String) -> String {
        guard let server = model.session?.serverURL.absoluteString else { return code }
        let typedName = runnerName.trimmingCharacters(in: .whitespacesAndNewlines)
        let name = typedName.isEmpty ? "mac-runner" : typedName
        return "state-runner pair --server \(server) --code \(code) --name \(name) --adapters claude-code,codex --work-root \"$HOME/Projects\" && state-runner service install"
    }

    @ViewBuilder
    private func runnerStatus(for runner: Runner) -> some View {
        let online = runner.isOnline()
        HStack(spacing: 5) {
            Circle()
                .fill(online ? Color.green : Color.secondary.opacity(0.45))
                .frame(width: 8, height: 8)
            if online {
                Text("Online")
            } else {
                Text(String(localized: "Last seen")) + Text(" ") + Text(runner.lastSeenAt, style: .relative)
            }
        }
        .font(.caption)
        .foregroundStyle(.secondary)
        .accessibilityIdentifier("runner-status-\(runner.id)")
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
                    if let url = Platform.notificationSettingsURL {
                        openURL(url)
                    }
                }
            }
        }
        .navigationTitle("Notifications")
        .stateInlineNavigationTitle()
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

extension Runner {
    /// A runner is online while its last heartbeat is younger than this window.
    static let onlineWindow: TimeInterval = 120

    /// isOnline decides the dot next to a runner row. A timestamp from the
    /// future counts as online, because it only means clock skew.
    static func isOnline(lastSeenAt: Date?, now: Date) -> Bool {
        guard let lastSeenAt else { return false }
        return now.timeIntervalSince(lastSeenAt) < onlineWindow
    }

    func isOnline(now: Date = Date()) -> Bool {
        Runner.isOnline(lastSeenAt: lastSeenAt, now: now)
    }
}

/// A settings row that leads to a sub page and shows how many items it holds.
private struct SettingsRowLabel: View {
    let title: LocalizedStringKey
    let systemImage: String
    let count: Int

    var body: some View {
        LabeledContent {
            if count > 0 {
                Text(count, format: .number)
                    .foregroundStyle(.secondary)
            }
        } label: {
            Label(title, systemImage: systemImage)
        }
    }
}
