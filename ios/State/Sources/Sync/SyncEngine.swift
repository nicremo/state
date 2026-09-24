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
        try await pullChanges()
    }

    private func pushPendingMutations() async throws {
        let mutations = try await database.pendingMutations()
        for mutation in mutations {
            do {
                let response = try await api.send(mutation: mutation)
                if mutation.method == "POST", mutation.path == "/api/v1/reminders" {
                    try await applyCreatedReminder(response: response, provisionalID: mutation.entityID)
                }
                if mutation.method == "POST", mutation.path == Self.notesPath {
                    try await applyStoredNote(response: response, provisionalID: mutation.entityID)
                }
                if mutation.method == "PATCH", mutation.path.hasPrefix(Self.notesPath + "/") {
                    try await applyStoredNote(response: response, provisionalID: nil)
                }
                try await database.removeMutation(id: mutation.id)
            } catch let StateAPIError.revisionConflict(serverSnapshot) where mutation.path.hasPrefix(Self.notesPath + "/") {
                try await keepConflictingNote(mutation: mutation, serverSnapshot: serverSnapshot)
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

    /// Replaces a provisional note with the one the server stored.
    private func applyStoredNote(response: Data, provisionalID: String?) async throws {
        guard let note = try? StateJSON.decoder.decode(Note.self, from: response) else { return }
        try await database.apply(note: note)
        if let provisionalID, provisionalID != note.id {
            try await database.deleteNote(id: provisionalID)
        }
    }

    /// A note edited on two devices keeps both texts: the server version stays
    /// the note, the local text becomes a new note, as a conflicted copy. No
    /// merge dialog, and nothing typed offline is lost.
    private func keepConflictingNote(mutation: PendingMutation, serverSnapshot: Data) async throws {
        guard let entityID = mutation.entityID,
              let local = try await database.note(id: entityID),
              let server = try? StateJSON.decoder.decode(Note.self, from: Self.serverObject(in: serverSnapshot))
        else { return }
        try await database.apply(note: server)
        guard local.document != server.document else { return }
        let now = Date()
        let copyID = UUIDv7.generate().uuidString.lowercased()
        let copyTitle = String(localized: "\(local.title) (conflict copy)")
        let copy = Note.local(id: copyID, title: copyTitle, document: local.document, at: now)
        try await database.apply(note: copy)
        let requestID = UUIDv7.generate().uuidString.lowercased()
        let request = CreateNoteRequest(
            title: copyTitle,
            document: local.document,
            clientTime: now,
            source: "ios",
            clientRequestID: requestID
        )
        _ = try await database.enqueue(
            method: "POST",
            path: Self.notesPath,
            body: StateJSON.encoder.encode(request),
            entityID: copyID
        )
    }

    /// The REST error body wraps the current object as details.server; a bare
    /// object is accepted too.
    private static func serverObject(in snapshot: Data) -> Data {
        guard
            let object = (try? JSONSerialization.jsonObject(with: snapshot)) as? [String: Any],
            let details = object["details"] as? [String: Any],
            let server = details["server"],
            let data = try? JSONSerialization.data(withJSONObject: server)
        else { return snapshot }
        return data
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
                    try await database.apply(note: try await api.getNote(id: identifier))
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
