import SwiftUI

/// The closed capability enum from internal/state/execution_models.go. The
/// server rejects anything outside this list, so the editor offers exactly
/// these nine.
enum CapabilityCatalog {
    static let all: [String] = [
        "read_repository",
        "edit_repository",
        "run_tests",
        "read_state_context",
        "write_state",
        "network_access",
        "deploy",
        "message_external",
        "destructive",
    ]

    /// The allow-list a policy in unattended-low-risk mode may carry; every
    /// other capability requires supervised mode.
    static let unattendedLowRisk: Set<String> = [
        "read_repository",
        "read_state_context",
        "run_tests",
        "edit_repository",
    ]

    static func label(for capability: String) -> String {
        switch capability {
        case "read_repository": String(localized: "Read repository")
        case "edit_repository": String(localized: "Edit repository")
        case "run_tests": String(localized: "Run tests")
        case "read_state_context": String(localized: "Read State context")
        case "write_state": String(localized: "Write State data")
        case "network_access": String(localized: "Network access")
        case "deploy": String(localized: "Deploy and publish")
        case "message_external": String(localized: "Message external services")
        case "destructive": String(localized: "Destructive actions")
        default: capability
        }
    }
}

extension PolicyDraft {
    init(policy: ExecutionPolicy) {
        self.init()
        name = policy.name
        projectID = policy.projectID
        adapter = policy.adapter
        mode = policy.mode
        allowedCapabilities = policy.allowedCapabilities
        markOccurrenceDoneOnSuccess = policy.markOccurrenceDoneOnSuccess
        notifyOnStart = policy.notifyOnStart
        notifyOnCompletion = policy.notifyOnCompletion
        notifyOnFailure = policy.notifyOnFailure
        timeoutMinutes = policy.timeoutMinutes
        enabled = policy.enabled
    }

    /// Mirrors ValidProjectSlug in internal/state/project.go: two to
    /// sixty-four characters, lower case letters, digits and inner hyphens.
    static func isValidName(_ name: String) -> Bool {
        guard (2...64).contains(name.count) else { return false }
        let allowed = Set("abcdefghijklmnopqrstuvwxyz0123456789-")
        guard name.allSatisfy(allowed.contains) else { return false }
        return name.first != "-" && name.last != "-"
    }

    /// Capabilities the chosen mode may not carry, mirroring the server-side
    /// unattended allow-list. Empty for supervised policies.
    var disallowedCapabilities: [String] {
        guard mode == .unattendedLowRisk else { return [] }
        return allowedCapabilities.filter { !CapabilityCatalog.unattendedLowRisk.contains($0) }
    }

    var isValid: Bool {
        Self.isValidName(name.trimmingCharacters(in: .whitespacesAndNewlines))
            && !projectID.isEmpty
            && HarnessCatalog.isValid(adapter)
            && disallowedCapabilities.isEmpty
    }
}

struct PolicyEditorView: View {
    @Environment(\.dismiss) private var dismiss
    @Bindable var model: AppModel
    @State private var draft: PolicyDraft
    @State private var adapterSelection: String
    @State private var customAdapter: String
    @State private var isSaving = false
    private let policy: ExecutionPolicy?
    private let title: LocalizedStringKey

