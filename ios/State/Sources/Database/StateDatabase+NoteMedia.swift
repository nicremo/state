import Foundation
import GRDB

/// The upload queue for photos and recordings. A capture note is created
/// like any note; its files wait here until the server knows the note, then
/// go up one by one, and the processing request follows once all are in.
extension StateDatabase {
    /// Queues files for a note and, when asked, one processing request.
    func enqueueNoteUploads(_ uploads: [NoteUpload], processRequestID: String?, noteID: String) async throws {
        try await pool.write { database in
            for upload in uploads {
                try database.execute(
                    sql: """
                    INSERT INTO note_upload (id, note_id, ordinal, kind, mime_type, file_name, sha256, byte_size,
                                             duration_ms, request_id, status, error, created_at)
                    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)
                    """,
                    arguments: [upload.id, upload.noteID, upload.ordinal, upload.kind, upload.mimeType, upload.fileName,
                                upload.sha256, upload.byteSize, upload.durationMs, upload.requestID, NoteUpload.pending, Date()]
                )
            }
            if let processRequestID {
                try database.execute(
                    sql: "INSERT OR IGNORE INTO note_process_request (note_id, request_id, sent) VALUES (?, ?, 0)",
                    arguments: [noteID, processRequestID]
                )
            }
        }
    }

    /// Uploads whose note already exists on the server, with the note's
    /// server identifier. Uploads of a note still being created wait.
    func readyNoteUploads() async throws -> [NoteUpload] {
        try await pool.read { database in
            try Row.fetchAll(database, sql: "SELECT * FROM note_upload WHERE status = ? ORDER BY created_at, ordinal", arguments: [NoteUpload.pending])
                .compactMap { row -> NoteUpload? in
                    var upload = Self.noteUpload(row: row)
                    guard let record = try Self.noteRecord(id: upload.noteID, in: database), record.base != nil else { return nil }
                    upload.noteID = record.note.id
                    return upload
                }
        }
    }

    /// Every upload of a note, for its screen: waiting ones show from the
    /// local file, failed ones say why.
    func noteUploads(for noteID: String) async throws -> [NoteUpload] {
        try await pool.read { database in
            let resolved = try Self.noteRecord(id: noteID, in: database)?.note.id ?? noteID
            let aliases = try String.fetchAll(database, sql: "SELECT provisional_id FROM note_alias WHERE server_id = ?", arguments: [resolved])
            let identifiers = [resolved] + aliases
            let marks = databaseQuestionMarks(count: identifiers.count)
            return try Row.fetchAll(database, sql: "SELECT * FROM note_upload WHERE note_id IN (\(marks)) AND status != ? ORDER BY ordinal",
                                    arguments: StatementArguments(identifiers + [NoteUpload.done]))
                .map(Self.noteUpload(row:))
        }
    }

    func markNoteUploadDone(id: String) async throws {
        try await pool.write { database in
            try database.execute(sql: "UPDATE note_upload SET status = ?, error = NULL WHERE id = ?", arguments: [NoteUpload.done, id])
        }
    }

    func markNoteUploadFailed(id: String, message: String) async throws {
        try await pool.write { database in
            try database.execute(sql: "UPDATE note_upload SET status = ?, error = ? WHERE id = ?", arguments: [NoteUpload.failed, message, id])
        }
    }

    /// Puts failed uploads of a note back in the queue, for "Try again".
    func retryNoteUploads(noteID: String) async throws {
        try await pool.write { database in
            let resolved = try Self.noteRecord(id: noteID, in: database)?.note.id ?? noteID
            let aliases = try String.fetchAll(database, sql: "SELECT provisional_id FROM note_alias WHERE server_id = ?", arguments: [resolved])
            for identifier in [resolved] + aliases {
                try database.execute(sql: "UPDATE note_upload SET status = ?, error = NULL WHERE note_id = ? AND status = ?",
                                     arguments: [NoteUpload.pending, identifier, NoteUpload.failed])
            }
        }
    }

    /// Processing requests that are due: the note exists on the server and
    /// none of its files is still waiting.
    func readyProcessRequests() async throws -> [(noteID: String, requestID: String)] {
        try await pool.read { database in
            try Row.fetchAll(database, sql: "SELECT note_id, request_id FROM note_process_request WHERE sent = 0").compactMap { row in
                let localID: String = row["note_id"]
                guard let record = try Self.noteRecord(id: localID, in: database), record.base != nil else { return nil }
                let aliases = try String.fetchAll(database, sql: "SELECT provisional_id FROM note_alias WHERE server_id = ?", arguments: [record.note.id])
                let identifiers = [record.note.id] + aliases
                let waiting = try Int.fetchOne(
                    database,
                    sql: "SELECT COUNT(*) FROM note_upload WHERE status = ? AND note_id IN (\(databaseQuestionMarks(count: identifiers.count)))",
                    arguments: StatementArguments([NoteUpload.pending] + identifiers)
                ) ?? 0
                return waiting == 0 ? (record.note.id, row["request_id"]) : nil
            }
        }
    }

    func markProcessRequestSent(noteID: String, requestID: String) async throws {
        try await pool.write { database in
            try database.execute(sql: "UPDATE note_process_request SET sent = 1 WHERE request_id = ?", arguments: [requestID])
        }
    }

    /// Queues a fresh processing request, for "Try again" after a failure.
    /// A request still waiting under the note's provisional ID is replaced,
    /// never sent next to the new one.
    func requestNoteProcessingAgain(noteID: String) async throws {
        try await pool.write { database in
            let resolved = try Self.noteRecord(id: noteID, in: database)?.note.id ?? noteID
            let aliases = try String.fetchAll(database, sql: "SELECT provisional_id FROM note_alias WHERE server_id = ?", arguments: [resolved])
            for identifier in Set([noteID, resolved] + aliases) {
                try database.execute(sql: "DELETE FROM note_process_request WHERE note_id = ?", arguments: [identifier])
            }
            try database.execute(sql: "INSERT INTO note_process_request (note_id, request_id, sent) VALUES (?, ?, 0)",
                                 arguments: [resolved, UUIDv7.generate().uuidString.lowercased()])
        }
    }

    private static func noteUpload(row: Row) -> NoteUpload {
        NoteUpload(
            id: row["id"],
            noteID: row["note_id"],
            ordinal: row["ordinal"],
            kind: row["kind"],
            mimeType: row["mime_type"],
            fileName: row["file_name"],
            sha256: row["sha256"],
            byteSize: row["byte_size"],
            durationMs: row["duration_ms"],
            requestID: row["request_id"],
            status: row["status"],
            error: row["error"]
        )
    }
}
