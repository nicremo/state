import SwiftUI

/// Starts an agent session. From a reminder it works on that reminder; from
/// the Agent tab the owner types a new task, which becomes a reminder first so
/// it keeps its history. The owner picks the project and, for T3 Code, the
/// model; both default to what was used last. When the Mac is out of reach
/// the start waits and goes out after the next sync.
struct StartAgentSessionSheet: View {
    @Bindable var model: AppModel
    /// The reminder to work on; nil for a new task typed here.
    let reminder: Reminder?
    @Environment(\.dismiss) private var dismiss
    @AppStorage("state.agent.last-policy") private var lastPolicyID = ""
    @AppStorage(AgentPreferences.t3OnlyKey) private var t3Only = true
    @State private var policyID: String?
    @State private var agentModel: AgentModel?
    @State private var task = ""
    @State private var instruction = ""
    @State private var starting = false
    @FocusState private var taskFocused: Bool

    private var policies: [ExecutionPolicy] { model.agentPolicies(t3Only: t3Only) }
    private var policy: ExecutionPolicy? { policies.first { $0.id == policyID } }
    private var catalog: ModelCatalog { model.modelCatalog }
    private var usesModel: Bool { policy?.adapter == AgentNames.t3Code }

    private var canStart: Bool {
        guard policy != nil, !starting else { return false }
        return reminder != nil || !task.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    var body: some View {
        NavigationStack {
            Form {
                taskSection
                if policies.isEmpty {
                    Section {
                        Text("No agent is set up yet. The owner sets one up on the Mac with state-server agent-project: it names the project folder, the agent and what it may do.")
                            .font(.callout)
                            .foregroundStyle(.secondary)
                    }
                } else {
                    Section {
                        if policies.count > 1 {
                            AgentProjectPicker(model: model, policies: policies, policyID: $policyID)
                        } else if let only = policies.first {
                            LabeledContent(String(localized: "Project"), value: model.project(for: only)?.name ?? only.name)
                        }
                        if usesModel {
                            AgentModelPicker(catalog: catalog, selection: $agentModel)
                        }
                    } footer: {
                        if let policy {
                            VStack(alignment: .leading, spacing: StateTheme.Space.tight) {
                                AgentProjectFooter(project: model.project(for: policy), catalogIsEmpty: usesModel && catalog.isEmpty)
                                if !usesModel {
                                    Text(AgentText.rights(policy.allowedCapabilities))
                                }
                            }
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
            .onAppear(perform: preselect)
            .onChange(of: policyID) { _, _ in
                if usesModel, agentModel == nil { agentModel = defaultModel }
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
                    Text(AgentText.rowPreview(description))
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

    /// The reminder's own model, else the last one used, else Claude Sonnet.
    private var defaultModel: AgentModel? {
        reminder?.executionModel ?? AgentPreferences.lastModel() ?? catalog.suggested
    }

    private func preselect() {
        guard policyID == nil else { return }
        let preferred = policies.first { $0.id == reminder?.executionPolicyID }
            ?? policies.first { $0.id == lastPolicyID }
            ?? policies.first
        policyID = preferred?.id
        if preferred?.adapter == AgentNames.t3Code { agentModel = defaultModel }
        if reminder == nil { taskFocused = true }
    }

    private func start() async {
        guard let policy else { return }
        starting = true
        let chosenModel = usesModel ? agentModel : nil
        let result: AgentStartResult
        if let reminder {
            result = await model.startAgentSession(reminderID: reminder.id, policyID: policy.id, instruction: instruction, model: chosenModel)
        } else {
            result = await model.startAgentTask(title: task, policyID: policy.id, model: chosenModel)
        }
        starting = false
        if result.succeeded {
            lastPolicyID = policy.id
            AgentPreferences.remember(chosenModel)
            dismiss()
        }
    }
}
