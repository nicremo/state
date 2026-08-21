import GRDB
import XCTest
@testable import State

final class DatabaseTests: XCTestCase {
    func testApplicationDatabasePathPreservesDirectorySpaces() {
        let baseURL = URL(fileURLWithPath: "/tmp/Application Support", isDirectory: true)

        XCTAssertEqual(
            StateDatabase.databasePath(baseURL: baseURL),
            "/tmp/Application Support/state.sqlite"
        )
    }

    func testStateJSONDecodesAcronymKeysAndActorRecords() throws {
        let json = Data(
            """
            {
              "actors": [
                {
                  "actor": {
                    "id": "01989f00-0000-7000-8000-000000000001",
                    "kind": "harness",
                    "display_name": "Claude Code",
                    "harness": "claude-code",
                    "device_name": "MacBook"
                  },
                  "created_at": "2026-08-11T08:00:00Z"
                }
              ]
            }
            """.utf8
        )

        let response = try StateJSON.decoder.decode(ActorListResponse.self, from: json)

        XCTAssertEqual(response.actors.first?.actor.id, "01989f00-0000-7000-8000-000000000001")
        XCTAssertEqual(response.actors.first?.actor.harness, "claude-code")
    }

    func testStateJSONDecodesIdentifierAndURLSuffixes() throws {
        let json = Data(
            """
            {
              "actor_id": "actor-1",
              "relay_url": "https://relay.example.com",
              "route_id": "route-1",
              "public_key": "AQID",
              "created_at": "2026-08-11T08:00:00Z",
              "updated_at": "2026-08-11T08:01:00Z"
            }
            """.utf8
        )

        let route = try StateJSON.decoder.decode(DeviceRoute.self, from: json)

        XCTAssertEqual(route.actorID, "actor-1")
        XCTAssertEqual(route.relayURL, "https://relay.example.com")
        XCTAssertEqual(route.routeID, "route-1")
        XCTAssertEqual(route.publicKey, Data([1, 2, 3]))
    }

    func testServerDetailAndPendingMutationAreStoredTransactionally() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let reminder = Reminder.fixture(id: "0198a1b5-62ce-78d4-b240-814bff9dc3f4", revision: 2)
        let detail = ReminderDetail(reminder: reminder, comments: [], occurrences: [], history: [])

        try await database.apply(detail: detail, cursor: 8)
        let mutation = try await database.enqueue(
            method: "PATCH",
            path: "/api/v1/reminders/\(reminder.id)",
            body: Data("{}".utf8),
            entityID: reminder.id
        )

