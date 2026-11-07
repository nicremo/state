import Foundation

/// A model in T3 Code: the provider instance (for example
/// `claudeAgent_claude_hub`) and the provider's model slug (for example
/// `claude-sonnet-5-5`). The server and the runner pass both on unchanged.
struct AgentModel: Codable, Hashable, Sendable {
    var provider: String
    var model: String
}

/// One entry of the model catalog a runner read from T3 Code.
struct HarnessModel: Codable, Hashable, Identifiable, Sendable {
    var provider: String
    var providerName: String
    var model: String
    var modelName: String
    var isDefault: Bool

    var id: String { provider + "\u{0}" + model }
    var agentModel: AgentModel { AgentModel(provider: provider, model: model) }

    private enum CodingKeys: String, CodingKey {
        case provider
        case providerName
        case model
        case modelName
        case isDefault = "default"
    }

    init(provider: String, providerName: String, model: String, modelName: String, isDefault: Bool = false) {
        self.provider = provider
        self.providerName = providerName
        self.model = model
        self.modelName = modelName
        self.isDefault = isDefault
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        provider = try container.decode(String.self, forKey: .provider)
        providerName = try container.decode(String.self, forKey: .providerName)
        model = try container.decode(String.self, forKey: .model)
        modelName = try container.decode(String.self, forKey: .modelName)
        isDefault = try container.decodeIfPresent(Bool.self, forKey: .isDefault) ?? false
    }
}

/// The models all runners offer, grouped by provider in a stable order, and
/// the lookups the pickers need.
struct ModelCatalog: Sendable {
    let models: [HarnessModel]

    init(runners: [Runner]) {
        var seen = Set<String>()
        var collected: [HarnessModel] = []
        for runner in runners {
            for model in runner.harnessModels where seen.insert(model.id).inserted {
                collected.append(model)
            }
        }
        models = collected
    }

    var isEmpty: Bool { models.isEmpty }

    /// Providers in the order the owner thinks of them: Claude, Codex, Kimi,
    /// DeepSeek, then the rest by name.
    var providers: [(name: String, models: [HarnessModel])] {
        let grouped = Dictionary(grouping: models, by: \.providerName)
        return grouped.keys
            .sorted { Self.rank($0) == Self.rank($1) ? $0 < $1 : Self.rank($0) < Self.rank($1) }
            .map { ($0, grouped[$0] ?? []) }
    }

    func entry(for model: AgentModel?) -> HarnessModel? {
        guard let model else { return nil }
        return models.first { $0.provider == model.provider && $0.model == model.model }
    }

    /// A readable name for a stored model, even when the catalog no longer
    /// lists it.
    func label(for model: AgentModel?) -> String? {
        guard let model else { return nil }
        if let entry = entry(for: model) { return entry.modelName }
        return model.model
    }

    /// The model a new assignment starts with: Claude Sonnet if offered,
    /// else the first provider's default.
    var suggested: AgentModel? {
        if let sonnet = models.first(where: { $0.model.contains("sonnet") && $0.providerName.lowercased().contains("claude") }) {
            return sonnet.agentModel
        }
        let first = providers.first?.models
        return (first?.first(where: \.isDefault) ?? first?.first)?.agentModel
    }

    private static func rank(_ providerName: String) -> Int {
        let name = providerName.lowercased()
        if name.contains("claude") { return 0 }
        if name.contains("codex") { return 1 }
        if name.contains("kimi") { return 2 }
        if name.contains("deepseek") || name.contains("dsh") { return 3 }
        return 4
    }
}

/// What happened to a start request.
enum AgentStartResult: Sendable {
    case started(AgentSession)
    /// The Mac was out of reach; the start waits and goes out after the next
    /// successful sync.
    case queued
    case failed

    var succeeded: Bool {
        if case .failed = self { return false }
        return true
    }
}

/// A start that waits for the Mac.
struct PendingAgentStart: Codable, Hashable, Identifiable, Sendable {
    /// The request ID the server deduplicates on.
    let id: String
    var reminderID: String
    var title: String
    var policyID: String
    var model: AgentModel?
    var instruction: String
    var createdAt: Date
}
