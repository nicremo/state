import Foundation

/// The agent labels State offers by name, plus the rule the server applies to
/// any other label. Keeping the rule here lets the owner pair an agent State
/// has never heard of without waiting for a server release.
enum HarnessCatalog {
    /// Picker tag for a label the owner types instead of choosing.
    static let customTag = "__custom__"

    /// Agents State names in the picker. Any other valid label pairs the same
    /// way, it only needs its MCP server entry added by hand.
    static let presets: [(id: String, label: String)] = [
        ("codex", "Codex"),
        ("claude-code", "Claude Code"),
        ("opencode", "OpenCode"),
        ("pi", "Pi"),
        ("deepseek-harness", "DeepSeek Harness"),
    ]

    /// Adapters a runner can launch for an execution policy. These are the
    /// names in DefaultAdapters (internal/runner/adapters.go), which differ
    /// from pairing labels: Pi pairs as `pi` but runs as `pi-agent`.
    static let adapterPresets: [(id: String, label: String)] = [
        ("claude-code", "Claude Code"),
        ("codex", "Codex"),
        ("opencode", "OpenCode"),
        ("pi-agent", "Pi"),
        ("deepseek-harness", "DeepSeek Harness"),
    ]

    /// The name State proposes for a newly paired agent. The owner sees the
    /// agent under this name in the audit history, so a sensible default beats
    /// an empty field that blocks the button.
    static func suggestedName(for harness: String) -> String {
        let base = presets.first { $0.id == harness }?.label ?? displayLabel(for: harness)
        return String(format: String(localized: "%@ Agent"), base)
    }

    /// Turns a raw label into something readable: `claude-code` becomes
    /// `Claude Code`. Used for agents State has no preset for.
    static func displayLabel(for harness: String) -> String {
        harness
            .split(separator: "-")
            .map { $0.prefix(1).uppercased() + $0.dropFirst() }
            .joined(separator: " ")
    }

    /// Agents whose configuration file statectl writes on its own. Mirrors
    /// knownHarnesses in internal/state/harness.go.
    static let shippedIntegrations: Set<String> = ["codex", "claude-code", "opencode"]

    private static let allowed = Set("abcdefghijklmnopqrstuvwxyz0123456789-")

    /// Reports whether statectl configures this agent without manual work.
    static func hasShippedIntegration(_ harness: String) -> Bool {
        shippedIntegrations.contains(harness)
    }

    /// Mirrors ValidHarness in internal/state/harness.go. Two to thirty-two
    /// characters, lower case letters, digits and inner hyphens. Rejecting the
    /// same labels on both sides keeps a typo from creating a second agent.
    static func isValid(_ harness: String) -> Bool {
        guard (2...32).contains(harness.count) else { return false }
        guard harness.allSatisfy(allowed.contains) else { return false }
        return harness.first != "-" && harness.last != "-"
    }

    /// Normalizes owner input before it reaches the server.
    static func normalize(_ harness: String) -> String {
        harness.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    }
}
