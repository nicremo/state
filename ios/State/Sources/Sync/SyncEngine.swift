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

    private func pullChanges() async throws {
        var currentCursor = try await database.cursor()
        while true {
            let response = try await api.getChanges(after: currentCursor, limit: 100)
            guard !response.changes.isEmpty else { return }
            let reminderIDs = Array(Set(response.changes.map(\.event.reminderID))).sorted()
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
