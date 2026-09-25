import SwiftUI

/// "Jetzt arbeiten lassen": picks the agent and project (a policy) for a
/// reminder, takes an optional instruction and starts the session. The new
/// session opens in the Agent tab.
struct StartAgentSessionSheet: View {
    @Bindable var model: AppModel
    let reminder: Reminder
    @Environment(\.dismiss) private var dismiss
    @State private var policyID: String?
    @State private var instruction = ""
    @State private var starting = false

    private var policy: ExecutionPolicy? { model.sessionPolicies.first { $0.id == policyID } }

    var body: some View {
        NavigationStack {
            Form {
                Section(String(localized: "Task")) {
                    Text(reminder.title)
                        .font(.headline)
                    if let description = reminder.description, !description.isEmpty {
                        Text(description)
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                            .lineLimit(5)
                    }
                }
                if model.sessionPolicies.isEmpty {
                    Section {
                        Text("No agent is set up yet. The owner sets one up on the Mac with state-server agent-project: it names the project folder, the agent and what it may do.")
                            .font(.callout)
                            .foregroundStyle(.secondary)
                    }
                } else {
                    Section {
                        Picker(String(localized: "Agent"), selection: $policyID) {
                            ForEach(model.sessionPolicies) { policy in
                                Text(label(for: policy)).tag(Optional(policy.id))
                            }
                        }
                        .accessibilityIdentifier("start-agent-policy")
                    } footer: {
                        if let policy {
                            Text(AgentText.rights(policy.allowedCapabilities))
                        }
                    }
                    Section {
                        TextField(String(localized: "Extra instruction (optional)"), text: $instruction, axis: .vertical)
                            .lineLimit(3...8)
                            .accessibilityIdentifier("start-agent-instruction")
                    } footer: {
                        Text("The agent starts on your Mac in the project folder. Its answer appears in the Agent tab, where you can keep writing with it.")
                    }
                }
            }
            .formStyle(.grouped)
            .navigationTitle(String(localized: "Let an agent work on it"))
            .stateInlineNavigationTitle()
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button(String(localized: "Cancel")) { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button {
                        Task { await start() }
                    } label: {
                        if starting {
                            ProgressView()
                        } else {
                            Text("Start")
                        }
                    }
                    .disabled(policy == nil || starting)
                    .accessibilityIdentifier("start-agent-confirm")
                }
            }
            .onAppear {
                guard policyID == nil else { return }
                let preferred = model.sessionPolicies.first { $0.id == reminder.executionPolicyID }
                policyID = (preferred ?? model.sessionPolicies.first)?.id
            }
        }
    }

    private func label(for policy: ExecutionPolicy) -> String {
        let project = model.projects.first { $0.id == policy.projectID }?.name
        return [AgentNames.name(for: policy.adapter), project].compactMap { $0 }.joined(separator: " · ")
    }

    private func start() async {
        guard let policy else { return }
        starting = true
        let session = await model.startAgentSession(reminderID: reminder.id, policyID: policy.id, instruction: instruction)
        starting = false
        if session != nil {
            dismiss()
        }
    }
}