        let stored = try await database.reminder(id: reminder.id)
        let pending = try await database.pendingMutations()
        let cursor = try await database.cursor()
        XCTAssertEqual(stored?.revision, 2)
        XCTAssertEqual(pending.map(\.id), [mutation.id])
        XCTAssertEqual(cursor, 8)
    }

    func testConflictPreservesServerAndLocalSnapshots() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let server = Reminder.fixture(id: "0198a1b5-62ce-78d4-b240-814bff9dc3f4", revision: 3)
        let local = Reminder.fixture(id: server.id, revision: 2, title: "Local title")

        try await database.recordConflict(
            entityID: server.id,
            server: try StateJSON.encoder.encode(server),
            local: try StateJSON.encoder.encode(local),
            fields: ["title"]
        )

        let conflicts = try await database.conflicts()
        XCTAssertEqual(conflicts.count, 1)
        XCTAssertEqual(conflicts[0].fields, ["title"])
        XCTAssertFalse(conflicts[0].resolved)
    }

    @MainActor
    func testDemoSeedsVisibleReminderHistory() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "state-tests-\(UUID().uuidString)"))
        let model = AppModel(
            database: database,
            sessionRepository: SessionRepository(defaults: defaults)
        )

        await model.enterDemo()

        XCTAssertNil(model.presentedError)
        XCTAssertTrue(model.isDemo)
        XCTAssertEqual(model.reminders.count, 2)
        XCTAssertEqual(model.activity.count, 3)
    }

    @MainActor
    func testDemoSeedsExecutionState() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "state-tests-\(UUID().uuidString)"))
        let model = AppModel(
            database: database,
            sessionRepository: SessionRepository(defaults: defaults)
        )

        await model.enterDemo()

        XCTAssertNil(model.presentedError)
        XCTAssertEqual(model.projects.map(\.name), ["customer-api"])
        XCTAssertEqual(model.policies.map(\.name), ["nightly-maintenance"])
        XCTAssertEqual(model.runners.map(\.displayName), ["Mac mini"])
        let executable = try XCTUnwrap(model.reminders.first { $0.executionPolicyID == model.policies.first?.id })
        let loadedDetail = await model.reminderDetail(id: executable.id)
        let detail = try XCTUnwrap(loadedDetail)
        XCTAssertEqual(detail.runs.count, 2)
        XCTAssertEqual(Set(detail.runs.map(\.status)), [.succeeded, .needsApproval])
        let approvalRun = try XCTUnwrap(detail.runs.first { $0.status == .needsApproval })
        XCTAssertEqual(approvalRun.approvalCapability, "deploy")
    }

    @MainActor
    func testPolicyAttachDetachEnqueuesExplicitPolicyField() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "state-tests-\(UUID().uuidString)"))
        let model = AppModel(
            database: database,
            sessionRepository: SessionRepository(defaults: defaults)
        )
        let reminder = Reminder.fixture(id: "0198a1b5-62ce-78d4-b240-814bff9dc3f4", revision: 1)
        try await database.apply(
            detail: ReminderDetail(reminder: reminder, comments: [], occurrences: [], history: []),
            cursor: nil
        )

        await model.setExecutionPolicy("01989f00-0000-7000-8000-000000000051", on: reminder)

        var pending = try await database.pendingMutations()
        var body = try XCTUnwrap(pending.last?.body)
        var object = try XCTUnwrap(JSONSerialization.jsonObject(with: body) as? [String: Any])
        XCTAssertEqual(object["execution_policy_id"] as? String, "01989f00-0000-7000-8000-000000000051")
        var storedReminder = try await database.reminder(id: reminder.id)
        var cached = try XCTUnwrap(storedReminder)
        XCTAssertEqual(cached.executionPolicyID, "01989f00-0000-7000-8000-000000000051")

        await model.setExecutionPolicy(nil, on: cached)

        pending = try await database.pendingMutations()
        XCTAssertEqual(pending.count, 2)
        body = try XCTUnwrap(pending.last?.body)
        object = try XCTUnwrap(JSONSerialization.jsonObject(with: body) as? [String: Any])
        XCTAssertTrue(object.keys.contains("execution_policy_id"), "detach must encode an explicit null")
        XCTAssertTrue(object["execution_policy_id"] is NSNull)
        storedReminder = try await database.reminder(id: reminder.id)
        cached = try XCTUnwrap(storedReminder)
        XCTAssertNil(cached.executionPolicyID)
    }

    func testFreshDatabaseRunsBothMigrations() async throws {
        let path = temporaryDatabasePath()
        _ = try StateDatabase(path: path)

        let pool = try DatabasePool(path: path)
        let applied = try await pool.read { database in
            try String.fetchAll(database, sql: "SELECT identifier FROM grdb_migrations ORDER BY identifier")
        }
        XCTAssertEqual(applied, ["v1", "v2"])
    }

    func testMigrationFromV1PreservesRemindersAndAddsExecutionCaches() async throws {
        let path = temporaryDatabasePath()
        let legacy = try DatabasePool(path: path)
        try Self.legacyV1Migrator.migrate(legacy)
        let reminder = Reminder.fixture(id: "0198a1b5-62ce-78d4-b240-814bff9dc3f4", revision: 2, title: "Legacy")
        try await legacy.write { database in
            try database.execute(
                sql: """
                INSERT INTO reminder_cache (id, title, status, revision, archived, scheduled_at, updated_at, json)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                """,
                arguments: [
                    reminder.id,
                    reminder.title,
                    reminder.status.rawValue,
                    reminder.revision,
                    false,
                    nil,
                    reminder.updatedAt,
                    try StateJSON.encoder.encode(reminder),
                ]
            )
        }

        let database = try StateDatabase(path: path)

        let stored = try await database.reminder(id: reminder.id)
        XCTAssertEqual(stored?.title, "Legacy")
        XCTAssertNil(stored?.executionPolicyID)
        let run = AgentRun.fixture(id: "0198a2ba-0000-7000-8000-000000000080", reminderID: reminder.id, status: .running)
        try await database.apply(
            detail: ReminderDetail(reminder: reminder, comments: [], occurrences: [], history: [], runs: [run]),
            cursor: nil
        )
        let migratedRuns = try await database.runs(for: reminder.id)
        XCTAssertEqual(migratedRuns.map(\.id), [run.id])
    }

    func testRunCacheIsReplacedOnDetailApply() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let reminder = Reminder.fixture(id: "0198a1b5-62ce-78d4-b240-814bff9dc3f4", revision: 1)
        let firstRun = AgentRun.fixture(id: "0198a2ba-0000-7000-8000-000000000080", reminderID: reminder.id, status: .running)

        try await database.apply(
            detail: ReminderDetail(reminder: reminder, comments: [], occurrences: [], history: [], runs: [firstRun]),
            cursor: nil
        )

        let cachedRuns = try await database.runs(for: reminder.id)
        let cachedDetail = try await database.detail(id: reminder.id)
        XCTAssertEqual(cachedRuns.map(\.id), [firstRun.id])
        XCTAssertEqual(cachedDetail?.runs.map(\.id), [firstRun.id])

        let secondRun = AgentRun.fixture(id: "0198a2ba-0000-7000-8000-000000000081", reminderID: reminder.id, status: .succeeded)
        try await database.apply(
            detail: ReminderDetail(reminder: reminder, comments: [], occurrences: [], history: [], runs: [secondRun]),
            cursor: nil
        )

        let replacedRuns = try await database.runs(for: reminder.id)
        let removedRun = try await database.run(id: firstRun.id)
        let storedRun = try await database.run(id: secondRun.id)
        XCTAssertEqual(replacedRuns.map(\.id), [secondRun.id])
        XCTAssertNil(removedRun)
        XCTAssertEqual(storedRun?.status, .succeeded)
    }

    func testDeleteReminderCascadesRuns() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let reminder = Reminder.fixture(id: "0198a1b5-62ce-78d4-b240-814bff9dc3f4", revision: 1)
        let run = AgentRun.fixture(id: "0198a2ba-0000-7000-8000-000000000080", reminderID: reminder.id, status: .running)
        try await database.apply(
            detail: ReminderDetail(reminder: reminder, comments: [], occurrences: [], history: [], runs: [run]),
            cursor: nil
        )

        try await database.deleteReminder(id: reminder.id)

        let remainingRuns = try await database.runs(for: reminder.id)
        let orphanedRun = try await database.run(id: run.id)
        XCTAssertEqual(remainingRuns, [])
        XCTAssertNil(orphanedRun)
    }

    func testGlobalListsReplaceAll() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let firstProject = Project.fixture(id: "01989f00-0000-7000-8000-000000000050", name: "customer-api")
        let secondProject = Project.fixture(id: "01989f00-0000-7000-8000-000000000053", name: "billing")
        let policy = ExecutionPolicy.fixture(id: "01989f00-0000-7000-8000-000000000051", projectID: firstProject.id, name: "nightly-maintenance")
        let runner = Runner.fixture(id: "01989f00-0000-7000-8000-000000000052", displayName: "Mac mini")

        try await database.apply(projects: [firstProject, secondProject])
        try await database.apply(policies: [policy])
        try await database.apply(runners: [runner])

        let projectNames = try await database.projects().map(\.name)
        let policyIDs = try await database.policies().map(\.id)
        let runnerIDs = try await database.runners().map(\.id)
        let capabilities = try await database.policies().first?.allowedCapabilities
        XCTAssertEqual(projectNames, ["billing", "customer-api"])
        XCTAssertEqual(policyIDs, [policy.id])
        XCTAssertEqual(runnerIDs, [runner.id])
        XCTAssertEqual(capabilities, ["run_tests"])

        try await database.apply(projects: [secondProject])
        try await database.apply(policies: [])
        try await database.apply(runners: [])

        let remainingProjects = try await database.projects().map(\.id)
        let remainingPolicies = try await database.policies()
        let remainingRunners = try await database.runners()
        XCTAssertEqual(remainingProjects, [secondProject.id])
        XCTAssertEqual(remainingPolicies, [])
        XCTAssertEqual(remainingRunners, [])
    }

    private func temporaryDatabasePath() -> String {
        FileManager.default.temporaryDirectory
            .appending(path: "state-tests-\(UUID().uuidString).sqlite")
            .path()
    }

    /// Replicates the frozen v1 schema so the migration test starts from a
    /// database that only knows the first migration.
    private static let legacyV1Migrator: DatabaseMigrator = {
        var migrator = DatabaseMigrator()
        migrator.registerMigration("v1") { database in
            try database.create(table: "reminder_cache") { table in
                table.column("id", .text).primaryKey()
                table.column("title", .text).notNull()
                table.column("status", .text).notNull()
                table.column("revision", .integer).notNull()
                table.column("archived", .boolean).notNull()
                table.column("scheduled_at", .datetime)
                table.column("updated_at", .datetime).notNull()
                table.column("json", .blob).notNull()
            }
            try database.create(index: "reminder_schedule_idx", on: "reminder_cache", columns: ["archived", "scheduled_at"])
            try database.create(table: "comment_cache") { table in
                table.column("id", .text).primaryKey()
                table.column("reminder_id", .text).notNull().indexed().references("reminder_cache", onDelete: .cascade)
                table.column("created_at", .datetime).notNull()
                table.column("json", .blob).notNull()
            }
            try database.create(table: "occurrence_cache") { table in
                table.column("id", .text).primaryKey()
                table.column("reminder_id", .text).notNull().indexed().references("reminder_cache", onDelete: .cascade)
                table.column("scheduled_at", .datetime)
                table.column("status", .text).notNull()
                table.column("json", .blob).notNull()
            }
            try database.create(table: "audit_cache") { table in
                table.column("id", .text).primaryKey()
                table.column("reminder_id", .text).notNull().indexed().references("reminder_cache", onDelete: .cascade)
                table.column("server_time", .datetime).notNull().indexed()
                table.column("action", .text).notNull()
                table.column("json", .blob).notNull()
            }
            try database.create(table: "metadata") { table in
                table.column("key", .text).primaryKey()
                table.column("value", .text).notNull()
            }
            try database.create(table: "pending_mutations") { table in
                table.column("id", .text).primaryKey()
                table.column("method", .text).notNull()
                table.column("path", .text).notNull()
                table.column("body", .blob).notNull()
                table.column("entity_id", .text)
                table.column("created_at", .datetime).notNull().indexed()
                table.column("attempts", .integer).notNull().defaults(to: 0)
            }
            try database.create(table: "conflicts") { table in
                table.column("id", .text).primaryKey()
                table.column("entity_id", .text).notNull().indexed()
                table.column("server_snapshot", .blob).notNull()
                table.column("local_snapshot", .blob).notNull()
                table.column("fields", .blob).notNull()
                table.column("created_at", .datetime).notNull()
                table.column("resolved", .boolean).notNull().defaults(to: false)
            }
        }
        return migrator
    }()
}

