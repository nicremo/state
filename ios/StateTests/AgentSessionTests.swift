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

    func testT3SessionCarriesItsThreadLinkAndNoTerminalResume() throws {
        var json = String(decoding: session(turns: turn(id: "t1", status: "succeeded", result: "OK"), adapter: "t3-code", harness: "mcp:abc"), as: UTF8.self)
        json = json.replacingOccurrences(of: #""harness_session_id":"mcp:abc""#, with: #""harness_session_id":"mcp:abc","harness_link":"https://app.t3.codes/threads/env/mcp%3Aabc""#)
        let decoded = try StateJSON.decoder.decode(AgentSession.self, from: Data(json.utf8))
        XCTAssertEqual(decoded.agentName, "T3 Code")
        XCTAssertTrue(decoded.runsInT3)
        XCTAssertEqual(decoded.harnessURL?.absoluteString, "https://app.t3.codes/threads/env/mcp%3Aabc")
        XCTAssertNil(decoded.resumeCommand)
        XCTAssertFalse(decoded.canOpenOnMac)
        XCTAssertTrue(decoded.canSend, "T3 threads take follow-up messages")
    }

    func testOnlyHTTPSHarnessLinksOpen() throws {
        var json = String(decoding: session(turns: "", adapter: "t3-code", harness: "x"), as: UTF8.self)
        json = json.replacingOccurrences(of: #""harness_session_id":"x""#, with: #""harness_session_id":"x","harness_link":"http://insecure.example/x""#)
        let decoded = try StateJSON.decoder.decode(AgentSession.self, from: Data(json.utf8))
        XCTAssertNil(decoded.harnessURL)
    }

    func testUnknownAdaptersGetAReadableName() {
        XCTAssertEqual(AgentNames.name(for: "my-agent"), "My Agent")
    }

    func testRowPreviewDropsMarkdownHeadings() {
        XCTAssertEqual(AgentText.rowPreview("## Objective\nDen **Report** bauen.\n- `make test`"), "Den Report bauen. make test")
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

/// Records the requests of an APIClient and answers with a fixed body.
final class RecordingURLProtocol: URLProtocol {
    nonisolated(unsafe) static var requests: [URLRequest] = []
    nonisolated(unsafe) static var body = Data()
    nonisolated(unsafe) static var status = 200

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        Self.requests.append(request)
        let response = HTTPURLResponse(url: request.url!, statusCode: Self.status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Self.body)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

final class AgentSessionAPITests: XCTestCase {
    /// The E2E run found the list asked for `/api/v1/agent-sessions%3Flimit=100`:
    /// the server answered 404 and the app emptied its session list.
    func testTheSessionListSendsItsLimitAsAQueryParameter() async throws {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [RecordingURLProtocol.self]
        RecordingURLProtocol.requests = []
        RecordingURLProtocol.status = 200
        RecordingURLProtocol.body = Data(#"{"sessions":[]}"#.utf8)
        let client = try APIClient(serverURL: URL(string: "http://127.0.0.1:19848")!, token: "t", session: URLSession(configuration: configuration))
        _ = try await client.listAgentSessions()
        let url = try XCTUnwrap(RecordingURLProtocol.requests.first?.url)
        XCTAssertEqual(url.path, "/api/v1/agent-sessions")
        XCTAssertEqual(URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems, [URLQueryItem(name: "limit", value: "100")])
    }
}
