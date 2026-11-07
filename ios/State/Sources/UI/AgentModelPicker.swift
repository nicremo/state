import SwiftUI

/// The switch "Nur T3 Code": while it is on and a T3 Code policy exists, the
/// app offers only T3 Code policies to start agents with.
enum AgentPreferences {
    static let t3OnlyKey = "state.agent.t3-only"
    static let lastModelKey = "state.agent.last-model"

    static func lastModel() -> AgentModel? {
        guard let data = UserDefaults.standard.data(forKey: lastModelKey) else { return nil }
        return try? JSONDecoder().decode(AgentModel.self, from: data)
    }

    static func remember(_ model: AgentModel?) {
        guard let model, let data = try? JSONEncoder().encode(model) else { return }
        UserDefaults.standard.set(data, forKey: lastModelKey)
    }
}

extension AppModel {
    /// The policies an agent can be started with. With "Nur T3 Code" on and
    /// at least one T3 Code policy, only those; otherwise every enabled one.
    func agentPolicies(t3Only: Bool) -> [ExecutionPolicy] {
        let t3 = sessionPolicies.filter { $0.adapter == AgentNames.t3Code }
        return t3Only && !t3.isEmpty ? t3 : sessionPolicies
    }

    var modelCatalog: ModelCatalog { ModelCatalog(runners: runners) }

    func project(for policy: ExecutionPolicy) -> Project? {
        projects.first { $0.id == policy.projectID }
    }
}

/// Picks the project an agent works in. A policy is shown by its project
/// folder, with the project's description underneath; T3 Code policies need
/// no agent name because the model is chosen separately.
struct AgentProjectPicker: View {
    @Bindable var model: AppModel
    let policies: [ExecutionPolicy]
    @Binding var policyID: String?
    var allowsNone = false

    var body: some View {
        Picker(selection: $policyID) {
            if allowsNone {
                Text("None").tag(nil as String?)
            }
            ForEach(policies) { policy in
                Text(label(for: policy)).tag(Optional(policy.id))
            }
        } label: {
            Text("Project")
        }
        .accessibilityIdentifier("agent-project-picker")
    }

    private func label(for policy: ExecutionPolicy) -> String {
        let project = model.project(for: policy)?.name ?? policy.name
        guard policy.adapter != AgentNames.t3Code else { return project }
        // Policies of other agents name their agent, so two of them in one
        // project stay apart.
        return "\(project) · \(AgentNames.name(for: policy.adapter))"
    }
}

/// Picks the T3 Code model, grouped by provider (Claude, Codex, Kimi,
/// DeepSeek). A stored model the catalog no longer lists stays selectable,
/// so opening the editor never changes it silently.
struct AgentModelPicker: View {
    let catalog: ModelCatalog
    @Binding var selection: AgentModel?

    var body: some View {
        Picker(selection: $selection) {
            if catalog.isEmpty || selection == nil {
                Text("T3 default").tag(nil as AgentModel?)
            }
            if let selection, catalog.entry(for: selection) == nil {
                Text(selection.model).tag(Optional(selection))
            }
            ForEach(catalog.providers, id: \.name) { provider in
                Section(provider.name) {
                    ForEach(provider.models) { entry in
                        Text(entry.modelName).tag(Optional(entry.agentModel))
                    }
                }
            }
        } label: {
            Text("Model")
        }
        .accessibilityIdentifier("agent-model-picker")
    }
}

/// The project's description under the pickers, so "karla-report" says what
/// it is.
struct AgentProjectFooter: View {
    let project: Project?
    let catalogIsEmpty: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: StateTheme.Space.tight) {
            if let description = project?.description, !description.isEmpty {
                Text(description)
            }
            if catalogIsEmpty {
                Text("The models appear as soon as your Mac reports them.")
            }
        }
    }
}
