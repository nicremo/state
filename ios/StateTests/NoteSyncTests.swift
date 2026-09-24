import Foundation
import XCTest
@testable import State

/// Notes sync through a dirty flag, the server version the local edits are
/// based on, and a version counter. The fake server below applies the same
/// revision and idempotency rules as internal/state/notes.go.
final class NoteSyncTests: XCTestCase {
    private let serverJSON = """
    {"id":"0198a2b9-0000-7000-8000-00000000a001","title":"Ideen","title_source":"derived",
     "document":"# Ideen\\nState bekommt Notizen","plain_text":"Ideen\\nState bekommt Notizen",
     "summary":"State bekommt Notizen","summary_source":"derived","archived":false,"revision":3,
     "created_at":"2026-09-24T10:00:00Z","updated_at":"2026-09-24T11:00:00.123Z"}
    """

    func testDecodesAServerNoteAndANoteEvent() throws {
        let note = try StateJSON.decoder.decode(Note.self, from: Data(serverJSON.utf8))
        XCTAssertEqual(note.title, "Ideen")
        XCTAssertEqual(note.titleSource, Note.derivedSource)
        XCTAssertEqual(note.revision, 3)

        let event = try StateJSON.decoder.decode(AuditEvent.self, from: Data("""
        {"id":"e","reminder_id":"","note_id":"n1","action":"note.created","actor":{"id":"a","kind":"owner"},
         "server_time":"2026-09-24T10:00:00Z","changed_fields":[],"revision":1,"correlation_id":"c",
         "client_request_id":"r","hash":"h","signature":"s"}
        """.utf8))
        XCTAssertNil(event.reminderID)
        XCTAssertEqual(event.noteID, "n1")
    }

    func testLocalSearchIgnoresCaseAndTerms() {
        let note = Note.local(id: "n", title: "", document: "# Einkauf\nMilch und Käse", at: Date())
        XCTAssertTrue(note.matches("käse milch"))
        XCTAssertTrue(note.matches("EINKAUF"))
        XCTAssertTrue(note.matches("kase"), "diacritics are ignored like on the server")
        XCTAssertFalse(note.matches("brot"))
    }

    func testCreatePushReplacesTheProvisionalIDAndLeavesAnAlias() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let server = FakeNoteServer()
        let local = Note.local(id: "0198a2b9-0000-7000-8000-00000000c001", title: "", document: "# Ideen\nText", at: Date())
        try await database.insertLocalNote(local)

        try await SyncEngine(database: database, api: server).sync()

