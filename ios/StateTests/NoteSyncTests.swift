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

// MARK: Regressions from the verification review

private actor LossyAPI: StateAPI {
    let inner: FakeNoteServer
    var dropNextResponse = false
    var beforeSend: (@Sendable () async -> Void)?
    init(_ inner: FakeNoteServer) { self.inner = inner }
    func dropNext() { dropNextResponse = true }
    func setBeforeSend(_ hook: (@Sendable () async -> Void)?) { beforeSend = hook }
    var beforeChanges: (@Sendable () async -> Void)?
    func setBeforeChanges(_ hook: (@Sendable () async -> Void)?) { beforeChanges = hook }
    func getChanges(after: Int64, limit: Int) async throws -> ChangesResponse {
        if let hook = beforeChanges { beforeChanges = nil; await hook() }
        return try await inner.getChanges(after: after, limit: limit)
    }
    func getReminder(id: String) async throws -> ReminderDetail { try await inner.getReminder(id: id) }
    func getNote(id: String) async throws -> Note { try await inner.getNote(id: id) }
    func confirmOccurrences(_ identifiers: [String]) async throws {}
    func send(mutation: PendingMutation) async throws -> Data {
        if let hook = beforeSend { beforeSend = nil; await hook() }
        let data = try await inner.send(mutation: mutation)
        if dropNextResponse { dropNextResponse = false; throw URLError(.timedOut) }
        return data
    }
}

extension NoteSyncTests {
    /// PATCH reached the server, the response was lost, the user typed more.
    func testALostPatchResponseDoesNotSwallowLaterTyping() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let server = FakeNoteServer()
        let api = LossyAPI(server)
        try await database.insertLocalNote(Note.local(id: "0198a2b9-0000-7000-8000-00000000d001", title: "", document: "Basis", at: Date()))
        try await SyncEngine(database: database, api: api).sync()
        let noteID = try await database.notes()[0].id

        _ = try await database.editNote(id: noteID) { $0.apply(title: nil, document: "Basis\nA1") }
        await api.dropNext()
        do { try await SyncEngine(database: database, api: api).sync(); XCTFail("expected URLError") } catch is URLError {}
        _ = try await database.editNote(id: noteID) { $0.apply(title: nil, document: "Basis\nA1\nA2") }
        try await SyncEngine(database: database, api: api).sync()
        try await SyncEngine(database: database, api: api).sync()

