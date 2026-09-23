import XCTest
@testable import State

/// Connecting to a server has to start from that server's truth. A cache left
/// over from the demo or from another server showed foreign reminders and made
/// the notification confirmation fail with "not found".
final class LocalCacheResetTests: XCTestCase {
    private let demoID = "01989f00-0000-7000-8000-000000000010"
    private let realID = "0198a1b5-62ce-78d4-b240-814bff9dc3f4"

    func testResetClearsCachedContentAndCursor() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let reminder = Reminder.fixture(id: realID, revision: 1)
        try await database.apply(detail: ReminderDetail(reminder: reminder, comments: [], occurrences: [], history: []), cursor: 12)
        _ = try await database.enqueue(method: "PATCH", path: "/api/v1/reminders/\(realID)", body: Data("{}".utf8), entityID: realID)

        try await database.resetCache(keepingPendingMutations: false)

        let stored = try await database.reminder(id: realID)
        let cursor = try await database.cursor()
        let pending = try await database.pendingMutations()
        XCTAssertNil(stored)
        XCTAssertEqual(cursor, 0)
        XCTAssertTrue(pending.isEmpty)
    }

    func testResetCanKeepUnsentLocalChanges() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        _ = try await database.enqueue(method: "PATCH", path: "/api/v1/reminders/\(realID)", body: Data("{}".utf8), entityID: realID)

        try await database.resetCache(keepingPendingMutations: true)

        let pending = try await database.pendingMutations()
        XCTAssertEqual(pending.count, 1)
    }

    func testDetectsLeftoverDemoContent() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        try await database.apply(
            detail: ReminderDetail(reminder: Reminder.fixture(id: realID, revision: 1), comments: [], occurrences: [], history: []),
            cursor: nil
        )
        let withoutDemo = try await database.containsDemoContent()
        XCTAssertFalse(withoutDemo)

        try await database.apply(
            detail: ReminderDetail(reminder: Reminder.fixture(id: demoID, revision: 1), comments: [], occurrences: [], history: []),
            cursor: nil
        )
        let withDemo = try await database.containsDemoContent()
        XCTAssertTrue(withDemo)
    }

    private func temporaryDatabasePath() -> String {
        FileManager.default.temporaryDirectory
            .appending(path: "state-reset-tests-\(UUID().uuidString).sqlite")
            .path()
    }
}