        let notes = try await database.notes()
        XCTAssertEqual(notes.count, 1)
        XCTAssertNotEqual(notes[0].id, local.id)
        let resolved = try await database.note(id: local.id)
        XCTAssertEqual(resolved?.id, notes[0].id, "the provisional ID still finds the note")
        let dirty = try await database.dirtyNoteRecords()
        XCTAssertTrue(dirty.isEmpty)
    }

    func testAnEditDuringThePushStaysDirtyAndGoesOutNext() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let server = FakeNoteServer()
        let local = Note.local(id: "0198a2b9-0000-7000-8000-00000000c002", title: "", document: "Eins", at: Date())
        try await database.insertLocalNote(local)
        await server.beforeResponding {
            _ = try? await database.editNote(id: local.id) { $0.apply(title: nil, document: "Eins\nZwei") }
        }

        try await SyncEngine(database: database, api: server).sync()
        var stored = try await database.notes()
        XCTAssertEqual(stored.first?.document, "Eins\nZwei", "the edit made in flight is not replaced by the server echo")
        let dirtyAfterFirst = try await database.dirtyNoteRecords()
        XCTAssertEqual(dirtyAfterFirst.count, 1)

        await server.beforeResponding(nil)
        try await SyncEngine(database: database, api: server).sync()
        stored = try await database.notes()
        let onServer = await server.document(of: stored[0].id)
        XCTAssertEqual(onServer, "Eins\nZwei")
        let dirtyAfterSecond = try await database.dirtyNoteRecords()
        XCTAssertTrue(dirtyAfterSecond.isEmpty)
    }

    /// Review finding C1: two offline edits after another device's change
    /// must not overwrite that change on the server.
    func testOfflineEditsNeverOverwriteAnotherDevicesEdit() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let server = FakeNoteServer()
        let local = Note.local(id: "0198a2b9-0000-7000-8000-00000000c003", title: "", document: "Einkauf\nMilch", at: Date())
        try await database.insertLocalNote(local)
        try await SyncEngine(database: database, api: server).sync()
        let noteID = try await database.notes()[0].id

        await server.editElsewhere(id: noteID, document: "Einkauf\nMilch\nBrot (anderes Gerät)")
        _ = try await database.editNote(id: noteID) { $0.apply(title: nil, document: "Einkauf\nMilch\nEier") }
        _ = try await database.editNote(id: noteID) { $0.apply(title: nil, document: "Einkauf\nMilch\nEier\nKäse") }

        try await SyncEngine(database: database, api: server).sync()
        try await SyncEngine(database: database, api: server).sync()

        let onServer = await server.document(of: noteID)
        XCTAssertEqual(onServer, "Einkauf\nMilch\nBrot (anderes Gerät)", "the other device's edit survives")
        let notes = try await database.notes()
        XCTAssertEqual(notes.count, 2)
        let copy = try XCTUnwrap(notes.first { $0.id != noteID })
        XCTAssertEqual(copy.document, "Einkauf\nMilch\nEier\nKäse", "the offline text is kept as a conflict copy")
        XCTAssertTrue(copy.title.hasSuffix(String(localized: "(conflict copy)")))
        let copyOnServer = await server.document(of: copy.id)
        XCTAssertEqual(copyOnServer, copy.document, "the copy reaches the server too")
    }

    func testAConflictOnArchiveKeepsTheIntentWithoutACopy() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let server = FakeNoteServer()
        try await database.insertLocalNote(Note.local(id: "0198a2b9-0000-7000-8000-00000000c004", title: "", document: "Alt", at: Date()))
        try await SyncEngine(database: database, api: server).sync()
        let noteID = try await database.notes()[0].id

        await server.editElsewhere(id: noteID, document: "Alt\nneu von woanders")
        _ = try await database.editNote(id: noteID) { $0.archived = true }
        try await SyncEngine(database: database, api: server).sync()
        try await SyncEngine(database: database, api: server).sync()

        let archivedOnServer = await server.isArchived(noteID)
        XCTAssertTrue(archivedOnServer)
        let onServer = await server.document(of: noteID)
        XCTAssertEqual(onServer, "Alt\nneu von woanders")
        let all = try await database.notes(includeArchived: true)
        XCTAssertEqual(all.count, 1, "an archive-only edit makes no conflict copy")
    }

    /// Review finding C2: a note the server rejects must not block anything.
    func testARejectedNoteIsMarkedAndOthersStillSync() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let server = FakeNoteServer()
        let rejected = Note.local(id: "0198a2b9-0000-7000-8000-00000000c005", title: "", document: "wird abgelehnt", at: Date(timeIntervalSince1970: 1))
        let accepted = Note.local(id: "0198a2b9-0000-7000-8000-00000000c006", title: "", document: "kommt durch", at: Date(timeIntervalSince1970: 2))
        try await database.insertLocalNote(rejected)
        try await database.insertLocalNote(accepted)
        await server.reject(document: "wird abgelehnt")

        try await SyncEngine(database: database, api: server).sync()

        let errors = try await database.noteSyncErrors()
        XCTAssertNotNil(errors[rejected.id])
        let storedCount = await server.count
        XCTAssertEqual(storedCount, 1)

        _ = try await database.editNote(id: rejected.id) { $0.apply(title: nil, document: "jetzt gültig") }
        let errorsAfterEdit = try await database.noteSyncErrors()
        XCTAssertNil(errorsAfterEdit[rejected.id], "a new edit clears the mark and retries")
    }

    func testPullKeepsUnsentLocalEdits() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let server = FakeNoteServer()
        try await database.insertLocalNote(Note.local(id: "0198a2b9-0000-7000-8000-00000000c007", title: "", document: "Basis", at: Date()))
        try await SyncEngine(database: database, api: server).sync()
        let noteID = try await database.notes()[0].id
        _ = try await database.editNote(id: noteID) { $0.apply(title: nil, document: "lokal") }

        let fetched = try await database.note(id: noteID)
        var incoming = try XCTUnwrap(fetched)
        incoming.apply(title: nil, document: "vom Server")
        incoming.revision += 1
        try await database.applyServer(note: incoming)

        let stored = try await database.note(id: noteID)
        XCTAssertEqual(stored?.document, "lokal")
    }

    func testRetriedPatchReusesItsRequestID() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        try await database.insertLocalNote(Note.local(id: "0198a2b9-0000-7000-8000-00000000c008", title: "", document: "x", at: Date()))
        let first = try await database.notePatchRequestID(id: "0198a2b9-0000-7000-8000-00000000c008")
        let second = try await database.notePatchRequestID(id: "0198a2b9-0000-7000-8000-00000000c008")
        XCTAssertEqual(first, second)
    }

    func testPullFetchesNotesNamedInTheChangeFeed() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let note = try StateJSON.decoder.decode(Note.self, from: Data(serverJSON.utf8))
        let server = FakeNoteServer()
        await server.seed(note, cursor: 21)

        try await SyncEngine(database: database, api: server).sync()

        let stored = try await database.note(id: note.id)
        let cursor = try await database.cursor()
        XCTAssertEqual(stored?.revision, 3)
        XCTAssertEqual(cursor, 21)
    }

    func testDatabaseListsNewestFirstAndSeparatesTheArchive() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let older = Note.local(id: "0198a2b9-0000-7000-8000-00000000b001", title: "", document: "Alt", at: Date(timeIntervalSince1970: 1))
        let newer = Note.local(id: "0198a2b9-0000-7000-8000-00000000b002", title: "", document: "Neu", at: Date(timeIntervalSince1970: 2))
        var archived = Note.local(id: "0198a2b9-0000-7000-8000-00000000b003", title: "", document: "Weg", at: Date(timeIntervalSince1970: 3))
        archived.archived = true
        for note in [older, newer, archived] {
            try await database.applyServer(note: note)
        }

        let visible = try await database.notes()
        XCTAssertEqual(visible.map(\.id), [newer.id, older.id])
        let archive = try await database.archivedNotes()
        XCTAssertEqual(archive.map(\.id), [archived.id])
    }

    func testQuickSuccessiveTogglesAllLand() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let id = "0198a2b9-0000-7000-8000-00000000c009"
        try await database.insertLocalNote(Note.local(id: id, title: "", document: "Liste\n- [ ] a\n- [ ] b\n- [ ] c", at: Date()))

        await withTaskGroup(of: Void.self) { group in
            for line in 1...3 {
                group.addTask {
                    _ = try? await database.editNote(id: id) { note in
                        note.apply(title: nil, document: NoteEditing.toggleTask(atLine: line, in: note.document))
                    }
                }
            }
        }

        let stored = try await database.note(id: id)
        XCTAssertEqual(stored?.document, "Liste\n- [x] a\n- [x] b\n- [x] c")
    }

    private func temporaryDatabasePath() -> String {
        FileManager.default.temporaryDirectory
            .appending(path: "state-note-tests-\(UUID().uuidString).sqlite")
            .path()
    }
}

