import Testing
@testable import StateServerCore

struct PairingCommandTests {
    @Test func harnessCommandUsesTheBundledStatectl() {
        let command = PairingCommand.forSelection(
            "claude-code", code: "abc", localURL: "http://127.0.0.1:9848", statectlPath: "/Apps/State Server.app/statectl")
        #expect(command == "'/Apps/State Server.app/statectl' pair --server 'http://127.0.0.1:9848' --code 'abc' --harness 'claude-code' --profile 'claude-code'")
    }

    @Test func runnerCommandPairsAndInstallsTheService() {
        let command = PairingCommand.forSelection(
            PairingCommand.runnerSelection, code: "xyz", localURL: "http://127.0.0.1:9848", statectlPath: "/unused")
        #expect(command == "state-runner pair --server 'http://127.0.0.1:9848' --code 'xyz' --name 'Mac Runner' --adapters claude-code,codex --work-root \"$HOME/Desktop\" && state-runner service install")
    }

    @Test func quotesSingleQuotes() {
        #expect(PairingCommand.quote("it's") == "'it'\\''s'")
    }

    @Test func runnerSelectionIsNotAHarness() {
        #expect(PairingCommand.isRunner(PairingCommand.runnerSelection))
        #expect(!PairingCommand.isRunner("codex"))
        #expect(!PairingCommand.isRunner(""))
    }
}
