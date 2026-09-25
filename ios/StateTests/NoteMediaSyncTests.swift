import Foundation
import XCTest
@testable import State

/// Photos and recordings wait on the device until their note exists on the
/// server, go up with a stable request ID, and the processing request
/// follows once, after the last file.
final class NoteMediaSyncTests: XCTestCase {
    func testDecodesAServerNoteViewWithAIData() throws {
        let json = """
        {"id":"n1","title":"Seite","title_source":"ai","document":"","plain_text":"","summary":"KI",
         "summary_source":"ai","capture":"image","archived":false,"revision":2,
         "created_at":"2026-09-25T10:00:00Z","updated_at":"2026-09-25T10:00:00Z",
         "attachments":[{"id":"a1","note_id":"n1","ordinal":0,"kind":"image","mime_type":"image/jpeg","byte_size":10,
           "sha256":"\(String(repeating: "a", count: 64))","derived_text":"Brot","derived_kind":"ocr","derived_model":"m",
           "created_at":"2026-09-25T10:00:00Z"}],
         "processing":{"status":"ready","model":"deepseek/deepseek-v4.1-flash","updated_at":"0001-01-01T00:00:00Z"},
         "ai":{"title":"Seite","summary":"KI","model":"deepseek/deepseek-v4.1-flash","source_hash":"h","updated_at":"2026-09-25T10:00:00Z"},
         "relations":[{"id":"r1","note_id":"n1","related_note_id":"n2","related_title":"Andere","reason":"Beide Karla","confidence":0.8,
           "created_by":"notes-agent","created_at":"2026-09-25T10:00:00Z"}],
         "reminder_proposals":[{"id":"p1","note_id":"n1","title":"Anrufen","local_date":"2026-10-02","reason":"steht da",
           "status":"pending","created_at":"2026-09-25T10:00:00Z","updated_at":"2026-09-25T10:00:00Z"}]}
        """
        let note = try StateJSON.decoder.decode(Note.self, from: Data(json.utf8))
        XCTAssertEqual(note.titleSource, Note.aiSource)
        XCTAssertEqual(note.attachments?.first?.derivedText, "Brot")
        XCTAssertEqual(note.processing?.status, NoteProcessing.ready)
        XCTAssertEqual(note.relations?.first?.relatedNoteID, "n2")
        XCTAssertEqual(note.reminderProposals?.first?.localDate, "2026-10-02")
        // Stored and read back, nothing of it is lost.
        let roundTrip = try StateJSON.decoder.decode(Note.self, from: StateJSON.encoder.encode(note))
        XCTAssertEqual(roundTrip, note)
    }

    func testAnAITitleSurvivesEditsAndIsNeverPushedAsWritten() {
        var note = Note.local(id: "n", title: "", document: "Erste Zeile\nmehr", at: Date())
        note.title = "KI Titel"
        note.titleSource = Note.aiSource
        note.apply(title: "", document: "Erste Zeile\n- [x] mehr")
        XCTAssertEqual(note.title, "KI Titel")
        XCTAssertEqual(note.titleSource, Note.aiSource)
        note.apply(title: "Eigener", document: note.document)
        XCTAssertEqual(note.titleSource, Note.userSource, "a written title still wins")
    }

    func testUploadsWaitForTheNoteThenGoUpOnceAndProcessingFollows() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let server = MediaFakeServer()
        let engine = SyncEngine(database: database, api: server)
        let noteID = try await createCaptureNote(in: database, files: 2)

        await server.failNextUpload()
        do {
            try await engine.sync()
            XCTFail("a network failure must surface")
        } catch {}
        let firstAttempt = await server.uploadRequestIDs
        XCTAssertEqual(firstAttempt.count, 1)
        let processingBefore = await server.processingRequests
        XCTAssertEqual(processingBefore.count, 0, "processing must wait for every file")

        try await engine.sync()
        let requestIDs = await server.uploadRequestIDs
        XCTAssertEqual(requestIDs.count, 3)
        XCTAssertEqual(requestIDs[0], requestIDs[1], "a retry sends the same request ID")
        let processing = await server.processingRequests
        XCTAssertEqual(processing.count, 1)
        let captures = await server.createdCaptures
        XCTAssertEqual(captures, ["image"])