        let local = try await database.note(id: noteID)
        let onServer = await server.document(of: noteID)
        XCTAssertEqual(local?.document, "Basis\nA1\nA2", "LOCAL lost the second edit")
        XCTAssertEqual(onServer, "Basis\nA1\nA2", "SERVER never got the second edit")
    }

    /// POST reached the server, the response was lost, the user typed more.
    func testALostCreateResponseDoesNotSwallowLaterTyping() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let server = FakeNoteServer()
        let api = LossyAPI(server)
        let id = "0198a2b9-0000-7000-8000-00000000d002"
        try await database.insertLocalNote(Note.local(id: id, title: "", document: "Neu", at: Date()))
        await api.dropNext()
        do { try await SyncEngine(database: database, api: api).sync(); XCTFail("expected URLError") } catch is URLError {}
        _ = try await database.editNote(id: id) { $0.apply(title: nil, document: "Neu\nweiter getippt") }
        try await SyncEngine(database: database, api: api).sync()
        try await SyncEngine(database: database, api: api).sync()

        let local = try await database.note(id: id)
        XCTAssertEqual(local?.document, "Neu\nweiter getippt", "LOCAL lost the typing after a lost create response")
    }

    /// The user types while a PATCH that will 409 is in flight.
    func testTypingDuringAConflictingPushEndsUpInTheCopy() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let server = FakeNoteServer()
        let api = LossyAPI(server)
        try await database.insertLocalNote(Note.local(id: "0198a2b9-0000-7000-8000-00000000d003", title: "", document: "Einkauf", at: Date()))
        try await SyncEngine(database: database, api: api).sync()
        let noteID = try await database.notes()[0].id
        await server.editElsewhere(id: noteID, document: "Einkauf\nvom Agenten")
        _ = try await database.editNote(id: noteID) { $0.apply(title: nil, document: "Einkauf\nL1") }
        await api.setBeforeSend {
            _ = try? await database.editNote(id: noteID) { $0.apply(title: nil, document: "Einkauf\nL1\nL2 im Flug") }
        }
        try await SyncEngine(database: database, api: api).sync()
        try await SyncEngine(database: database, api: api).sync()

        let all = try await database.notes(includeArchived: true)
        XCTAssertTrue(all.contains { $0.document.contains("L2 im Flug") }, "text typed during the conflicting push vanished: \(all.map(\.document))")
    }

    /// A never-synced note whose create response was lost is archived offline.
    func testArchivingAfterALostCreateArchivesOnTheServer() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let server = FakeNoteServer()
        let api = LossyAPI(server)
        let id = "0198a2b9-0000-7000-8000-00000000d004"
        try await database.insertLocalNote(Note.local(id: id, title: "", document: "Wegwerfen", at: Date()))
        await api.dropNext()
        do { try await SyncEngine(database: database, api: api).sync(); XCTFail("expected URLError") } catch is URLError {}
        _ = try await database.editNote(id: id) { $0.archived = true }
        try await SyncEngine(database: database, api: api).sync()

        let local = try await database.notes(includeArchived: true)
        let serverCount = await server.count
        XCTAssertFalse(serverCount == 1 && local.isEmpty, "server keeps a live note the device deleted locally; the next pull resurrects it")
    }

    @MainActor
    func testATitleOnlySaveKeepsAnAgentsEdit() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "state-tests-\(UUID().uuidString)"))
        let model = AppModel(database: database, sessionRepository: SessionRepository(defaults: defaults))
        var server = Note.local(id: "0198a2b9-0000-7000-8000-00000000d005", title: "", document: "Plan\nalt", at: Date())
        try await database.applyServer(note: server)
        let fetchedBase = try await database.note(id: server.id)
        let editorBase = try XCTUnwrap(fetchedBase)
        // An agent edits; the pull applies it while the editor is open.
        server.apply(title: nil, document: "Plan\nneu vom Agenten")
        server.revision += 1
        try await database.applyServer(note: server)
        // The user only typed a title.
        _ = await model.saveNote(id: server.id, title: "Mein Titel", document: editorBase.document, base: editorBase)
        let stored = try await database.note(id: server.id)
        XCTAssertEqual(stored?.document, "Plan\nneu vom Agenten", "a title-only save reverted the agent's document")
    }

    @MainActor
    func testClearingWhileTypingDoesNotArchive() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "state-tests-\(UUID().uuidString)"))
        let model = AppModel(database: database, sessionRepository: SessionRepository(defaults: defaults))
        let note = Note.local(id: "0198a2b9-0000-7000-8000-00000000d006", title: "", document: "Alt", at: Date())
        try await database.applyServer(note: note)
        let fetched0 = try await database.note(id: note.id)
        var base = try XCTUnwrap(fetched0)
        // Autosave fires while the user has selected all and deleted.
        let first = await model.saveNote(id: note.id, title: "", document: "", base: base, isFinal: false)
        XCTAssertEqual(first, .skipped, "an autosave of an empty moment writes nothing")
        let fetched1 = try await database.note(id: note.id)
        base = try XCTUnwrap(fetched1)
        // The user keeps typing the new text.
        _ = await model.saveNote(id: note.id, title: "", document: "Ganz neu", base: base, isFinal: false)
        let stored = try await database.note(id: note.id)
        XCTAssertEqual(stored?.archived, false, "the note being edited stays archived and has left the list")
        let final = await model.saveNote(id: note.id, title: "", document: "", base: try XCTUnwrap(stored), isFinal: true)
        XCTAssertEqual(final, .archivedEmpty, "closing the editor on an empty note archives it")
    }

    @MainActor
    func testACancelledAutosaveShowsNoError() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "state-tests-\(UUID().uuidString)"))
        let model = AppModel(database: database, sessionRepository: SessionRepository(defaults: defaults))
        let note = Note.local(id: "0198a2b9-0000-7000-8000-00000000d007", title: "", document: "Alt", at: Date())
        try await database.applyServer(note: note)
        let fetched2 = try await database.note(id: note.id)
        let base = try XCTUnwrap(fetched2)
        let task = Task { @MainActor in
            _ = await model.saveNote(id: note.id, title: "", document: "Alt\nneu", base: base)
        }
        task.cancel()
        await task.value
        XCTAssertNil(model.presentedError, "a cancelled autosave pops an error: \(model.presentedError ?? "")")
    }

    /// A pull skips a dirty note; the user then reverts the edit (e.g. ticks
    /// and unticks a box). The note must not stay on the old server version.
    func testAnUndoneEditMovesToTheNewestServerVersion() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let server = FakeNoteServer()
        let api = LossyAPI(server)
        try await database.insertLocalNote(Note.local(id: "0198a2b9-0000-7000-8000-00000000d008", title: "", document: "Liste\n- [ ] a", at: Date()))
        try await SyncEngine(database: database, api: api).sync()
        let noteID = try await database.notes()[0].id
        // An agent edits on the server, with a change-feed entry.
        var agent = try await server.getNote(id: noteID)
        agent.apply(title: nil, document: "Liste\n- [ ] a\n- [ ] vom Agenten")
        agent.revision += 1
        await server.seed(agent, cursor: 50)
        // The user ticks the box while the sync is between push and pull.
        await api.setBeforeChanges {
            _ = try? await database.editNote(id: noteID) { $0.apply(title: nil, document: NoteEditing.toggleTask(atLine: 1, in: $0.document)) }
        }
        try await SyncEngine(database: database, api: api).sync()
        // ... and unticks it again.
        _ = try await database.editNote(id: noteID) { $0.apply(title: nil, document: NoteEditing.toggleTask(atLine: 1, in: $0.document)) }
        try await SyncEngine(database: database, api: api).sync()
        try await SyncEngine(database: database, api: api).sync()

        let local = try await database.note(id: noteID)
        XCTAssertEqual(local?.document, "Liste\n- [ ] a\n- [ ] vom Agenten", "the device is stuck on an old server version")
    }
}