    init(model: AppModel, policy: ExecutionPolicy? = nil) {
        self.model = model
        self.policy = policy
        let draft = policy.map { PolicyDraft(policy: $0) } ?? PolicyDraft()
        _draft = State(initialValue: draft)
        // A label outside the shipped presets starts in the custom field.
        if HarnessCatalog.presets.contains(where: { $0.id == draft.adapter }) {
            _adapterSelection = State(initialValue: draft.adapter)
            _customAdapter = State(initialValue: "")
        } else {
            _adapterSelection = State(initialValue: HarnessCatalog.customTag)
            _customAdapter = State(initialValue: draft.adapter)
        }
        title = policy == nil ? "New policy" : "Edit policy"
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("Policy") {
                    TextField("Name", text: $draft.name)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    if !draft.name.isEmpty, !PolicyDraft.isValidName(draft.name) {
                        Text("Use 2 to 64 characters: lower case letters, digits and inner hyphens.")
                            .font(.caption)
                            .foregroundStyle(.red)
                    }
                    if policy == nil {
                        Picker("Project", selection: $draft.projectID) {
                            Text("Choose a project").tag("")
                            ForEach(model.projects) { project in
                                Text(project.name).tag(project.id)
                            }
                        }
                    } else {
                        LabeledContent("Project", value: projectName)
                    }
                    Toggle("Enabled", isOn: $draft.enabled)
                }

                Section("Adapter") {
                    Picker("Adapter", selection: $adapterSelection) {
                        ForEach(HarnessCatalog.presets, id: \.id) { preset in
                            Text(preset.label).tag(preset.id)
                        }
                        Text("Other").tag(HarnessCatalog.customTag)
                    }
                    if adapterSelection == HarnessCatalog.customTag {
                        TextField("Adapter identifier", text: $customAdapter)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                        if !customAdapter.isEmpty, !HarnessCatalog.isValid(HarnessCatalog.normalize(customAdapter)) {
                            Text("Use 2 to 32 characters: lower case letters, digits and inner hyphens.")
                                .font(.caption)
                                .foregroundStyle(.red)
                        }
                    }
                }

                Section {
                    Picker("Mode", selection: $draft.mode) {
                        Text("Supervised").tag(ExecutionMode.supervised)
                        Text("Unattended (low risk)").tag(ExecutionMode.unattendedLowRisk)
                    }
                } footer: {
                    Text("Supervised runs may use every selected capability and ask before risky steps. Unattended runs never ask, so only low-risk capabilities are allowed.")
                }

                Section("Capabilities") {
                    ForEach(CapabilityCatalog.all, id: \.self) { capability in
                        let blocked = isBlocked(capability)
                        Button {
                            toggle(capability)
                        } label: {
                            HStack {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(CapabilityCatalog.label(for: capability))
                                        .foregroundStyle(StateTheme.graphite)
                                    if blocked {
                                        Text("Requires supervised mode")
                                            .font(.caption)
                                            .foregroundStyle(.secondary)
                                    }
                                }
                                Spacer()
                                if draft.allowedCapabilities.contains(capability) {
                                    Image(systemName: "checkmark")
                                        .foregroundStyle(StateTheme.accent)
                                }
                            }
                        }
                        .disabled(blocked)
                        .accessibilityIdentifier("capability-\(capability)")
                    }
                }

                Section("Automation") {
                    Toggle("Mark occurrence done on success", isOn: $draft.markOccurrenceDoneOnSuccess)
                    Stepper(value: $draft.timeoutMinutes, in: 1...240, step: 5) {
                        LabeledContent("Timeout", value: "\(draft.timeoutMinutes) min")
                    }
                }

                Section("Notifications") {
                    Toggle("Notify on start", isOn: $draft.notifyOnStart)
                    Toggle("Notify on completion", isOn: $draft.notifyOnCompletion)
                    Toggle("Notify on failure", isOn: $draft.notifyOnFailure)
                }
            }
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .interactiveDismissDisabled(isSaving)
            .task {
                // The project picker reads the synced list; a fresh install may
                // not have pulled it yet.
                if model.projects.isEmpty {
                    await model.synchronize()
                }
            }
            .onChange(of: adapterSelection) { _, selection in
                if selection != HarnessCatalog.customTag {
                    draft.adapter = selection
                } else {
                    draft.adapter = HarnessCatalog.normalize(customAdapter)
                }
            }
            .onChange(of: customAdapter) { _, custom in
                if adapterSelection == HarnessCatalog.customTag {
                    draft.adapter = HarnessCatalog.normalize(custom)
                }
            }
            .onChange(of: draft.mode) { _, mode in
                // Switching to unattended silently drops the capabilities the
                // mode cannot run; the rows read as disabled either way.
                if mode == .unattendedLowRisk {
                    draft.allowedCapabilities = draft.allowedCapabilities.filter {
                        CapabilityCatalog.unattendedLowRisk.contains($0)
                    }
                }
            }
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                        .disabled(isSaving)
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") {
                        isSaving = true
                        Task {
                            if let policy {
                                await model.updatePolicy(policy, draft: draft)
                            } else {
                                await model.createPolicy(draft)
                            }
                            dismiss()
                        }
                    }
                    .disabled(!draft.isValid || isSaving)
                    .accessibilityIdentifier("save-policy")
                }
            }
        }
    }

    private var projectName: String {
        guard let projectID = policy?.projectID else { return "" }
        return model.projects.first { $0.id == projectID }?.name ?? projectID
    }

    private func isBlocked(_ capability: String) -> Bool {
        draft.mode == .unattendedLowRisk && !CapabilityCatalog.unattendedLowRisk.contains(capability)
    }

    private func toggle(_ capability: String) {
        if draft.allowedCapabilities.contains(capability) {
            draft.allowedCapabilities.removeAll { $0 == capability }
        } else {
            draft.allowedCapabilities.append(capability)
        }
    }
}
