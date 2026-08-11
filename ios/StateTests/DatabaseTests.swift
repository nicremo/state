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

    private func temporaryDatabasePath() -> String {
        FileManager.default.temporaryDirectory
            .appending(path: "state-tests-\(UUID().uuidString).sqlite")
            .path()
    }
}
