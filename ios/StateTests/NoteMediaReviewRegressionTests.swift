import Foundation
import XCTest
@testable import State

/// Regression tests from the review of Stage B media sync.
final class NoteMediaReviewRegressionTests: XCTestCase {
    /// An edit typed while the create of a photo note is in flight must not
    /// strip capture and AI data from the local note.
    func testEditDuringInflightCreateKeepsCaptureAndAttachments() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        var local = Note.local(id: UUIDv7.generate().uuidString.lowercased(), title: "", document: "", at: Date())
        local.capture = "image"
        try await database.insertLocalNote(local)
        let inflight = NoteInflight(method: "POST", path: "/api/v1/notes", body: Data("{}".utf8), version: 1)
        _ = try await database.beginNotePush(id: local.id, inflight: inflight)
        _ = try await database.editNote(id: local.id) { $0.apply(title: nil, document: "typed meanwhile") }

        var server = Note.local(id: UUIDv7.generate().uuidString.lowercased(), title: "", document: "", at: Date())
        server.capture = "image"
        server.processing = NoteProcessing(status: NoteProcessing.queued)
        try await database.completeNotePush(localID: local.id, sentVersion: 1, server: server)

        let stored = try await database.note(id: server.id)
        XCTAssertEqual(stored?.document, "typed meanwhile")
        XCTAssertEqual(stored?.capture, "image", "capture dropped by completePush")
        XCTAssertEqual(stored?.processing?.status, NoteProcessing.queued, "processing dropped by completePush")
    }

    /// A server error on one upload must not stop the pull of reminders.
    func testTransientUploadErrorDoesNotBlockPull() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let server = ReviewRegressionFakeServer()
        await server.setUploadError(StateAPIError.server(status: 503, code: "internal_error"))
        _ = try await createCaptureNote(in: database)
        do { try await SyncEngine(database: database, api: server).sync() } catch {}
        let pulls = await server.pulls
        XCTAssertEqual(pulls, 1, "pullChanges never ran because pushNoteMedia threw")
    }

    /// "Try again" while the first processing request is still unsent must
    /// not produce two processing requests.
    func testRetryWhileProcessingUnsentSendsOnce() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let server = ReviewRegressionFakeServer()
        await server.setUploadError(StateAPIError.server(status: 400, code: "invalid_input"))
        await server.failNextProcessing()
        let localID = try await createCaptureNote(in: database)
        do { try await SyncEngine(database: database, api: server).sync() } catch {}
        let serverID = try await database.note(id: localID)!.id
        // AppModel.retryNoteProcessing with the id the detail view holds
        try await database.retryNoteUploads(noteID: serverID)
        try await database.requestNoteProcessingAgain(noteID: serverID)
        do { try await SyncEngine(database: database, api: server).sync() } catch {}
        let sent = await server.processing
        XCTAssertEqual(sent.count, 1, "processing requested \(sent.count) times: \(sent)")
    }

    /// Upload confirmed, file moved to the cache, app killed before
    /// markNoteUploadDone: the next sync must not report the file missing.
    func testMovedButUnmarkedUploadIsNotReportedMissing() async throws {
        let database = try StateDatabase(path: temporaryDatabasePath())
        let server = ReviewRegressionFakeServer()
        let localID = try await createCaptureNote(in: database)
        let pending = try await database.noteUploads(for: localID)
        NoteMediaFiles.keepUploaded(pending[0])
        try await SyncEngine(database: database, api: server).sync()
        let after = try await database.noteUploads(for: localID)
        XCTAssertTrue(after.isEmpty, "upload reported as \(after.map { "\($0.status): \($0.error ?? "")" })")
    }

    private func createCaptureNote(in database: StateDatabase) async throws -> String {
        var note = Note.local(id: UUIDv7.generate().uuidString.lowercased(), title: "", document: "", at: Date())
        note.capture = "image"
        try await database.insertLocalNote(note)
        let stored = try NoteMediaFiles.save(Data(UUID().uuidString.utf8), fileExtension: "jpg")
        let upload = NoteUpload(id: UUIDv7.generate().uuidString.lowercased(), noteID: note.id, ordinal: 0, kind: "image",
                                mimeType: "image/jpeg", fileName: stored.fileName, sha256: stored.sha256, byteSize: stored.byteSize,
                                durationMs: nil, requestID: UUIDv7.generate().uuidString.lowercased(), status: NoteUpload.pending)
        try await database.enqueueNoteUploads([upload], processRequestID: UUIDv7.generate().uuidString.lowercased(), noteID: note.id)
        return note.id
    }

    private func temporaryDatabasePath() -> String {
        FileManager.default.temporaryDirectory.appendingPathComponent("review-\(UUID().uuidString).sqlite").path
    }
}

private actor ReviewRegressionFakeServer: StateAPI {
    private var notes: [String: Note] = [:]
    private var uploadError: Error?
    private var failProcessingOnce = false
    private(set) var pulls = 0
    private(set) var processing: [String] = []

    func setUploadError(_ error: Error) { uploadError = error }
    func failNextProcessing() { failProcessingOnce = true }

    func getChanges(after: Int64, limit: Int) async throws -> ChangesResponse {
        pulls += 1
        return ChangesResponse(changes: [], cursor: after)
    }

    func getReminder(id: String) async throws -> ReminderDetail { throw StateAPIError.notFound }

    func getNote(id: String) async throws -> Note {
        guard let note = notes[id] else { throw StateAPIError.notFound }
        return note
    }

    func send(mutation: PendingMutation) async throws -> Data {
        let body = try JSONSerialization.jsonObject(with: mutation.body) as? [String: Any] ?? [:]
        var note = Note.local(id: UUIDv7.generate().uuidString.lowercased(), title: "", document: body["document"] as? String ?? "", at: Date())
        note.capture = body["capture"] as? String
        notes[note.id] = note
        return try StateJSON.encoder.encode(note)
    }

    func confirmOccurrences(_ identifiers: [String]) async throws {}

    func uploadNoteAttachment(noteID: String, upload: NoteUpload, data: Data) async throws -> Note {
        if let uploadError { throw uploadError }
        guard var note = notes[noteID] else { throw StateAPIError.notFound }
        note.attachments = (note.attachments ?? []) + [NoteAttachment(id: upload.requestID, noteID: noteID, ordinal: upload.ordinal, kind: upload.kind,
                                                                      mimeType: upload.mimeType, byteSize: upload.byteSize, sha256: upload.sha256)]
        notes[noteID] = note
        return note
    }

    func requestNoteProcessing(noteID: String, requestID: String) async throws -> Note {
        if failProcessingOnce {
            failProcessingOnce = false
            throw URLError(.networkConnectionLost)
        }
        processing.append(requestID)
        guard var note = notes[noteID] else { throw StateAPIError.notFound }
        note.processing = NoteProcessing(status: NoteProcessing.queued)
        notes[noteID] = note
        return note
    }
}