// MARK: Regressions from the second verification review

private actor FlakyAPI: StateAPI {
    let inner: FakeNoteServer
    private var failAfterApply = false
    private var afterApply: (@Sendable (Note) async -> Void)?
    init(_ inner: FakeNoteServer) { self.inner = inner }
    /// The next send is applied by the server, then answered with a 502.
    func failNextAfterApply(_ hook: (@Sendable (Note) async -> Void)?) { failAfterApply = true; afterApply = hook }
    func getChanges(after: Int64, limit: Int) async throws -> ChangesResponse { try await inner.getChanges(after: after, limit: limit) }
    func getReminder(id: String) async throws -> ReminderDetail { try await inner.getReminder(id: id) }
    func getNote(id: String) async throws -> Note { try await inner.getNote(id: id) }
    func confirmOccurrences(_ identifiers: [String]) async throws {}
    func send(mutation: PendingMutation) async throws -> Data {
        let data = try await inner.send(mutation: mutation)
        if failAfterApply {
            failAfterApply = false
            let stored = try StateJSON.decoder.decode(Note.self, from: data)
            if let hook = afterApply { await hook(stored) }
            throw StateAPIError.server(status: 502, code: "bad_gateway")
        }
        return data
    }
}

extension NoteSyncTests {
    private func proofDatabasePath() -> String {
        FileManager.default.temporaryDirectory.appending(path: "state-proof-\(UUID().uuidString).sqlite").path()
    }

    /// a PATCH applied by the server but answered with a 5xx is
    /// replayed and answered from the idempotency record (the old snapshot).
    /// completeNotePush then throws away `latest`, the agent's newer version
    /// the pull already saw, and the cursor is past it: the device stays stale.
    func testAReplayedPatchKeepsTheNewerServerVersion() async throws {
        let database = try StateDatabase(path: proofDatabasePath())
        let server = FakeNoteServer()
        let api = FlakyAPI(server)
        try await database.insertLocalNote(Note.local(id: "0198a2b9-0000-7000-8000-00000000e001", title: "", document: "Basis", at: Date()))
        try await SyncEngine(database: database, api: api).sync()
        let noteID = try await database.notes()[0].id

        _ = try await database.editNote(id: noteID) { $0.apply(title: nil, document: "Basis\nlokal") }
        await api.failNextAfterApply { stored in
            // An agent edits right after the device's PATCH landed.
            var agent = stored
            agent.apply(title: nil, document: stored.document + "\nvom Agenten")
            agent.revision += 1
            await server.seed(agent, cursor: 60)
        }
        try await SyncEngine(database: database, api: api).sync() // 502, pull records latest
        try await SyncEngine(database: database, api: api).sync() // replay answered from record
        try await SyncEngine(database: database, api: api).sync()

        let local = try await database.note(id: noteID)
        let onServer = await server.document(of: noteID)
        XCTAssertEqual(local?.document, onServer, "device stays on the replayed snapshot, the agent's edit never arrives")
    }