        try await engine.sync()
        let processingAfter = await server.processingRequests
        XCTAssertEqual(processingAfter.count, 1, "processing is requested once")
        let stored = try await database.note(id: noteID)
        XCTAssertEqual(stored?.attachments?.count, 2)
        XCTAssertEqual(stored?.processing?.status, NoteProcessing.queued)
        let waiting = try await database.noteUploads(for: noteID)
        XCTAssertTrue(waiting.isEmpty)
    }

    func testARefusedFileIsMarkedAndDoesNotBlockProcessing() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let server = MediaFakeServer()
        await server.refuseUploads()
        let engine = SyncEngine(database: database, api: server)
        let noteID = try await createCaptureNote(in: database, files: 1)

        try await engine.sync()
        let uploads = try await database.noteUploads(for: noteID)
        XCTAssertEqual(uploads.first?.status, NoteUpload.failed)
        XCTAssertNotNil(uploads.first?.error)
        let processing = await server.processingRequests
        XCTAssertEqual(processing.count, 1)

        try await database.retryNoteUploads(noteID: noteID)
        let retried = try await database.noteUploads(for: noteID)
        XCTAssertEqual(retried.first?.status, NoteUpload.pending)
    }

    func testTheQueueSurvivesARestart() async throws {
        let path = temporaryDatabasePath()
        do {
            let database = try StateDatabase(path: path)
            _ = try await createCaptureNote(in: database, files: 1)
        }
        let reopened = try StateDatabase(path: path)
        let server = MediaFakeServer()
        try await SyncEngine(database: reopened, api: server).sync()
        let uploads = await server.uploadRequestIDs
        XCTAssertEqual(uploads.count, 1)
    }

    func testAConflictCopyNeverTakesTheAttachments() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        var server = Note.local(id: "0198a2b9-0000-7000-8000-00000000b001", title: "", document: "Server", at: Date())
        server.attachments = [NoteAttachment(id: "a", noteID: server.id, ordinal: 0, kind: "image", mimeType: "image/jpeg", byteSize: 1, sha256: String(repeating: "b", count: 64))]
        try await database.applyServer(note: server)
        _ = try await database.editNote(id: server.id) { $0.apply(title: nil, document: "Lokal") }
        var changed = server
        changed.apply(title: nil, document: "Anders")
        changed.revision = 2
        try await database.resolveNoteConflict(localID: server.id, server: changed) { "\($0) (copy)" }
        let copies = try await database.notes().filter { $0.id != server.id }
        XCTAssertEqual(copies.count, 1)
        XCTAssertNil(copies.first?.attachments)
    }

    private func createCaptureNote(in database: StateDatabase, files: Int) async throws -> String {
        var note = Note.local(id: UUIDv7.generate().uuidString.lowercased(), title: "", document: "", at: Date())
        note.capture = "image"
        try await database.insertLocalNote(note)
        var uploads: [NoteUpload] = []
        for ordinal in 0..<files {
            let stored = try NoteMediaFiles.save(Data([0xFF, 0xD8, 0xFF, UInt8(ordinal)]), fileExtension: "jpg")
            uploads.append(NoteUpload(id: UUIDv7.generate().uuidString.lowercased(), noteID: note.id, ordinal: ordinal, kind: "image",
                                      mimeType: "image/jpeg", fileName: stored.fileName, sha256: stored.sha256, byteSize: stored.byteSize,
                                      durationMs: nil, requestID: UUIDv7.generate().uuidString.lowercased(), status: NoteUpload.pending))
        }
        try await database.enqueueNoteUploads(uploads, processRequestID: UUIDv7.generate().uuidString.lowercased(), noteID: note.id)
        return note.id
    }

    private func temporaryDatabasePath() -> String {
        FileManager.default.temporaryDirectory.appendingPathComponent("media-\(UUID().uuidString).sqlite").path
    }
}

/// Serves note creation, uploads and processing requests like the server.
private actor MediaFakeServer: StateAPI {
    private var notes: [String: Note] = [:]
    private var createRequests: [String: Note] = [:]
    private(set) var uploadRequestIDs: [String] = []
    private(set) var processingRequests: [String] = []
    private(set) var createdCaptures: [String] = []
    private var failNext = false
    private var refuse = false

    func failNextUpload() { failNext = true }
    func refuseUploads() { refuse = true }

    func getChanges(after: Int64, limit: Int) async throws -> ChangesResponse {
        ChangesResponse(changes: [], cursor: after)
    }

    func getReminder(id: String) async throws -> ReminderDetail { throw StateAPIError.notFound }

    func getNote(id: String) async throws -> Note {
        guard let note = notes[id] else { throw StateAPIError.notFound }
        return note
    }

    func send(mutation: PendingMutation) async throws -> Data {
        let body = try JSONSerialization.jsonObject(with: mutation.body) as? [String: Any] ?? [:]
        let requestID = body["client_request_id"] as? String ?? ""
        if let replay = createRequests[requestID] { return try StateJSON.encoder.encode(replay) }
        var note = Note.local(id: UUIDv7.generate().uuidString.lowercased(), title: "", document: body["document"] as? String ?? "", at: Date())
        note.capture = body["capture"] as? String
        createdCaptures.append(note.capture ?? "text")
        notes[note.id] = note
        createRequests[requestID] = note
        return try StateJSON.encoder.encode(note)
    }

    func confirmOccurrences(_ identifiers: [String]) async throws {}

    func uploadNoteAttachment(noteID: String, upload: NoteUpload, data: Data) async throws -> Note {
        uploadRequestIDs.append(upload.requestID)
        if failNext {
            failNext = false
            throw URLError(.networkConnectionLost)
        }
        if refuse { throw StateAPIError.server(status: 400, code: "invalid_input") }
        guard var note = notes[noteID] else { throw StateAPIError.notFound }
        XCTAssertEqual(NoteMediaFiles.sha256(data), upload.sha256)
        var attachments = note.attachments ?? []
        if !attachments.contains(where: { $0.id == upload.requestID }) {
            attachments.append(NoteAttachment(id: upload.requestID, noteID: noteID, ordinal: upload.ordinal, kind: upload.kind,
                                              mimeType: upload.mimeType, byteSize: upload.byteSize, sha256: upload.sha256))
        }
        note.attachments = attachments
        notes[noteID] = note
        return note
    }

    func requestNoteProcessing(noteID: String, requestID: String) async throws -> Note {
        processingRequests.append(requestID)
        guard var note = notes[noteID] else { throw StateAPIError.notFound }
        note.processing = NoteProcessing(status: NoteProcessing.queued)
        notes[noteID] = note
        return note
    }
}
