import Foundation

protocol StateAPI: Sendable {
    func getChanges(after: Int64, limit: Int) async throws -> ChangesResponse
    func getReminder(id: String) async throws -> ReminderDetail
    func send(mutation: PendingMutation) async throws -> Data
    func confirmOccurrences(_ identifiers: [String]) async throws
}

enum StateAPIError: Error, LocalizedError, Sendable {
    case invalidServerURL
    case invalidResponse
    case unauthorized
    case notFound
    case revisionConflict(server: Data)
    case server(status: Int, code: String)

    var errorDescription: String? {
        switch self {
        case .invalidServerURL:
            "The State server URL is invalid."
        case .invalidResponse:
            "The State server returned an invalid response."
        case .unauthorized:
            "The State credential is no longer valid."
        case .notFound:
            "The requested State object was not found."
        case .revisionConflict:
            "The object changed on another device."
        case let .server(status, code):
            "The State server returned \(status) with code \(code)."
        }
    }
}

actor APIClient: StateAPI {
    let serverURL: URL
    private let token: String
    private let session: URLSession

    init(serverURL: URL, token: String, session: URLSession = .shared) throws {
        guard Self.isAllowedServerURL(serverURL), !token.isEmpty else {
            throw StateAPIError.invalidServerURL
        }
        self.serverURL = serverURL
        self.token = token
        self.session = session
    }

    func getChanges(after: Int64, limit: Int) async throws -> ChangesResponse {
        let data = try await request(
            path: "/api/v1/changes",
            query: [
                URLQueryItem(name: "after", value: String(after)),
                URLQueryItem(name: "limit", value: String(limit)),
            ]
        )
        return try StateJSON.decoder.decode(ChangesResponse.self, from: data)
    }

    func getReminder(id: String) async throws -> ReminderDetail {
        let data = try await request(path: "/api/v1/reminders/\(id)")
        return try StateJSON.decoder.decode(ReminderDetail.self, from: data)
    }

    func send(mutation: PendingMutation) async throws -> Data {
        try await request(path: mutation.path, method: mutation.method, body: mutation.body)
    }

    func confirmOccurrences(_ identifiers: [String]) async throws {
        let body = try JSONSerialization.data(withJSONObject: ["occurrence_ids": identifiers], options: [.sortedKeys])
        _ = try await request(path: "/api/v1/devices/push/confirmations", method: "POST", body: body)
    }

    func registerPushRoute(
        relayURL: URL,
        routeID: String,
        authorization: String,
        publicKey: Data
    ) async throws -> DeviceRoute {
        let body = try JSONSerialization.data(withJSONObject: [
            "relay_url": relayURL.absoluteString,
            "route_id": routeID,
            "authorization": authorization,
            "public_key": publicKey.base64EncodedString(),
        ], options: [.sortedKeys])
        let data = try await request(path: "/api/v1/devices/push", method: "PUT", body: body)
        return try StateJSON.decoder.decode(DeviceRoute.self, from: data)
    }

    func createPairingCode(kind: ActorKind = .harness, harness: String?, displayName: String, deviceName: String) async throws -> PairingCode {
        var payload: [String: Any] = [
            "kind": kind.rawValue,
            "display_name": displayName,
            "device_name": deviceName,
        ]
        if let harness {
            payload["harness"] = harness
        }
        let body = try JSONSerialization.data(withJSONObject: payload, options: [.sortedKeys])
        let data = try await request(path: "/api/v1/pairing/codes", method: "POST", body: body)
        return try StateJSON.decoder.decode(PairingCode.self, from: data)
    }

    func listActors(kind: ActorKind) async throws -> [Actor] {
        let path = kind == .harness ? "/api/v1/agents" : "/api/v1/devices"
        let data = try await request(path: path)
        return try StateJSON.decoder.decode(ActorListResponse.self, from: data).actors.map(\.actor)
    }

    // MARK: Agent execution

    // UI-level endpoints for projects, policies, runners and runs. They are
    // intentionally not part of the StateAPI sync seam: runs ride inside
    // ReminderDetail during sync and the global lists are fetched
    // best-effort by the app model.

    func listProjects() async throws -> [Project] {
        let data = try await request(path: "/api/v1/projects")
        return try StateJSON.decoder.decode(ProjectListResponse.self, from: data).projects
    }

    func listPolicies() async throws -> [ExecutionPolicy] {
        let data = try await request(path: "/api/v1/policies")
        return try StateJSON.decoder.decode(PolicyListResponse.self, from: data).policies
    }

    func createPolicy(_ policy: ExecutionPolicy) async throws -> ExecutionPolicy {
        let body = try JSONSerialization.data(withJSONObject: [
            "name": policy.name,
            "project_id": policy.projectID,
            "adapter": policy.adapter,
            "mode": policy.mode.rawValue,
            "allowed_capabilities": policy.allowedCapabilities,
            "mark_occurrence_done_on_success": policy.markOccurrenceDoneOnSuccess,
            "notify_on_start": policy.notifyOnStart,
            "notify_on_completion": policy.notifyOnCompletion,
            "notify_on_failure": policy.notifyOnFailure,
            "timeout_minutes": policy.timeoutMinutes,
            "client_request_id": UUIDv7.generate().uuidString.lowercased(),
            "source": "ios",
        ], options: [.sortedKeys])
        let data = try await request(path: "/api/v1/policies", method: "POST", body: body)
        return try StateJSON.decoder.decode(ExecutionPolicy.self, from: data)
    }

    /// `policy.revision` is the last known server revision; it is sent as the
    /// expected revision of the update.
    func updatePolicy(_ policy: ExecutionPolicy) async throws -> ExecutionPolicy {
        let body = try JSONSerialization.data(withJSONObject: [
            "name": policy.name,
            "adapter": policy.adapter,
            "mode": policy.mode.rawValue,
            "allowed_capabilities": policy.allowedCapabilities,
            "mark_occurrence_done_on_success": policy.markOccurrenceDoneOnSuccess,
            "notify_on_start": policy.notifyOnStart,
            "notify_on_completion": policy.notifyOnCompletion,
            "notify_on_failure": policy.notifyOnFailure,
            "timeout_minutes": policy.timeoutMinutes,
            "enabled": policy.enabled,
            "expected_revision": policy.revision,
            "client_request_id": UUIDv7.generate().uuidString.lowercased(),
            "source": "ios",
        ], options: [.sortedKeys])
        let data = try await request(path: "/api/v1/policies/\(policy.id)", method: "PATCH", body: body)
        return try StateJSON.decoder.decode(ExecutionPolicy.self, from: data)
    }

    func listRunners() async throws -> [Runner] {
        let data = try await request(path: "/api/v1/runners")
        return try StateJSON.decoder.decode(RunnerListResponse.self, from: data).runners
    }

    /// `runner.revision` is the last known server revision; it is sent as the
    /// expected revision of the update.
    func updateRunner(_ runner: Runner) async throws -> Runner {
        let body = try JSONSerialization.data(withJSONObject: [
            "display_name": runner.displayName,
            "projects": runner.projects,
            "adapters": runner.adapters,
            "expected_revision": runner.revision,
            "client_request_id": UUIDv7.generate().uuidString.lowercased(),
            "source": "ios",
        ], options: [.sortedKeys])
        let data = try await request(path: "/api/v1/runners/\(runner.id)", method: "PATCH", body: body)
        return try StateJSON.decoder.decode(Runner.self, from: data)
    }

    func listRuns(
        reminderID: String? = nil,
        status: AgentRunStatus? = nil,
        runnerID: String? = nil,
        limit: Int = 100
    ) async throws -> [AgentRun] {
        var query = [URLQueryItem(name: "limit", value: String(limit))]
        if let reminderID {
            query.append(URLQueryItem(name: "reminder_id", value: reminderID))
        }
        if let status {
            query.append(URLQueryItem(name: "status", value: status.rawValue))
        }
        if let runnerID {
            query.append(URLQueryItem(name: "runner_id", value: runnerID))
        }
        let data = try await request(path: "/api/v1/runs", query: query)
        return try StateJSON.decoder.decode(RunListResponse.self, from: data).runs
    }

    func getRun(id: String) async throws -> AgentRun {
        let data = try await request(path: "/api/v1/runs/\(id)")
        return try StateJSON.decoder.decode(AgentRun.self, from: data)
    }

    func createManualRun(reminderID: String, policyID: String) async throws -> AgentRun {
        let requestID = UUIDv7.generate().uuidString.lowercased()
        let body = try JSONSerialization.data(withJSONObject: [
            "reminder_id": reminderID,
            "policy_id": policyID,
            "client_request_id": requestID,
            "correlation_id": requestID,
            "source": "ios",
        ], options: [.sortedKeys])
        let data = try await request(path: "/api/v1/runs", method: "POST", body: body)
        return try StateJSON.decoder.decode(AgentRun.self, from: data)
    }

    func approveRun(id: String, approved: Bool, expectedRevision: Int64) async throws -> AgentRun {
        let requestID = UUIDv7.generate().uuidString.lowercased()
        let body = try JSONSerialization.data(withJSONObject: [
            "approved": approved,
            "expected_revision": expectedRevision,
            "client_request_id": requestID,
            "correlation_id": requestID,
            "source": "ios",
        ], options: [.sortedKeys])
        let data = try await request(path: "/api/v1/runs/\(id)/approval", method: "POST", body: body)
        return try StateJSON.decoder.decode(AgentRun.self, from: data)
    }

    func cancelRun(id: String, expectedRevision: Int64) async throws -> AgentRun {
        let requestID = UUIDv7.generate().uuidString.lowercased()
        let body = try JSONSerialization.data(withJSONObject: [
            "expected_revision": expectedRevision,
            "client_request_id": requestID,
            "correlation_id": requestID,
            "source": "ios",
        ], options: [.sortedKeys])
        let data = try await request(path: "/api/v1/runs/\(id)/cancel", method: "POST", body: body)
        return try StateJSON.decoder.decode(AgentRun.self, from: data)
    }

    func revokeActor(id: String, kind: ActorKind) async throws {
        let path = kind == .harness ? "/api/v1/agents/\(id)" : "/api/v1/devices/\(id)"
        _ = try await request(path: path, method: "DELETE")
    }

    static func bootstrapOwner(
        serverURL: URL,
        bootstrapToken: String,
        displayName: String,
        deviceName: String,
        session: URLSession = .shared
    ) async throws -> Credential {
        guard isAllowedServerURL(serverURL) else { throw StateAPIError.invalidServerURL }
        let body = try JSONSerialization.data(withJSONObject: [
            "display_name": displayName,
            "device_name": deviceName,
        ], options: [.sortedKeys])
        var request = URLRequest(url: endpoint(base: serverURL, path: "/api/v1/pairing/owner"))
        request.httpMethod = "POST"
        request.httpBody = body
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.setValue(bootstrapToken, forHTTPHeaderField: "X-State-Bootstrap-Token")
        let (data, response) = try await session.data(for: request)
        try validate(response: response, data: data)
        return try StateJSON.decoder.decode(Credential.self, from: data)
    }

    static func exchangePairingCode(
        serverURL: URL,
        code: String,
        session: URLSession = .shared
    ) async throws -> Credential {
        guard isAllowedServerURL(serverURL) else { throw StateAPIError.invalidServerURL }
        let body = try JSONSerialization.data(withJSONObject: ["code": code], options: [.sortedKeys])
        var request = URLRequest(url: endpoint(base: serverURL, path: "/api/v1/pairing/exchange"))
        request.httpMethod = "POST"
        request.httpBody = body
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        let (data, response) = try await session.data(for: request)
        try validate(response: response, data: data)
        return try StateJSON.decoder.decode(Credential.self, from: data)
    }

    private func request(
        path: String,
        method: String = "GET",
        query: [URLQueryItem] = [],
        body: Data? = nil
    ) async throws -> Data {
        var components = URLComponents(url: Self.endpoint(base: serverURL, path: path), resolvingAgainstBaseURL: false)
        components?.queryItems = query.isEmpty ? nil : query
        guard let url = components?.url else { throw StateAPIError.invalidServerURL }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.httpBody = body
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if body != nil {
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        let (data, response) = try await session.data(for: request)
        try Self.validate(response: response, data: data)
        return data
    }

    private static func validate(response: URLResponse, data: Data) throws {
        guard let response = response as? HTTPURLResponse else {
            throw StateAPIError.invalidResponse
        }
        if (200..<300).contains(response.statusCode) {
            return
        }
        let object = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
        let code = object?["code"] as? String ?? "unknown"
        switch response.statusCode {
        case 401:
            throw StateAPIError.unauthorized
        case 404:
            throw StateAPIError.notFound
        case 409:
            if
                let details = object?["details"] as? [String: Any],
                let server = details["server"],
                let snapshot = try? JSONSerialization.data(withJSONObject: server, options: [.sortedKeys])
            {
                throw StateAPIError.revisionConflict(server: snapshot)
            }
            throw StateAPIError.server(status: response.statusCode, code: code)
        default:
            throw StateAPIError.server(status: response.statusCode, code: code)
        }
    }

    private static func endpoint(base: URL, path: String) -> URL {
        var components = URLComponents(url: base, resolvingAgainstBaseURL: false) ?? URLComponents()
        let basePath = components.path.hasSuffix("/") ? String(components.path.dropLast()) : components.path
        components.path = basePath + (path.hasPrefix("/") ? path : "/" + path)
        components.query = nil
        components.fragment = nil
        return components.url ?? base
    }

    private static func isAllowedServerURL(_ url: URL) -> Bool {
        guard let scheme = url.scheme?.lowercased(), let host = url.host?.lowercased() else { return false }
        if scheme == "https" { return true }
        return scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")
    }
}