private extension Project {
    static func fixture(id: String, name: String) -> Project {
        Project(
            id: id,
            name: name,
            description: nil,
            rootPathHint: nil,
            revision: 1,
            createdAt: Date(timeIntervalSince1970: 1_786_000_000),
            updatedAt: Date(timeIntervalSince1970: 1_786_000_000)
        )
    }
}

private extension ExecutionPolicy {
    static func fixture(id: String, projectID: String, name: String) -> ExecutionPolicy {
        ExecutionPolicy(
            id: id,
            name: name,
            projectID: projectID,
            adapter: "claude-code",
            mode: .supervised,
            allowedCapabilities: ["run_tests"],
            markOccurrenceDoneOnSuccess: true,
            notifyOnStart: false,
            notifyOnCompletion: true,
            notifyOnFailure: true,
            timeoutMinutes: 30,
            enabled: true,
            revision: 1,
            createdAt: Date(timeIntervalSince1970: 1_786_000_000),
            updatedAt: Date(timeIntervalSince1970: 1_786_000_000)
        )
    }
}

private extension Runner {
    static func fixture(id: String, displayName: String) -> Runner {
        Runner(
            id: id,
            displayName: displayName,
            projects: [],
            adapters: ["claude-code"],
            registeredAt: Date(timeIntervalSince1970: 1_786_000_000),
            lastSeenAt: Date(timeIntervalSince1970: 1_786_000_000),
            revision: 1
        )
    }
}

