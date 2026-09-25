import XCTest
@testable import State

/// Sessions come from the server with their rounds; the app reads what the
/// owner may do next from them.
final class AgentSessionTests: XCTestCase {
    private func session(turns: String, status: String = "waiting", closed: Bool = false, adapter: String = "claude-code", harness: String? = "e11a322c") -> Data {
        let harnessField = harness.map { #","harness_session_id":"\#($0)""# } ?? ""
        return Data("""
        {"id":"s1","reminder_id":"r1","policy_id":"p1","project_id":"pr1","project_name":"karla-report",
         "adapter":"\(adapter)","title":"Karla-Monatsreport","closed":\(closed),"status":"\(status)",
         "revision":1,"created_at":"2026-09-25T15:00:00Z","updated_at":"2026-09-25T15:00:00Z",
         "last_activity_at":"2026-09-25T15:05:00Z"\(harnessField),"turns":[\(turns)]}
        """.utf8)
    }

    private func turn(id: String, status: String, kind: String = "message", prompt: String = "", result: String? = nil) -> String {
        let resultField = result.map { #","result_text":"\#($0)""# } ?? ""
        return """
        {"id":"\(id)","reminder_id":"r1","policy_id":"p1","policy_revision":1,"project_id":"pr1","adapter":"claude-code",
         "status":"\(status)","idempotency_key":"k\(id)","context_cursor":0,"revision":2,
         "created_at":"2026-09-25T15:0\(id.suffix(1)):00Z","updated_at":"2026-09-25T15:0\(id.suffix(1)):30Z",
         "session_id":"s1","turn_kind":"\(kind)","prompt":"\(prompt)"\(resultField),
         "task_contract":{"run_id":"\(id)","correlation_id":"s1","objective":"x","acceptance_criteria":[],"project_id":"pr1",
           "project_name":"karla-report","policy_id":"p1","policy_revision":1,"contract_hash":"h","allowed_capabilities":[],"timeout_minutes":30}}
        """
    }

    func testDecodesASessionWithItsRounds() throws {
        let data = session(turns: turn(id: "t1", status: "succeeded", prompt: "Los", result: "## Fertig\\nReport liegt bereit."))
        let decoded = try StateJSON.decoder.decode(AgentSession.self, from: data)
        XCTAssertEqual(decoded.status, .waiting)
        XCTAssertEqual(decoded.harnessSessionID, "e11a322c")
        XCTAssertEqual(decoded.turns.first?.turnKind, .message)
        XCTAssertEqual(decoded.turns.first?.prompt, "Los")
        XCTAssertEqual(decoded.lastAnswer, "## Fertig\nReport liegt bereit.")
        XCTAssertEqual(decoded.agentName, "Claude Code")
    }

    func testTheComposerIsOpenOnlyWhileTheAgentWaits() throws {
        let waiting = try StateJSON.decoder.decode(AgentSession.self, from: session(turns: turn(id: "t1", status: "succeeded")))
        XCTAssertTrue(waiting.canSend)
        XCTAssertTrue(waiting.canOpenOnMac)
        XCTAssertNil(waiting.activeTurn)

        let working = try StateJSON.decoder.decode(AgentSession.self, from: session(turns: turn(id: "t1", status: "running"), status: "working"))
        XCTAssertFalse(working.canSend)
        XCTAssertFalse(working.canOpenOnMac)
        XCTAssertEqual(working.activeTurn?.id, "t1")

        let closed = try StateJSON.decoder.decode(AgentSession.self, from: session(turns: turn(id: "t1", status: "succeeded"), status: "closed", closed: true))
        XCTAssertFalse(closed.canSend)

        let noCLISession = try StateJSON.decoder.decode(AgentSession.self, from: session(turns: turn(id: "t1", status: "failed"), status: "failed", harness: nil))
        XCTAssertTrue(noCLISession.canSend, "a failed round does not end the session")
        XCTAssertFalse(noCLISession.canOpenOnMac, "without a CLI session there is nothing to open")
    }

    func testOpenRoundsAreNotAnswers() throws {
        let turns = [
            turn(id: "t1", status: "succeeded", result: "Antwort eins"),
            turn(id: "t2", status: "succeeded", kind: "open_terminal", result: "Opened in a terminal"),
        ].joined(separator: ",")
        let decoded = try StateJSON.decoder.decode(AgentSession.self, from: session(turns: turns))
        XCTAssertEqual(decoded.lastAnswer, "Antwort eins")
        XCTAssertEqual(decoded.turns.last?.turnKind, .openTerminal)
    }

    func testResumeCommandPerAgent() throws {
        let claude = try StateJSON.decoder.decode(AgentSession.self, from: session(turns: "", adapter: "claude-code", harness: "abc"))
        XCTAssertEqual(claude.resumeCommand, "claude --resume abc")
        let codex = try StateJSON.decoder.decode(AgentSession.self, from: session(turns: "", adapter: "codex", harness: "t-1"))
        XCTAssertEqual(codex.resumeCommand, "codex resume t-1")
        let kimi = try StateJSON.decoder.decode(AgentSession.self, from: session(turns: "", adapter: "kimi-code", harness: "k-1"))
        XCTAssertEqual(kimi.resumeCommand, "kimi -S k-1")
        let pi = try StateJSON.decoder.decode(AgentSession.self, from: session(turns: "", adapter: "pi-agent", harness: "x"))
        XCTAssertNil(pi.resumeCommand)
        XCTAssertFalse(pi.canOpenOnMac)
    }

    func testRunPushNamesItsSession() throws {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        let payload = try decoder.decode(RunPushPayload.self, from: Data("""
        {"kind":"run_finished","run_id":"r","reminder_id":"m","status":"succeeded","title":"Karla","finished_at":"2026-09-25T15:00:00Z","session_id":"s1"}
        """.utf8))
        XCTAssertEqual(payload.sessionId, "s1")
    }

    func testAgendaModesMapTheLaunchArguments() {
        XCTAssertEqual(StateTab.launch(from: "planned"), .agenda)
        XCTAssertEqual(ReminderCollectionMode.launch(from: "planned"), .planned)
        XCTAssertEqual(ReminderCollectionMode.launch(from: "today"), .today)
        XCTAssertEqual(StateTab.launch(from: "agent"), .agent)
        XCTAssertEqual(StateTab.launch(from: "activity"), .agenda)
        XCTAssertEqual(StateTab.launch(from: nil), .agenda)
    }
}
