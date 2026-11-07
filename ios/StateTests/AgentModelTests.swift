import XCTest
@testable import State

/// The model catalog comes from the runners; the app groups it, picks a
/// sensible default and keeps the queue of starts that wait for the Mac.
final class AgentModelTests: XCTestCase {
    private func runner(_ models: String) throws -> Runner {
        let json = """
        {"id":"r1","display_name":"MacBook Pro","projects":["p1"],"adapters":["t3-code"],
         "registered_at":"2026-11-07T15:00:00Z","last_seen_at":"2026-11-07T15:00:00Z","revision":2,
         "harness_models":[\(models)]}
        """
        return try StateJSON.decoder.decode(Runner.self, from: Data(json.utf8))
    }

    func testDecodesTheCatalogOfARunner() throws {
        let decoded = try runner(#"{"provider":"codex","provider_name":"Codex","model":"gpt-6.1-sol","model_name":"GPT-6.1-Sol","default":true}"#)
        XCTAssertEqual(decoded.harnessModels.count, 1)
        XCTAssertEqual(decoded.harnessModels.first?.isDefault, true)
        XCTAssertEqual(decoded.harnessModels.first?.agentModel, AgentModel(provider: "codex", model: "gpt-6.1-sol"))
    }

    func testRunnersWithoutACatalogStillDecode() throws {
        let json = """
        {"id":"r1","display_name":"Mac","projects":null,"adapters":null,"registered_at":"2026-11-07T15:00:00Z","last_seen_at":"2026-11-07T15:00:00Z","revision":1}
        """
        let decoded = try StateJSON.decoder.decode(Runner.self, from: Data(json.utf8))
        XCTAssertTrue(decoded.harnessModels.isEmpty)
    }

    func testCatalogOrdersProvidersAndSuggestsSonnet() throws {
        let models = [
            #"{"provider":"acpRegistry_dsh","provider_name":"DeepSeek Harness","model":"[\"deepseek-official\",\"deepseek-flash\"]","model_name":"DeepSeek-V4.1-Flash"}"#,
            #"{"provider":"acpRegistry_kimi","provider_name":"Kimi","model":"kimi-code/k3.1-pro","model_name":"K3.1 Pro","default":true}"#,
            #"{"provider":"codex","provider_name":"Codex","model":"gpt-6-astra","model_name":"GPT-6-Astra","default":true}"#,
            #"{"provider":"claudeAgent_claude_hub","provider_name":"Claude Hub","model":"claude-opus-5-5","model_name":"Claude Opus 5.5","default":true}"#,
            #"{"provider":"claudeAgent_claude_hub","provider_name":"Claude Hub","model":"claude-sonnet-5-5","model_name":"Claude Sonnet 5.5"}"#,
        ].joined(separator: ",")
        let catalog = ModelCatalog(runners: [try runner(models), try runner(models)])
        XCTAssertEqual(catalog.models.count, 5, "two runners with the same models list each once")
        XCTAssertEqual(catalog.providers.map(\.name), ["Claude Hub", "Codex", "Kimi", "DeepSeek Harness"])
        XCTAssertEqual(catalog.suggested, AgentModel(provider: "claudeAgent_claude_hub", model: "claude-sonnet-5-5"))
        XCTAssertEqual(catalog.label(for: AgentModel(provider: "codex", model: "gpt-6-astra")), "GPT-6-Astra")
        XCTAssertEqual(catalog.label(for: AgentModel(provider: "codex", model: "gone")), "gone")
    }

    func testReminderAndSessionCarryTheirModel() throws {
        let reminderJSON = """
        {"id":"r1","title":"Karla","status":"active","execution_policy_id":"p1","execution_model":{"provider":"claudeAgent_claude_hub","model":"claude-sonnet-5-5"},"revision":2,"archived":false,"created_at":"2026-11-07T15:00:00Z","updated_at":"2026-11-07T15:00:00Z"}
        """
        let reminder = try StateJSON.decoder.decode(Reminder.self, from: Data(reminderJSON.utf8))
        XCTAssertEqual(reminder.executionModel?.model, "claude-sonnet-5-5")
        let encoded = try StateJSON.encoder.encode(reminder)
        XCTAssertTrue(String(decoding: encoded, as: UTF8.self).contains("\"execution_model\""))
    }

    func testOnlyUnreachableAddressesTryTheOtherOne() {
        XCTAssertTrue(APIClient.isConnectivityFailure(URLError(.cannotFindHost)))
        XCTAssertTrue(APIClient.isConnectivityFailure(URLError(.timedOut)))
        XCTAssertFalse(APIClient.isConnectivityFailure(URLError(.cancelled)))
        XCTAssertFalse(APIClient.isConnectivityFailure(StateAPIError.unauthorized))
    }
}
