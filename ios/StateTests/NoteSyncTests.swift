import Foundation
import XCTest
@testable import State

/// Notes travel through the same change feed and mutation queue as reminders.
final class NoteSyncTests: XCTestCase {
    private let serverJSON = """
    {"id":"0198a2b9-0000-7000-8000-00000000a001","title":"Ideen","title_source":"derived",
     "document":"# Ideen\\nState bekommt Notizen","plain_text":"Ideen\\nState bekommt Notizen",
     "summary":"State bekommt Notizen","summary_source":"derived","archived":false,"revision":3,
     "created_at":"2026-09-24T10:00:00Z","updated_at":"2026-09-24T11:00:00.123Z"}
    """

    func testDecodesAServerNote() throws {
        let note = try StateJSON.decoder.decode(Note.self, from: Data(serverJSON.utf8))
        XCTAssertEqual(note.title, "Ideen")
        XCTAssertEqual(note.titleSource, Note.derivedSource)
        XCTAssertEqual(note.summary, "State bekommt Notizen")
        XCTAssertEqual(note.revision, 3)
    }

    func testLocalDerivationMatchesTheServer() {
        let note = Note.local(id: "n", title: "", document: "# Einkauf\n\nMilch und **Brot** holen.\n- [ ] Eier", at: Date())
        XCTAssertEqual(note.title, "Einkauf")
        XCTAssertEqual(note.summary, "Milch und Brot holen. Eier")
        XCTAssertFalse(note.plainText.contains("**"))

        var titled = Note.local(id: "t", title: "Fester Titel", document: "Erste Zeile\nZweite", at: Date())
        XCTAssertEqual(titled.titleSource, Note.userSource)
        XCTAssertEqual(titled.summary, "Erste Zeile Zweite")
        titled.apply(title: nil, document: "Neu")
        XCTAssertEqual(titled.title, "Fester Titel")
        XCTAssertEqual(titled.summary, "Neu")
        titled.apply(title: "", document: "Neu")
        XCTAssertEqual(titled.title, "Neu")
        XCTAssertEqual(titled.titleSource, Note.derivedSource)
    }

    func testDatabaseListsNewestFirstAndHidesArchived() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let older = Note.local(id: "0198a2b9-0000-7000-8000-00000000b001", title: "", document: "Alt", at: Date(timeIntervalSince1970: 1))
        let newer = Note.local(id: "0198a2b9-0000-7000-8000-00000000b002", title: "", document: "Neu", at: Date(timeIntervalSince1970: 2))
        var archived = Note.local(id: "0198a2b9-0000-7000-8000-00000000b003", title: "", document: "Weg", at: Date(timeIntervalSince1970: 3))
        archived.archived = true
        for note in [older, newer, archived] {
            try await database.apply(note: note)
        }

        let visible = try await database.notes()
        XCTAssertEqual(visible.map(\.id), [newer.id, older.id])
        let all = try await database.notes(includeArchived: true)
        XCTAssertEqual(all.first?.id, archived.id)
        let loaded = try await database.note(id: older.id)
        XCTAssertEqual(loaded?.document, "Alt")
    }

    func testPullFetchesNotesNamedInTheChangeFeed() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let note = try StateJSON.decoder.decode(Note.self, from: Data(serverJSON.utf8))
        let event = AuditEvent.noteFixture(noteID: note.id)
        let api = NoteMockAPI(
            changes: ChangesResponse(changes: [Change(cursor: 21, event: event)], cursor: 21),
            notes: [note.id: note]
        )

        try await SyncEngine(database: database, api: api).sync()

        let stored = try await database.note(id: note.id)
        let cursor = try await database.cursor()
        XCTAssertEqual(stored?.revision, 3)
        XCTAssertEqual(cursor, 21)
    }

    func testPushedNoteReplacesTheProvisionalOne() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let provisional = Note.local(id: "0198a2b9-0000-7000-8000-00000000c001", title: "", document: "# Ideen", at: Date())
        try await database.apply(note: provisional)
        _ = try await database.enqueue(method: "POST", path: "/api/v1/notes", body: Data("{}".utf8), entityID: provisional.id)
        let api = NoteMockAPI(created: Data(serverJSON.utf8))

        try await SyncEngine(database: database, api: api).sync()

        let notes = try await database.notes()
        XCTAssertEqual(notes.map(\.id), ["0198a2b9-0000-7000-8000-00000000a001"])
    }

    func testConflictingNoteEditKeepsTheLocalTextAsACopy() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let server = try StateJSON.decoder.decode(Note.self, from: Data(serverJSON.utf8))
        var local = server
        local.apply(title: nil, document: "# Ideen\nMeine Offline-Fassung")
        try await database.apply(note: local)
        _ = try await database.enqueue(
            method: "PATCH",
            path: "/api/v1/notes/\(server.id)",
            body: Data("{\"document\":\"# Ideen\\nMeine Offline-Fassung\"}".utf8),
            entityID: server.id
        )
        let api = NoteMockAPI(conflict: Data(serverJSON.utf8))

        try await SyncEngine(database: database, api: api).sync()

        let stored = try await database.note(id: server.id)
        XCTAssertEqual(stored?.document, server.document)
        let copies = try await database.notes().filter { $0.id != server.id }
        XCTAssertEqual(copies.count, 1)
        XCTAssertEqual(copies.first?.document, "# Ideen\nMeine Offline-Fassung")
        let pending = try await database.pendingMutations()
        XCTAssertEqual(pending.map(\.path), ["/api/v1/notes"])
    }

    private func temporaryDatabasePath() -> String {
        FileManager.default.temporaryDirectory
            .appending(path: "state-note-tests-\(UUID().uuidString).sqlite")
            .path()
    }
}

private actor NoteMockAPI: StateAPI {
    private let changesResponse: ChangesResponse
    private let notesByID: [String: Note]
    private let created: Data?
    private let conflict: Data?

    init(
        changes: ChangesResponse = ChangesResponse(changes: [], cursor: 0),
        notes: [String: Note] = [:],
        created: Data? = nil,
        conflict: Data? = nil
    ) {
        changesResponse = changes
        notesByID = notes
        self.created = created
        self.conflict = conflict
    }

    func getChanges(after: Int64, limit: Int) async throws -> ChangesResponse { changesResponse }

    func getReminder(id: String) async throws -> ReminderDetail { throw StateAPIError.notFound }

    func getNote(id: String) async throws -> Note {
        guard let note = notesByID[id] else { throw StateAPIError.notFound }
        return note
    }

    func send(mutation: PendingMutation) async throws -> Data {
        if mutation.method == "PATCH", let conflict {
            throw StateAPIError.revisionConflict(server: conflict)
        }
        if mutation.method == "POST", mutation.path == "/api/v1/notes", let created, conflict == nil {
            return created
        }
        if mutation.method == "POST" {
            throw URLError(.notConnectedToInternet)
        }
        return Data("{}".utf8)
    }

    func confirmOccurrences(_ identifiers: [String]) async throws {}
}

private extension AuditEvent {
    static func noteFixture(noteID: String) -> AuditEvent {
        AuditEvent(
            id: "0198a2ba-0000-7225-aa6d-797106fb50fa",
            reminderID: nil,
            action: "note.created",
            actor: Actor(id: "owner", kind: .owner, displayName: "Fabian", harness: nil, deviceName: nil),
            serverTime: Date(),
            clientTime: nil,
            source: "rest",
            sourceExcerpt: nil,
            changedFields: ["document"],
            revision: 1,
            correlationID: "c",
            clientRequestID: "r",
            previousHash: nil,
            hash: "h",
            signature: "s",
            noteID: noteID
        )
    }
}