private extension AgentRun {
    static func fixture(id: String, reminderID: String, status: AgentRunStatus) -> AgentRun {
        AgentRun(
            id: id,
            reminderID: reminderID,
            occurrenceID: nil,
            policyID: "01989f00-0000-7000-8000-000000000051",
            policyRevision: 1,
            projectID: "01989f00-0000-7000-8000-000000000050",
            adapter: "claude-code",
            runnerID: nil,
            status: status,
            idempotencyKey: id,
            taskContract: TaskContract(
                runID: id,
                correlationID: id,
                objective: "Objective",
                acceptanceCriteria: [],
                projectID: "01989f00-0000-7000-8000-000000000050",
                projectName: "customer-api",
                policyID: "01989f00-0000-7000-8000-000000000051",
                policyRevision: 1,
                contractHash: "hash",
                allowedCapabilities: ["run_tests"],
                timeoutMinutes: 30,
                completionRule: nil
            ),
            contextCursor: 0,
            leaseExpiresAt: nil,
            requestedAt: Date(timeIntervalSince1970: 1_786_000_000),
            claimedAt: nil,
            startedAt: nil,
            finishedAt: nil,
            resultSummary: nil,
            resultArtifactRef: nil,
            failureCode: nil,
            approvalCapability: nil,
            createdByActor: nil,
            completedByActor: nil,
            revision: 1,
            createdAt: Date(timeIntervalSince1970: 1_786_000_000),
            updatedAt: Date(timeIntervalSince1970: 1_786_000_000)
        )
    }
}
