import Foundation

/// What a session is doing, derived on the server from its latest round.
enum AgentSessionStatus: String, Codable, Hashable, Sendable {
    case working
    case needsApproval = "needs_approval"
    case waiting
    case failed
    case closed
}

/// A round either carries the owner's message or opens the session in a
/// terminal on the Mac.
enum AgentTurnKind: String, Codable, Hashable, Sendable {
    case message
    case openTerminal = "open_terminal"
}

/// A conversation with a coding agent on the owner's Mac. Every message is
/// one round (an `AgentRun`); the agent's final message of a round is its
/// answer.
struct AgentSession: Codable, Hashable, Identifiable, Sendable {
    let id: String
    let reminderID: String
    let policyID: String
    let projectID: String
    var projectName: String?
    var adapter: String
    /// The T3 Code model the session runs with.
    var model: AgentModel? = nil
    var title: String
    var closed: Bool
    var closedAt: Date?
    var status: AgentSessionStatus
    var harnessSessionID: String?
    /// The session in the agent's own interface, for example its T3 Code
    /// thread. Set by the server only for an absolute https link.
    var harnessLink: String? = nil
    var lastActivityAt: Date?
    var revision: Int64
    var createdAt: Date
    var turns: [AgentRun]

    /// The round the runner is working on, if any.
    var activeTurn: AgentRun? { turns.last { !$0.isFinished } }

    /// The owner can write while the agent waits; a failed round does not
    /// end the session.
    var canSend: Bool { !closed && activeTurn == nil }

    var canOpenOnMac: Bool { canSend && resumeCommand != nil }

    /// The agent's latest answer, for the list.
    var lastAnswer: String? {
        turns.last { $0.turnKind != .openTerminal && !($0.resultText ?? "").isEmpty }?.resultText
    }

    var agentName: String { AgentNames.name(for: adapter) }

    /// The T3 Code thread (or another harness view) to open, if the server
    /// reported one.
    var harnessURL: URL? {
        guard let link = harnessLink, let url = URL(string: link), url.scheme == "https" else { return nil }
        return url
    }

    /// Sessions of T3 Code live in T3; there is no terminal to resume them in.
    var runsInT3: Bool { adapter == AgentNames.t3Code }

    /// The command that continues this session in a terminal, run in the
    /// project folder on the Mac.
    var resumeCommand: String? {
        guard let id = harnessSessionID, !id.isEmpty else { return nil }
        switch adapter {
        case "claude-code": return "claude --resume \(id)"
        case "codex": return "codex resume \(id)"
        case "kimi-code": return "kimi -S \(id)"
        default: return nil
        }
    }
}

struct AgentSessionListResponse: Codable, Sendable {
    let sessions: [AgentSession]
}

/// Display names of the agents a runner can start.
enum AgentNames {
    static let t3Code = "t3-code"

    static func name(for adapter: String) -> String {
        switch adapter {
        case "claude-code": "Claude Code"
        case "codex": "Codex"
        case "kimi-code": "Kimi Code"
        case "opencode": "OpenCode"
        case "pi-agent": "Pi"
        case "deepseek-harness": "DeepSeek Harness"
        case t3Code: "T3 Code"
        default: HarnessCatalog.displayLabel(for: adapter)
        }
    }
}

extension AgentRun {
    var isFinished: Bool {
        switch status {
        case .succeeded, .failed, .cancelled, .expired: true
        default: false
        }
    }
}
