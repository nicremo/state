import SwiftUI

/// Starts an agent session. From a reminder it works on that reminder; from
/// the Agent tab the owner types a new task, which becomes a reminder first so
/// it keeps its history. The agent and project (a policy) default to the last
/// one used; with a single policy there is nothing to choose. The new session
/// opens in the Agent tab.
struct StartAgentSessionSheet: View {
    @Bindable var model: AppModel
    /// The reminder to work on; nil for a new task typed here.
    let reminder: Reminder?
    @Environment(\.dismiss) private var dismiss
    @AppStorage("state.agent.last-policy") private var lastPolicyID = ""
    @State private var policyID: String?
    @State private var task = ""
    @State private var instruction = ""
    @State private var starting = false
    @FocusState private var taskFocused: Bool

    private var policy: ExecutionPolicy? { model.sessionPolicies.first { $0.id == policyID } }

    private var canStart: Bool {
        guard policy != nil, !starting else { return false }
        return reminder != nil || !task.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    var body: some View {
        NavigationStack {
            Form {
                taskSection
                if model.sessionPolicies.isEmpty {
                    Section {
                        Text("No agent is set up yet. The owner sets one up on the Mac with state-server agent-project: it names the project folder, the agent and what it may do.")
                            .font(.callout)
                            .foregroundStyle(.secondary)
                    }
                } else {
                    Section {
                        if model.sessionPolicies.count == 1, let only = model.sessionPolicies.first {
                            LabeledContent(String(localized: "Agent"), value: label(for: only))
                        } else {
                            Picker(String(localized: "Agent"), selection: $policyID) {
                                ForEach(model.sessionPolicies) { policy in
                                    Text(label(for: policy)).tag(Optional(policy.id))
                                }
                            }
                            .accessibilityIdentifier("start-agent-policy")
                        }
                    } footer: {
                        if let policy {
                            Text(footer(for: policy))
                        }
                    }
                    if reminder != nil {
                        Section {
                            TextField(String(localized: "Extra instruction (optional)"), text: $instruction, axis: .vertical)
                                .lineLimit(2...6)
                                .accessibilityIdentifier("start-agent-instruction")
                        }
                    }
                }
            }
            .formStyle(.grouped)
            .navigationTitle(reminder == nil ? String(localized: "New task") : String(localized: "Let an agent work on it"))
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
                    .disabled(!canStart)
                    .accessibilityIdentifier("start-agent-confirm")
                }
            }
            .onAppear {
                guard policyID == nil else { return }
                let policies = model.sessionPolicies
                let preferred = policies.first { $0.id == reminder?.executionPolicyID }
                    ?? policies.first { $0.id == lastPolicyID }
                    ?? policies.first
                policyID = preferred?.id
                if reminder == nil { taskFocused = true }
            }
        }
    }

    @ViewBuilder
    private var taskSection: some View {
        if let reminder {
            Section(String(localized: "Task")) {
                Text(reminder.title)
                    .font(.headline)
                if let description = reminder.description, !description.isEmpty {
                    Text(AgentText.preview(description))
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .lineLimit(4)
                }
            }
        } else {
            Section {
                TextField(String(localized: "What should the agent do?"), text: $task, axis: .vertical)
                    .lineLimit(3...10)
                    .focused($taskFocused)
                    .accessibilityIdentifier("start-agent-task")
            } footer: {
                Text("Becomes a reminder without a date, so the task keeps its history.")
            }
        }
    }

    /// "Agent · Project", plus the policy name when two policies would read
    /// the same (for example one that reads and one that edits).
    private func label(for policy: ExecutionPolicy) -> String {
        let project = model.projects.first { $0.id == policy.projectID }?.name
        let base = [AgentNames.name(for: policy.adapter), project].compactMap { $0 }.joined(separator: " · ")
        let twins = model.sessionPolicies.filter { $0.adapter == policy.adapter && $0.projectID == policy.projectID }
        return twins.count > 1 ? "\(base) (\(policy.name))" : base
    }

    private func footer(for policy: ExecutionPolicy) -> String {
        if policy.adapter == AgentNames.t3Code {
            return String(localized: "Runs as a thread in T3 Code on your Mac. You can follow it there or open it from the session.")
        }
        return AgentText.rights(policy.allowedCapabilities)
    }

    private func start() async {
        guard let policy else { return }
        starting = true
        let session: AgentSession?
        if let reminder {
            session = await model.startAgentSession(reminderID: reminder.id, policyID: policy.id, instruction: instruction)
        } else {
            session = await model.startAgentTask(title: task, policyID: policy.id)
        }
        starting = false
        if session != nil {
            lastPolicyID = policy.id
            dismiss()
        }
    }
}
