import Foundation

actor SyncEngine {
    private let database: StateDatabase
    private let api: any StateAPI

    init(database: StateDatabase, api: any StateAPI) {
        self.database = database
        self.api = api
    }

    func sync() async throws {
        try await pushPendingMutations()
        try await pushDirtyNotes()
        try await pullChanges()
        // Media goes last: a slow or failing upload never holds up the
        // reminders or the other notes.
        try await pushNoteMedia()
    }

    /// Uploads photos and recordings of notes the server already knows, then
    /// asks for processing once a note's files are all in. A file the server
    /// refuses for good is marked and skipped, like a rejected note.
    private func pushNoteMedia() async throws {
        for upload in try await database.readyNoteUploads() {
            guard let data = NoteMediaFiles.uploadData(upload) else {
                try await database.markNoteUploadFailed(id: upload.id, message: String(localized: "The file is missing on this device."))
                continue
            }
            do {
                let note = try await api.uploadNoteAttachment(noteID: upload.noteID, upload: upload, data: data)
                try await database.applyServer(note: note)
                // Marked first, moved second: a stop in between leaves a
                // finished upload whose file is still found in the queue.
                try await database.markNoteUploadDone(id: upload.id)
                NoteMediaFiles.keepUploaded(upload)
            } catch StateAPIError.unauthorized {
                throw StateAPIError.unauthorized
            } catch let error where Self.isTransient(error) {
                // Offline or a busy server: the rest waits for the next sync.
                return
            } catch let StateAPIError.server(_, code) {
                try await database.markNoteUploadFailed(id: upload.id, message: Self.uploadFailure(code: code))
            } catch {
                try await database.markNoteUploadFailed(id: upload.id, message: String(localized: "The upload failed."))
            }
        }
        for request in try await database.readyProcessRequests() {
            do {
                let note = try await api.requestNoteProcessing(noteID: request.noteID, requestID: request.requestID)
                try await database.applyServer(note: note)
                try await database.markProcessRequestSent(noteID: request.noteID, requestID: request.requestID)
            } catch StateAPIError.unauthorized {
                throw StateAPIError.unauthorized
            } catch let error where Self.isTransient(error) {
                return
            } catch {
                // Refused for good: the note shows its state from the server.
                try await database.markProcessRequestSent(noteID: request.noteID, requestID: request.requestID)
            }
        }
    }

    /// Worth another try later: no connection, a timeout, rate limiting or
    /// a server error.
    private static func isTransient(_ error: Error) -> Bool {
        if error is URLError { return true }
        if case let StateAPIError.server(status, _) = error {
            return status >= 500 || status == 408 || status == 429
        }
        return false
    }

    private static func uploadFailure(code: String) -> String {
        switch code {
        case "invalid_input": String(localized: "The server did not accept this file.")
        case "forbidden": String(localized: "This device may not add files.")
        default: String(localized: "The upload failed.")
        }
    }

    private func pushPendingMutations() async throws {
        let mutations = try await database.pendingMutations()
        for mutation in mutations {
            do {
                let response = try await api.send(mutation: mutation)
                if mutation.method == "POST", mutation.path == "/api/v1/reminders" {
                    try await applyCreatedReminder(response: response, provisionalID: mutation.entityID)
                }
                try await database.removeMutation(id: mutation.id)
            } catch let StateAPIError.revisionConflict(serverSnapshot) {
                try await recordConflict(mutation: mutation, serverSnapshot: serverSnapshot)
                try await database.removeMutation(id: mutation.id)
            } catch {
                try await database.incrementAttempts(id: mutation.id)
                throw error
            }
        }
    }

    private func applyCreatedReminder(response: Data, provisionalID: String?) async throws {
        guard let reminder = try? StateJSON.decoder.decode(Reminder.self, from: response) else {
            return
        }
        let detail = try await api.getReminder(id: reminder.id)
        try await database.apply(detail: detail, cursor: nil)
        if let provisionalID, provisionalID != reminder.id {
            try await database.deleteReminder(id: provisionalID)
        }
    }

    static let notesPath = "/api/v1/notes"

    /// Notes do not queue one request per edit. Each note carries a dirty
    /// flag and the server version its edits are based on; a push sends the
    /// difference. A note the server rejects for good is marked and skipped,
    /// so it can never hold up other notes or the reminder queue.
    private func pushDirtyNotes() async throws {
        for record in try await database.dirtyNoteRecords() {
            do {
                try await push(record)
            } catch let error as URLError {
                throw error
            } catch StateAPIError.unauthorized {
                throw StateAPIError.unauthorized
            } catch StateAPIError.server(let status, _) where Self.isTransient(status) {
                // The server could not answer this time; the stored request
                // goes out unchanged with the next sync.
                continue
            } catch is CancellationError {
                throw CancellationError()
            } catch let error as StateAPIError {
                try await database.markNoteSyncError(id: record.note.id, message: error.localizedDescription)
            } catch is DecodingError {
                try await database.markNoteSyncError(id: record.note.id, message: StateAPIError.invalidResponse.localizedDescription)
            }
        }
    }

    private static func isTransient(_ status: Int) -> Bool {
        status == 408 || status == 409 || status == 425 || status == 429 || status >= 500
    }

    /// One note, one request at a time. A request that was sent but not
    /// answered is replayed byte for byte; only after its answer is the rest
    /// of the local edit sent as a new request with a new ID.
    private func push(_ record: NoteRecord) async throws {
        if let inflight = record.inflight {
            try await send(inflight, for: record.note.id)
            return
        }
        let note = record.note
        guard let base = record.base else {
            if note.archived {
                // Never sent, so the server has never seen it.
                try await database.deleteLocalNote(id: note.id)
                return
            }
            let request = CreateNoteRequest(
                title: note.titleSource == Note.userSource ? note.title : nil,
                document: note.document,
                capture: note.capture,
                clientTime: note.updatedAt,
                source: "ios",
                clientRequestID: record.createRequestID ?? UUIDv7.generate().uuidString.lowercased()
            )
            try await begin(NoteInflight(method: "POST", path: Self.notesPath, body: try StateJSON.encoder.encode(request), version: record.localVersion), for: note.id)
            return
        }

        var title: String?
        if note.titleSource == Note.userSource, base.titleSource != Note.userSource || base.title != note.title {
            title = note.title
        } else if note.titleSource == Note.derivedSource, base.titleSource == Note.userSource {
            title = ""
        }
        let document = note.document == base.document ? nil : note.document
        let archived = note.archived == base.archived ? nil : note.archived
        guard title != nil || document != nil || archived != nil else {
            // Nothing left to send (an edit that was undone). A newer server
            // version seen meanwhile becomes the note.
            try await database.completeNotePush(localID: note.id, sentVersion: record.localVersion, server: record.latest ?? base)
            return
        }
        let request = UpdateNoteRequest(
            title: title,
            document: document,
            archived: archived,
            expectedRevision: base.revision,
            clientTime: note.updatedAt,
            source: "ios",
            clientRequestID: UUIDv7.generate().uuidString.lowercased()
        )
        try await begin(
            NoteInflight(method: "PATCH", path: "\(Self.notesPath)/\(note.id)", body: try StateJSON.encoder.encode(request), version: record.localVersion),
            for: note.id
        )
    }

    private func begin(_ inflight: NoteInflight, for noteID: String) async throws {
        // Send what is stored, never a body the database did not keep.
        guard let stored = try await database.beginNotePush(id: noteID, inflight: inflight) else { return }
        try await send(stored, for: noteID)
    }

    private func send(_ inflight: NoteInflight, for noteID: String) async throws {
        let mutation = PendingMutation(
            id: UUIDv7.generate().uuidString.lowercased(),
            method: inflight.method,
            path: inflight.path,
            body: inflight.body,
            entityID: noteID,
            createdAt: Date(),
            attempts: 0
        )
        do {
            let response = try await api.send(mutation: mutation)
            let stored = try StateJSON.decoder.decode(Note.self, from: response)
            try await database.completeNotePush(localID: noteID, sentVersion: inflight.version, server: stored)
        } catch let StateAPIError.revisionConflict(snapshot) where inflight.method == "PATCH" {
            let server = try StateJSON.decoder.decode(Note.self, from: snapshot)
            try await database.resolveNoteConflict(localID: noteID, server: server) { title in
                NoteText.conflictCopyTitle(for: title)
            }
        }
    }


    private func pullChanges() async throws {
        var currentCursor = try await database.cursor()
        while true {
            let response = try await api.getChanges(after: currentCursor, limit: 100)
            guard !response.changes.isEmpty else { return }
            // Policy, project and runner events carry no reminder ID. They are
            // fetched through the global lists in AppModel.synchronize, so the
            // pull only groups reminder-scoped events.
            let noteIDs = Array(Set(response.changes.compactMap(\.event.noteID))).sorted()
            for identifier in noteIDs {
                do {
                    try await database.applyServer(note: try await api.getNote(id: identifier))
                } catch StateAPIError.notFound {
                    // A server without notes, or a note this client may not read.
                }
            }
            let reminderIDs = Array(Set(response.changes.compactMap(\.event.reminderID))).sorted()
            if reminderIDs.isEmpty {
                try await database.advanceCursor(to: response.cursor)
            }
            var details: [ReminderDetail] = []
            for identifier in reminderIDs {
                details.append(try await api.getReminder(id: identifier))
            }
            for (index, detail) in details.enumerated() {
                let cursor = index == details.indices.last ? response.cursor : nil
                try await database.apply(detail: detail, cursor: cursor)
            }
            if response.cursor <= currentCursor || response.changes.count < 100 {
                return
            }
            currentCursor = response.cursor
        }
    }

    private func recordConflict(mutation: PendingMutation, serverSnapshot: Data) async throws {
        guard
            let entityID = mutation.entityID,
            let local = try await database.reminder(id: entityID)
        else {
            return
        }
        let fields = changedFields(in: mutation.body)
        try await database.recordConflict(
            entityID: entityID,
            server: serverSnapshot,
            local: StateJSON.encoder.encode(local),
            fields: fields
        )
    }

    private func changedFields(in body: Data) -> [String] {
        guard let object = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any] else {
            return []
        }
        let metadata = Set([
            "client_request_id",
            "client_time",
            "correlation_id",
            "expected_revision",
            "source",
            "source_excerpt",
        ])
        return object.keys.filter { !metadata.contains($0) }.sorted()
    }
}
