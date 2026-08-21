import Foundation
import XCTest
@testable import State

final class SyncEngineTests: XCTestCase {
    func testSyncPushesPendingMutationThenPullsChangedDetail() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let reminder = Reminder.fixture(id: "0198a2b9-8c11-7378-93c7-7374c3b6fdbf", revision: 2)
        let detail = ReminderDetail(reminder: reminder, comments: [], occurrences: [], history: [])
        let event = AuditEvent.fixture(reminderID: reminder.id)
        let api = MockStateAPI(
            changes: ChangesResponse(changes: [Change(cursor: 12, event: event)], cursor: 12),
            details: [reminder.id: detail]
        )
        let mutation = try await database.enqueue(
            method: "POST",
            path: "/api/v1/reminders",
            body: Data("{}".utf8),
            entityID: nil
        )
        let engine = SyncEngine(database: database, api: api)

        try await engine.sync()

        let sentMutationIDs = await api.sentMutationIDs
        let pending = try await database.pendingMutations()
        let stored = try await database.reminder(id: reminder.id)
        let cursor = try await database.cursor()
        XCTAssertEqual(sentMutationIDs, [mutation.id])
        XCTAssertTrue(pending.isEmpty)
        XCTAssertEqual(stored?.revision, 2)
        XCTAssertEqual(cursor, 12)
    }

    func testRevisionConflictIsStoredAndMutationLeavesQueue() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let local = Reminder.fixture(id: "0198a2b9-8c11-7378-93c7-7374c3b6fdbf", revision: 2, title: "Local")
        try await database.apply(detail: ReminderDetail(reminder: local, comments: [], occurrences: [], history: []), cursor: 1)
        let server = Reminder.fixture(id: local.id, revision: 3, title: "Server")
        let api = MockStateAPI(conflict: server)
        _ = try await database.enqueue(
            method: "PATCH",
            path: "/api/v1/reminders/\(local.id)",
            body: Data("{\"title\":\"Local\"}".utf8),
            entityID: local.id
        )
        let engine = SyncEngine(database: database, api: api)

        try await engine.sync()

        let pending = try await database.pendingMutations()
        XCTAssertTrue(pending.isEmpty)
        let conflicts = try await database.conflicts()
        XCTAssertEqual(conflicts.count, 1)
        XCTAssertEqual(conflicts[0].entityID, local.id)
        XCTAssertEqual(conflicts[0].fields, ["title"])
    }

    func testPullSkipsNullReminderEventsAndAdvancesCursor() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let reminder = Reminder.fixture(id: "0198a2b9-8c11-7378-93c7-7374c3b6fdbf", revision: 2)
        let detail = ReminderDetail(reminder: reminder, comments: [], occurrences: [], history: [])
        let policyEvent = AuditEvent.fixture(reminderID: nil, action: "policy.created")
        let reminderEvent = AuditEvent.fixture(reminderID: reminder.id)
        let api = MockStateAPI(
            changes: ChangesResponse(
                changes: [Change(cursor: 11, event: policyEvent), Change(cursor: 12, event: reminderEvent)],
                cursor: 12
            ),
            details: [reminder.id: detail]
        )
        let engine = SyncEngine(database: database, api: api)

        try await engine.sync()

        let stored = try await database.reminder(id: reminder.id)
        let cursor = try await database.cursor()
        XCTAssertEqual(stored?.revision, 2)
        XCTAssertEqual(cursor, 12)
    }

    func testPullAdvancesCursorWhenPageHasOnlyNullReminderEvents() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let api = MockStateAPI(
            changes: ChangesResponse(
                changes: [Change(cursor: 7, event: AuditEvent.fixture(reminderID: nil, action: "runner.registered"))],
                cursor: 7
            ),
            details: [:]
        )
        let engine = SyncEngine(database: database, api: api)

        // Any reminder fetch would throw StateAPIError.notFound, so a passing
        // sync proves the page was not grouped on a reminder at all.
        try await engine.sync()

        let cursor = try await database.cursor()
        XCTAssertEqual(cursor, 7)
    }

    private func temporaryDatabasePath() -> String {
        FileManager.default.temporaryDirectory
            .appending(path: "state-sync-tests-\(UUID().uuidString).sqlite")
            .path()
    }
}

private actor MockStateAPI: StateAPI {
    private let changesResponse: ChangesResponse
    private let detailsByID: [String: ReminderDetail]
    private let conflict: Reminder?
    private(set) var sentMutationIDs: [String] = []

    init(
        changes: ChangesResponse = ChangesResponse(changes: [], cursor: 0),
        details: [String: ReminderDetail] = [:],
        conflict: Reminder? = nil
    ) {
        changesResponse = changes
        detailsByID = details
        self.conflict = conflict
    }

    func getChanges(after: Int64, limit: Int) async throws -> ChangesResponse {
        changesResponse
    }

    func getReminder(id: String) async throws -> ReminderDetail {
        guard let detail = detailsByID[id] else { throw StateAPIError.notFound }
        return detail
    }

    func send(mutation: PendingMutation) async throws -> Data {
        sentMutationIDs.append(mutation.id)
        if let conflict {
            throw StateAPIError.revisionConflict(server: try StateJSON.encoder.encode(conflict))
        }
        return Data("{}".utf8)
    }

    func confirmOccurrences(_ identifiers: [String]) async throws {}
}

private extension AuditEvent {
    static func fixture(reminderID: String?, action: String = "reminder.updated") -> AuditEvent {
        AuditEvent(
            id: "0198a2ba-30c1-7225-aa6d-797106fb50fa",
            reminderID: reminderID,
            action: action,
            actor: Actor(id: "owner", kind: .owner, displayName: "Fabian", harness: nil, deviceName: "iPhone"),
            serverTime: Date(timeIntervalSince1970: 1_786_000_000),
            clientTime: nil,
            source: "ios",
            sourceExcerpt: nil,
            changedFields: ["title"],
            revision: 2,
            correlationID: "correlation",
            clientRequestID: "request",
            previousHash: nil,
            hash: "hash",
            signature: "signature"
        )
    }
}
