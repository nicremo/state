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

    private func temporaryDatabasePath() -> String {
        FileManager.default.temporaryDirectory
            .appending(path: "state-tests-\(UUID().uuidString).sqlite")
            .path()
    }
}
