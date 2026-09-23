import Foundation

/// Builds the terminal command the Mac Server app copies for a pairing code.
/// Harnesses pair through the bundled statectl; a runner pairs through the
/// separately installed state-runner and then installs its launch agent.
public enum PairingCommand {
    /// Picker value that selects a runner instead of a harness label.
    public static let runnerSelection = "runner"

    public static func isRunner(_ selection: String) -> Bool {
        selection == runnerSelection
    }

    public static func forSelection(_ selection: String, code: String, localURL: String, statectlPath: String) -> String {
        if isRunner(selection) {
            // Most of the owner's checkouts live on the Desktop; the hint under
            // the button says to adjust --work-root before running it.
            return "state-runner pair --server \(quote(localURL)) --code \(quote(code)) --name 'Mac Runner' --adapters claude-code,codex --work-root \"$HOME/Desktop\" && state-runner service install"
        }
        return "\(quote(statectlPath)) pair --server \(quote(localURL)) --code \(quote(code)) --harness \(quote(selection)) --profile \(quote(selection))"
    }

    public static func quote(_ value: String) -> String {
        "'" + value.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }
}