/// A note server with the Go rules: revision check, idempotent request IDs,
/// validation errors, and a change feed of note events.
private actor FakeNoteServer: StateAPI {
    private var notes: [String: Note] = [:]
    private var requests: [String: Note] = [:]
    private var rejectedDocuments: Set<String> = []
    private var feed: [Change] = []
    private var hook: (@Sendable () async -> Void)?

    var count: Int { notes.count }

    func document(of id: String) -> String? { notes[id]?.document }
    func isArchived(_ id: String) -> Bool { notes[id]?.archived ?? false }
    func reject(document: String) { rejectedDocuments.insert(document) }
    func beforeResponding(_ hook: (@Sendable () async -> Void)?) { self.hook = hook }

    func seed(_ note: Note, cursor: Int64) {
        notes[note.id] = note
        feed.append(Change(cursor: cursor, event: Self.event(for: note)))
    }

    func editElsewhere(id: String, document: String) {
        guard var note = notes[id] else { return }
        note.apply(title: nil, document: document)
        note.revision += 1
        notes[id] = note
    }

    func getChanges(after: Int64, limit: Int) async throws -> ChangesResponse {
        let changes = feed.filter { $0.cursor > after }
        return ChangesResponse(changes: changes, cursor: changes.last?.cursor ?? after)
    }

    func getReminder(id: String) async throws -> ReminderDetail { throw StateAPIError.notFound }

    func getNote(id: String) async throws -> Note {
        guard let note = notes[id] else { throw StateAPIError.notFound }
        return note
    }

    func send(mutation: PendingMutation) async throws -> Data {
        let body = try JSONSerialization.jsonObject(with: mutation.body) as? [String: Any] ?? [:]
        let requestID = body["client_request_id"] as? String ?? ""
        if let replay = requests[requestID] {
            return try StateJSON.encoder.encode(replay)
        }
        let stored: Note
        if mutation.method == "POST" {
            let document = body["document"] as? String ?? ""
            if rejectedDocuments.contains(document) { throw StateAPIError.server(status: 400, code: "invalid_input") }
            var note = Note.local(
                id: UUIDv7.generate().uuidString.lowercased(),
                title: body["title"] as? String ?? "",
                document: document,
                at: Date()
            )
            note.revision = 1
            stored = note
        } else {
            let id = String(mutation.path.split(separator: "/").last ?? "")
            guard var note = notes[id] else { throw StateAPIError.notFound }
            let expected = (body["expected_revision"] as? NSNumber)?.int64Value ?? 0
            guard expected == note.revision else {
                throw StateAPIError.revisionConflict(server: try StateJSON.encoder.encode(note))
            }
            let title = body["title"] as? String
            let document = body["document"] as? String ?? note.document
            note.apply(title: title, document: document)
            if let archived = body["archived"] as? Bool { note.archived = archived }
            note.revision += 1
            stored = note
        }
        if let hook { await hook() }
        notes[stored.id] = stored
        requests[requestID] = stored
        return try StateJSON.encoder.encode(stored)
    }

    func confirmOccurrences(_ identifiers: [String]) async throws {}

    private static func event(for note: Note) -> AuditEvent {
        AuditEvent(
            id: UUIDv7.generate().uuidString.lowercased(),
            reminderID: nil,
            action: "note.created",
            actor: Actor(id: "owner", kind: .owner, displayName: "Fabian", harness: nil, deviceName: nil),
            serverTime: Date(),
            clientTime: nil,
            source: "rest",
            sourceExcerpt: nil,
            changedFields: ["document"],
            revision: note.revision,
            correlationID: "c",
            clientRequestID: "r",
            previousHash: nil,
            hash: "h",
            signature: "s",
            noteID: note.id
        )
    }
}