    /// the sync makes a conflict copy while the editor is open; the
    /// editor's next autosave (base = its last save) makes a second copy.
    @MainActor
    func testTheEditorKeepsWritingIntoTheSyncsConflictCopy() async throws {
        let database = try StateDatabase(path: proofDatabasePath())
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "state-proof-\(UUID().uuidString)"))
        let model = AppModel(database: database, sessionRepository: SessionRepository(defaults: defaults))
        let server = FakeNoteServer()
        try await database.insertLocalNote(Note.local(id: "0198a2b9-0000-7000-8000-00000000e002", title: "", document: "Einkauf", at: Date()))
        try await SyncEngine(database: database, api: server).sync()
        let noteID = try await database.notes()[0].id
        let opened = try await database.note(id: noteID)
        let editorBase = try XCTUnwrap(opened)

        guard case let .saved(afterFirst) = await model.saveNote(id: noteID, title: "", document: "Einkauf\nL1", base: editorBase, isFinal: false) else {
            return XCTFail("first autosave")
        }
        await server.editElsewhere(id: noteID, document: "Einkauf\nvom Agenten")
        try await SyncEngine(database: database, api: server).sync() // 409 -> copy #1
        _ = await model.saveNote(id: noteID, title: "", document: "Einkauf\nL1\nL2", base: afterFirst, isFinal: false)

        let copies = try await database.notes().filter { $0.id != noteID }
        XCTAssertEqual(copies.count, 1, "one conflict, several copies: \(copies.map(\.document))")
    }

    /// finding 5 fixed for the document only. A body-only save
    /// reverts a title the agent set while the editor was open.
    @MainActor
    func testABodySaveKeepsAnAgentsTitle() async throws {
        let database = try StateDatabase(path: proofDatabasePath())
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "state-proof-\(UUID().uuidString)"))
        let model = AppModel(database: database, sessionRepository: SessionRepository(defaults: defaults))
        var server = Note.local(id: "0198a2b9-0000-7000-8000-00000000e003", title: "Alt", document: "Plan", at: Date())
        try await database.applyServer(note: server)
        let opened = try await database.note(id: server.id)
        let editorBase = try XCTUnwrap(opened)
        server.apply(title: "Neu vom Agenten", document: server.document)
        server.revision += 1
        try await database.applyServer(note: server)

        _ = await model.saveNote(id: server.id, title: "Alt", document: "Plan\nmehr", base: editorBase)
        let stored = try await database.note(id: server.id)
        XCTAssertEqual(stored?.title, "Neu vom Agenten", "a body-only save reverted the agent's title")
    }

    /// a POST applied but answered with a 5xx; the pull then fetches
    /// the created note by its server ID and lists it next to the local row.
    func testALostCreateResponseListsTheNoteOnce() async throws {
        let database = try StateDatabase(path: proofDatabasePath())
        let server = FakeNoteServer()
        let api = FlakyAPI(server)
        try await database.insertLocalNote(Note.local(id: "0198a2b9-0000-7000-8000-00000000e004", title: "", document: "Neu", at: Date()))
        await api.failNextAfterApply { stored in await server.seed(stored, cursor: 70) }
        try await SyncEngine(database: database, api: api).sync()
        let listed = try await database.notes()
        XCTAssertEqual(listed.count, 1, "the note shows twice until the replay: \(listed.map(\.id))")
    }
}


extension NoteSyncTests {
    /// singleLine regression on iOS. A first line of control characters
    /// only derives an empty title, so a note with text on line two counts as
    /// empty: it is never created, and an existing one is archived on close.
    @MainActor
    func testAControlOnlyFirstLineIsNotAnEmptyNote() async throws {
        let database = try StateDatabase(path: FileManager.default.temporaryDirectory.appending(path: "state-proof-\(UUID().uuidString).sqlite").path())
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "state-proof-\(UUID().uuidString)"))
        let model = AppModel(database: database, sessionRepository: SessionRepository(defaults: defaults))
        let created = await model.createNote(title: "", document: "\u{1B}\nEinkaufsliste Milch")
        XCTAssertNotNil(created, "typed text was discarded: the new note is never created")

        let note = Note.local(id: "0198a2b9-0000-7000-8000-00000000e005", title: "", document: "Alt", at: Date())
        try await database.applyServer(note: note)
        let outcome = await model.saveNote(id: note.id, title: "", document: "\u{1B}\nEinkaufsliste Milch", base: note, isFinal: true)
        XCTAssertNotEqual(outcome, .archivedEmpty, "a note with text was archived as empty")
    }
}
