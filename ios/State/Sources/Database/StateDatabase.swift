import Foundation
import GRDB

struct PendingMutation: Identifiable, Sendable {
    let id: String
    let method: String
    let path: String
    let body: Data
    let entityID: String?
    let createdAt: Date
    let attempts: Int
}

/// A cached note plus what the sync needs: whether it has unsent edits, the
/// server version those edits are based on, a counter that tells a push
/// whether the note changed while the request was in flight, and the request
/// IDs that keep retries idempotent.
struct NoteRecord: Sendable, Equatable {
    var note: Note
    var dirty: Bool
    var localVersion: Int64
    var base: Note?
    var createRequestID: String?
    var patchRequestID: String?
    var syncError: String?
    /// The request sent and not yet answered. A retry resends exactly this,
    /// so the server's idempotency record can never answer a different body.
    var inflight: NoteInflight? = nil
    /// The newest server version seen while local edits were unsent.
    var latest: Note? = nil
    /// Set on a conflict copy: the note whose conflicting text it keeps.
    var conflictOf: String? = nil
}

struct NoteInflight: Sendable, Equatable {
    let method: String
    let path: String
    let body: Data
    /// The local version the body was built from.
    let version: Int64
}

struct StoredConflict: Identifiable, Sendable {
    let id: String
    let entityID: String
    let serverSnapshot: Data
    let localSnapshot: Data
    let fields: [String]
    let createdAt: Date
    let resolved: Bool
}

final class StateDatabase: Sendable {
    private let pool: DatabasePool

    init(path: String) throws {
        var configuration = Configuration()
        configuration.foreignKeysEnabled = true
        configuration.busyMode = .timeout(5)
        pool = try DatabasePool(path: path, configuration: configuration)
        try migrator.migrate(pool)
    }

    static func applicationDatabase() throws -> StateDatabase {
        let baseURL = Platform.sharedContainerURL
        try FileManager.default.createDirectory(at: baseURL, withIntermediateDirectories: true)
        return try StateDatabase(path: databasePath(baseURL: baseURL))
    }

    static func databasePath(baseURL: URL) -> String {
        baseURL.appending(path: "state.sqlite").path
    }

    func apply(detail: ReminderDetail, cursor: Int64?) async throws {
        let reminderJSON = try StateJSON.encoder.encode(detail.reminder)
        let scheduledAt = detail.occurrences.compactMap(\.scheduledAt).min()
        let comments = try detail.comments.map { ($0, try StateJSON.encoder.encode($0)) }
        let occurrences = try detail.occurrences.map { ($0, try StateJSON.encoder.encode($0)) }
        let history = try detail.history.map { ($0, try StateJSON.encoder.encode($0)) }
        let runs = try detail.runs.map { ($0, try StateJSON.encoder.encode($0)) }
        try await pool.write { database in
            try database.execute(
                sql: """
                INSERT INTO reminder_cache (id, title, status, revision, archived, scheduled_at, updated_at, json)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(id) DO UPDATE SET
                    title = excluded.title,
                    status = excluded.status,
                    revision = excluded.revision,
                    archived = excluded.archived,
                    scheduled_at = excluded.scheduled_at,
                    updated_at = excluded.updated_at,
                    json = excluded.json
                """,
                arguments: [
                    detail.reminder.id,
                    detail.reminder.title,
                    detail.reminder.status.rawValue,
                    detail.reminder.revision,
                    detail.reminder.archived,
                    scheduledAt,
                    detail.reminder.updatedAt,
                    reminderJSON,
                ]
            )
            try database.execute(sql: "DELETE FROM comment_cache WHERE reminder_id = ?", arguments: [detail.reminder.id])
            try database.execute(sql: "DELETE FROM occurrence_cache WHERE reminder_id = ?", arguments: [detail.reminder.id])
            try database.execute(sql: "DELETE FROM audit_cache WHERE reminder_id = ?", arguments: [detail.reminder.id])
            try database.execute(sql: "DELETE FROM run_cache WHERE reminder_id = ?", arguments: [detail.reminder.id])
            for (comment, encoded) in comments {
                try database.execute(
                    sql: "INSERT INTO comment_cache (id, reminder_id, created_at, json) VALUES (?, ?, ?, ?)",
                    arguments: [comment.id, comment.reminderID, comment.createdAt, encoded]
                )
            }
            for (occurrence, encoded) in occurrences {
                try database.execute(
                    sql: "INSERT INTO occurrence_cache (id, reminder_id, scheduled_at, status, json) VALUES (?, ?, ?, ?, ?)",
                    arguments: [occurrence.id, occurrence.reminderID, occurrence.scheduledAt, occurrence.status.rawValue, encoded]
                )
            }
            for (event, encoded) in history {
                try database.execute(
                    sql: "INSERT INTO audit_cache (id, reminder_id, server_time, action, json) VALUES (?, ?, ?, ?, ?)",
                    arguments: [event.id, event.reminderID ?? detail.reminder.id, event.serverTime, event.action, encoded]
                )
            }
            for (run, encoded) in runs {
                try database.execute(
                    sql: "INSERT INTO run_cache (id, reminder_id, status, updated_at, json) VALUES (?, ?, ?, ?, ?)",
                    arguments: [run.id, run.reminderID, run.status.rawValue, run.updatedAt, encoded]
                )
            }
            if let cursor {
                try database.execute(
                    sql: "INSERT INTO metadata (key, value) VALUES ('cursor', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
                    arguments: [String(cursor)]
                )
            }
        }
    }

    func enqueue(method: String, path: String, body: Data, entityID: String?) async throws -> PendingMutation {
        let mutation = PendingMutation(
            id: UUIDv7.generate().uuidString.lowercased(),
            method: method,
            path: path,
            body: body,
            entityID: entityID,
            createdAt: Date(),
            attempts: 0
        )
        try await pool.write { database in
            try database.execute(
                sql: "INSERT INTO pending_mutations (id, method, path, body, entity_id, created_at, attempts) VALUES (?, ?, ?, ?, ?, ?, 0)",
                arguments: [mutation.id, mutation.method, mutation.path, mutation.body, mutation.entityID, mutation.createdAt]
            )
        }
        return mutation
    }

    func pendingMutations() async throws -> [PendingMutation] {
        try await pool.read { database in
            try Row.fetchAll(database, sql: "SELECT * FROM pending_mutations ORDER BY created_at, id").map(Self.pendingMutation)
        }
    }

    func removeMutation(id: String) async throws {
        try await pool.write { database in
            try database.execute(sql: "DELETE FROM pending_mutations WHERE id = ?", arguments: [id])
        }
    }

    func deleteReminder(id: String) async throws {
        try await pool.write { database in
            try database.execute(sql: "DELETE FROM reminder_cache WHERE id = ?", arguments: [id])
        }
    }

    /// Forgets everything cached from a server, including the sync cursor, so
    /// the next sync rebuilds the cache from that server's own history.
    /// Unsent local changes survive only when they belong to the same server.
    func resetCache(keepingPendingMutations: Bool) async throws {
        try await pool.write { database in
            for table in [
                "reminder_cache", "comment_cache", "occurrence_cache", "audit_cache",
                "conflicts", "project_cache", "policy_cache", "runner_cache", "run_cache", "metadata",
            ] {
                try database.execute(sql: "DELETE FROM \(table)")
            }
            if keepingPendingMutations {
                // Unsent note edits are pending work too.
                try database.execute(sql: "DELETE FROM note_cache WHERE dirty = 0")
            } else {
                try database.execute(sql: "DELETE FROM pending_mutations")
                try database.execute(sql: "DELETE FROM note_cache")
                try database.execute(sql: "DELETE FROM note_alias")
            }
        }
    }

    /// Whether demo reminders are still cached. Demo identifiers share a fixed
    /// synthetic prefix that a generated UUIDv7 never carries.
    func containsDemoContent() async throws -> Bool {
        try await pool.read { database in
            try Bool.fetchOne(
                database,
                sql: """
                SELECT EXISTS(SELECT 1 FROM reminder_cache WHERE id LIKE ?)
                    OR EXISTS(SELECT 1 FROM note_cache WHERE id LIKE ?)
                """,
                arguments: [Self.demoIdentifierPrefix + "%", Self.demoIdentifierPrefix + "%"]
            ) ?? false
        }
    }

    static let demoIdentifierPrefix = "01989f00-0000-7000-8000-"

    func incrementAttempts(id: String) async throws {
        try await pool.write { database in
            try database.execute(sql: "UPDATE pending_mutations SET attempts = attempts + 1 WHERE id = ?", arguments: [id])
        }
    }

    func reminder(id: String) async throws -> Reminder? {
        try await pool.read { database in
            guard let data: Data = try Data.fetchOne(database, sql: "SELECT json FROM reminder_cache WHERE id = ?", arguments: [id]) else {
                return nil
            }
            return try StateJSON.decoder.decode(Reminder.self, from: data)
        }
    }

    func reminders(includeArchived: Bool = false) async throws -> [Reminder] {
        try await pool.read { database in
            let sql = includeArchived
                ? "SELECT json FROM reminder_cache ORDER BY scheduled_at IS NULL, scheduled_at, updated_at DESC"
                : "SELECT json FROM reminder_cache WHERE archived = 0 ORDER BY scheduled_at IS NULL, scheduled_at, updated_at DESC"
            return try Data.fetchAll(database, sql: sql).map { try StateJSON.decoder.decode(Reminder.self, from: $0) }
        }
    }

    func detail(id: String) async throws -> ReminderDetail? {
        try await pool.read { database in
            guard let reminderData: Data = try Data.fetchOne(database, sql: "SELECT json FROM reminder_cache WHERE id = ?", arguments: [id]) else {
                return nil
            }
            let reminder = try StateJSON.decoder.decode(Reminder.self, from: reminderData)
            let comments = try Data.fetchAll(database, sql: "SELECT json FROM comment_cache WHERE reminder_id = ? ORDER BY created_at", arguments: [id])
                .map { try StateJSON.decoder.decode(Comment.self, from: $0) }
            let occurrences = try Data.fetchAll(database, sql: "SELECT json FROM occurrence_cache WHERE reminder_id = ? ORDER BY scheduled_at", arguments: [id])
                .map { try StateJSON.decoder.decode(Occurrence.self, from: $0) }
            let history = try Data.fetchAll(database, sql: "SELECT json FROM audit_cache WHERE reminder_id = ? ORDER BY server_time", arguments: [id])
                .map { try StateJSON.decoder.decode(AuditEvent.self, from: $0) }
            let runs = try Data.fetchAll(database, sql: "SELECT json FROM run_cache WHERE reminder_id = ? ORDER BY updated_at DESC", arguments: [id])
                .map { try StateJSON.decoder.decode(AgentRun.self, from: $0) }
            return ReminderDetail(reminder: reminder, comments: comments, occurrences: occurrences, history: history, runs: runs)
        }
    }

    func activity(limit: Int = 200) async throws -> [AuditEvent] {
        try await pool.read { database in
            try Data.fetchAll(database, sql: "SELECT json FROM audit_cache ORDER BY server_time DESC LIMIT ?", arguments: [limit])
                .map { try StateJSON.decoder.decode(AuditEvent.self, from: $0) }
        }
    }

    func pendingOccurrences(limit: Int = 64) async throws -> [Occurrence] {
        try await pool.read { database in
            try Data.fetchAll(
                database,
                sql: "SELECT json FROM occurrence_cache WHERE status IN ('pending', 'snoozed') AND scheduled_at IS NOT NULL ORDER BY scheduled_at LIMIT ?",
                arguments: [limit]
            ).map { try StateJSON.decoder.decode(Occurrence.self, from: $0) }
        }
    }

    /// Per reminder: its earliest open occurrence and whether it has any.
    func occurrenceSummaries() async throws -> [String: OccurrenceSummary] {
        let occurrences = try await pool.read { database in
            try Data.fetchAll(database, sql: "SELECT json FROM occurrence_cache")
                .map { try StateJSON.decoder.decode(Occurrence.self, from: $0) }
        }
        var summaries: [String: OccurrenceSummary] = [:]
        for occurrence in occurrences {
            var summary = summaries[occurrence.reminderID] ?? OccurrenceSummary(next: nil, hasAny: false)
            summary.hasAny = true
            if occurrence.status != .completed {
                let key = (occurrence.localDate, occurrence.localTime ?? "")
                if let current = summary.next {
                    if key < (current.localDate, current.localTime ?? "") { summary.next = occurrence }
                } else {
                    summary.next = occurrence
                }
            }
            summaries[occurrence.reminderID] = summary
        }
        return summaries
    }

    func occurrence(id: String) async throws -> Occurrence? {
        try await pool.read { database in
            guard let data: Data = try Data.fetchOne(
                database,
                sql: "SELECT json FROM occurrence_cache WHERE id = ?",
                arguments: [id]
            ) else {
                return nil
            }
            return try StateJSON.decoder.decode(Occurrence.self, from: data)
        }
    }

    func runs(for reminderID: String) async throws -> [AgentRun] {
        try await pool.read { database in
            try Data.fetchAll(database, sql: "SELECT json FROM run_cache WHERE reminder_id = ? ORDER BY updated_at DESC", arguments: [reminderID])
                .map { try StateJSON.decoder.decode(AgentRun.self, from: $0) }
        }
    }

    func run(id: String) async throws -> AgentRun? {
        try await pool.read { database in
            guard let data: Data = try Data.fetchOne(database, sql: "SELECT json FROM run_cache WHERE id = ?", arguments: [id]) else {
                return nil
            }
            return try StateJSON.decoder.decode(AgentRun.self, from: data)
        }
    }

    // MARK: Notes

    /// Newest change first, the order of the Notes list.
    func notes(includeArchived: Bool = false) async throws -> [Note] {
        try await pool.read { database in
            let sql = includeArchived
                ? "SELECT json FROM note_cache ORDER BY updated_at DESC, id DESC"
                : "SELECT json FROM note_cache WHERE archived = 0 ORDER BY updated_at DESC, id DESC"
            return try Data.fetchAll(database, sql: sql).map { try StateJSON.decoder.decode(Note.self, from: $0) }
        }
    }

    func archivedNotes() async throws -> [Note] {
        try await pool.read { database in
            try Data.fetchAll(database, sql: "SELECT json FROM note_cache WHERE archived = 1 ORDER BY updated_at DESC, id DESC")
                .map { try StateJSON.decoder.decode(Note.self, from: $0) }
        }
    }

    func note(id: String) async throws -> Note? {
        try await noteRecord(id: id)?.note
    }

    /// Follows the alias a provisional identifier left behind, so a screen
    /// that still holds it finds the note under its server identifier.
    func noteRecord(id: String) async throws -> NoteRecord? {
        try await pool.read { database in try Self.noteRecord(id: id, in: database) }
    }

    func noteAliases() async throws -> [String: String] {
        try await pool.read { database in
            Dictionary(uniqueKeysWithValues: try Row.fetchAll(database, sql: "SELECT provisional_id, server_id FROM note_alias")
                .map { (row: Row) -> (String, String) in (row["provisional_id"], row["server_id"]) })
        }
    }

    func noteSyncErrors() async throws -> [String: String] {
        try await pool.read { database in
            Dictionary(uniqueKeysWithValues: try Row.fetchAll(database, sql: "SELECT id, sync_error FROM note_cache WHERE sync_error IS NOT NULL")
                .map { (row: Row) -> (String, String) in (row["id"], row["sync_error"]) })
        }
    }

    func dirtyNoteRecords() async throws -> [NoteRecord] {
        try await pool.read { database in
            try Row.fetchAll(database, sql: "SELECT * FROM note_cache WHERE dirty = 1 AND sync_error IS NULL ORDER BY updated_at, id")
                .map(Self.noteRecord(row:))
        }
    }

    /// A note that exists only on this device so far.
    func insertLocalNote(_ note: Note) async throws {
        try await pool.write { database in
            try Self.write(
                NoteRecord(note: note, dirty: true, localVersion: 1, base: nil,
                           createRequestID: UUIDv7.generate().uuidString.lowercased(), patchRequestID: nil, syncError: nil),
                in: database
            )
        }
    }

    /// The single writer for local edits: reads the current note inside the
    /// transaction, so quick successive edits, such as ticking several
    /// checklist items, never overwrite each other.
    @discardableResult
    func editNote(id: String, change: @escaping @Sendable (inout Note) -> Void) async throws -> Note? {
        try await pool.write { database in
            guard var record = try Self.noteRecord(id: id, in: database) else { return nil }
            var edited = record.note
            change(&edited)
            guard edited != record.note else { return record.note }
            edited.updatedAt = Date()
            record.note = edited
            record.dirty = true
            record.localVersion += 1
            record.syncError = nil
            try Self.write(record, in: database)
            return edited
        }
    }

    /// Applies a note from the server. A note with unsent local edits keeps
    /// them; the next push meets the new revision and resolves it.
    func applyServer(note: Note) async throws {
        try await pool.write { database in
            if try Self.noteRecord(id: note.id, in: database) == nil,
               try Self.adoptAsAnsweredCreate(note, in: database) {
                return
            }
            if var existing = try Self.noteRecord(id: note.id, in: database), existing.dirty {
                // Keep it for the push: if the local edit turns out to be
                // nothing (ticked and unticked), the note moves to this version.
                if note.revision > (existing.latest?.revision ?? existing.base?.revision ?? 0) {
                    existing.latest = note
                    try Self.write(existing, in: database)
                }
                return
            }
            try Self.write(
                NoteRecord(note: note, dirty: false, localVersion: 0, base: note,
                           createRequestID: nil, patchRequestID: nil, syncError: nil),
                in: database
            )
        }
    }

    /// Stores the request a push is about to send, before it is sent.
    /// Returns the request that is stored now: the given one, or the one that
    /// was already waiting, which must then be replayed instead.
    func beginNotePush(id: String, inflight: NoteInflight) async throws -> NoteInflight? {
        try await pool.write { database in
            guard var record = try Self.noteRecord(id: id, in: database) else { return nil }
            if let existing = record.inflight { return existing }
            record.inflight = inflight
            try Self.write(record, in: database)
            return inflight
        }
    }

    /// Records the server's answer to a push. Edits made while the request
    /// was in flight stay dirty and go out with the next push.
    func completeNotePush(localID: String, sentVersion: Int64, server answered: Note) async throws {
        try await pool.write { database in
            guard let record = try Self.noteRecord(id: localID, in: database) else { return }
            try Self.completePush(record, sentVersion: sentVersion, server: answered, in: database)
        }
    }

    private static func completePush(_ original: NoteRecord, sentVersion: Int64, server answered: Note, in database: Database) throws {
        var record = original
        // A replayed request is answered from the server's idempotency
        // record, which can be older than a version the pull already saw.
        let server: Note
        if let latest = record.latest, latest.id == answered.id, latest.revision > answered.revision {
            server = latest
        } else {
            server = answered
        }
        if record.note.id != server.id {
            try database.execute(sql: "DELETE FROM note_cache WHERE id = ?", arguments: [record.note.id])
            try database.execute(
                sql: "INSERT OR REPLACE INTO note_alias (provisional_id, server_id) VALUES (?, ?)",
                arguments: [record.note.id, server.id]
            )
        }
        let conflictOf = record.conflictOf
        if record.localVersion == sentVersion {
            record = NoteRecord(note: server, dirty: false, localVersion: record.localVersion, base: server,
                                createRequestID: nil, patchRequestID: nil, syncError: nil, inflight: nil, latest: nil,
                                conflictOf: conflictOf)
        } else {
            let pending = record.note
            record = NoteRecord(
                note: Note(id: server.id, title: pending.title, titleSource: pending.titleSource,
                           document: pending.document, plainText: pending.plainText, summary: pending.summary,
                           summarySource: pending.summarySource, archived: pending.archived,
                           revision: server.revision, createdAt: server.createdAt, updatedAt: pending.updatedAt),
                dirty: true, localVersion: record.localVersion, base: server,
                createRequestID: nil, patchRequestID: nil, syncError: nil, inflight: nil, latest: nil,
                conflictOf: conflictOf
            )
        }
        try write(record, in: database)
    }

    /// A push met a newer server version. The server version becomes the
    /// note. When the local document was edited and differs from the server's,
    /// it is kept as a new note, the conflict copy, built here from the text
    /// stored now, including anything typed while the push was in flight.
    /// Title and archive intents are reapplied on top of the server version.
    func resolveNoteConflict(localID: String, server: Note, copyTitle: @escaping @Sendable (String) -> String) async throws {
        try await pool.write { database in
            guard let record = try Self.noteRecord(id: localID, in: database) else { return }
            let base = record.base
            let editedDocument = record.note.document != base?.document
            if editedDocument, record.note.document != server.document {
                let copy = Note.local(
                    id: UUIDv7.generate().uuidString.lowercased(),
                    title: copyTitle(record.note.title),
                    document: record.note.document,
                    at: Date()
                )
                try Self.write(
                    NoteRecord(note: copy, dirty: true, localVersion: 1, base: nil,
                               createRequestID: UUIDv7.generate().uuidString.lowercased(), patchRequestID: nil, syncError: nil,
                               conflictOf: record.note.id),
                    in: database
                )
            }
            var merged = server
            if let base, record.note.archived != base.archived {
                merged.archived = record.note.archived
            }
            if let base, record.note.title != base.title || record.note.titleSource != base.titleSource {
                merged.apply(title: record.note.titleSource == Note.userSource ? record.note.title : "", document: merged.document)
            }
            let stillDirty = merged != server
            try Self.write(
                NoteRecord(note: merged, dirty: stillDirty, localVersion: record.localVersion + 1, base: server,
                           createRequestID: nil, patchRequestID: nil, syncError: nil, inflight: nil, latest: nil,
                           conflictOf: record.conflictOf),
                in: database
            )
        }
    }

    /// The conflict copy the sync made from this note that still holds the
    /// given text, so an open editor keeps writing into it instead of making
    /// a second copy.
    func conflictCopy(of noteID: String, holding document: String) async throws -> Note? {
        try await pool.read { database in
            let resolved = try String.fetchOne(database, sql: "SELECT server_id FROM note_alias WHERE provisional_id = ?", arguments: [noteID]) ?? noteID
            return try Row.fetchAll(database, sql: "SELECT * FROM note_cache WHERE conflict_of = ? ORDER BY updated_at DESC", arguments: [resolved])
                .map(Self.noteRecord(row:))
                .first { $0.note.document == document }?.note
        }
    }

    /// A push the server rejected for good (invalid input, gone). The note
    /// keeps its text and waits for the next edit instead of blocking sync.
    func markNoteSyncError(id: String, message: String) async throws {
        try await pool.write { database in
            // A rejected create may still have been stored by the server. Its
            // request ID must never carry a different body later, so a later
            // create uses a new one.
            try database.execute(
                sql: """
                UPDATE note_cache SET sync_error = ?, inflight_method = NULL, inflight_path = NULL,
                    inflight_body = NULL, inflight_version = NULL,
                    create_request_id = CASE WHEN base_json IS NULL THEN ? ELSE create_request_id END
                WHERE id = ?
                """,
                arguments: [message, UUIDv7.generate().uuidString.lowercased(), id]
            )
        }
    }

    /// Removes a note the server never saw, such as one archived offline.
    func deleteLocalNote(id: String) async throws {
        try await pool.write { database in
            try database.execute(sql: "DELETE FROM note_cache WHERE id = ?", arguments: [id])
        }
    }

    /// A create whose response was lost comes back through the pull under its
    /// server ID. It belongs to the local note waiting on that create, not
    /// next to it. The local row takes the server ID, as if the response had
    /// arrived; the replay of the create is then no longer needed.
    private static func adoptAsAnsweredCreate(_ note: Note, in database: Database) throws -> Bool {
        let waiting = try Row.fetchAll(database, sql: "SELECT * FROM note_cache WHERE inflight_method = 'POST'")
            .map(noteRecord(row:))
        for record in waiting {
            guard let inflight = record.inflight,
                  let body = try? JSONSerialization.jsonObject(with: inflight.body) as? [String: Any],
                  body["document"] as? String == note.document else { continue }
            let sentTitle = body["title"] as? String
            guard sentTitle == nil ? note.titleSource == Note.derivedSource : note.title == sentTitle else { continue }
            try completePush(record, sentVersion: inflight.version, server: note, in: database)
            return true
        }
        return false
    }

    private static func noteRecord(id: String, in database: Database) throws -> NoteRecord? {
        let resolved = try String.fetchOne(database, sql: "SELECT server_id FROM note_alias WHERE provisional_id = ?", arguments: [id]) ?? id
        return try Row.fetchOne(database, sql: "SELECT * FROM note_cache WHERE id = ?", arguments: [resolved]).map(noteRecord(row:))
    }

    private static func noteRecord(row: Row) throws -> NoteRecord {
        let baseData: Data? = row["base_json"]
        return NoteRecord(
            note: try StateJSON.decoder.decode(Note.self, from: row["json"] as Data),
            dirty: row["dirty"],
            localVersion: row["local_version"],
            base: try baseData.map { try StateJSON.decoder.decode(Note.self, from: $0) },
            createRequestID: row["create_request_id"],
            patchRequestID: row["patch_request_id"],
            syncError: row["sync_error"],
            inflight: {
                guard let method: String = row["inflight_method"], let path: String = row["inflight_path"],
                      let body: Data = row["inflight_body"], let version: Int64 = row["inflight_version"] else { return nil }
                return NoteInflight(method: method, path: path, body: body, version: version)
            }(),
            latest: try (row["latest_json"] as Data?).map { try StateJSON.decoder.decode(Note.self, from: $0) },
            conflictOf: row["conflict_of"]
        )
    }

    private static func write(_ record: NoteRecord, in database: Database) throws {
        let json = try StateJSON.encoder.encode(record.note)
        let base = try record.base.map { try StateJSON.encoder.encode($0) }
        let latest = try record.latest.map { try StateJSON.encoder.encode($0) }
        try database.execute(
            sql: """
            INSERT INTO note_cache (id, title, archived, updated_at, json, dirty, local_version, base_json,
                                    create_request_id, patch_request_id, sync_error,
                                    inflight_method, inflight_path, inflight_body, inflight_version, latest_json, conflict_of)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(id) DO UPDATE SET
                title = excluded.title, archived = excluded.archived, updated_at = excluded.updated_at,
                json = excluded.json, dirty = excluded.dirty, local_version = excluded.local_version,
                base_json = excluded.base_json, create_request_id = excluded.create_request_id,
                patch_request_id = excluded.patch_request_id, sync_error = excluded.sync_error,
                inflight_method = excluded.inflight_method, inflight_path = excluded.inflight_path,
                inflight_body = excluded.inflight_body, inflight_version = excluded.inflight_version,
                latest_json = excluded.latest_json, conflict_of = excluded.conflict_of
            """,
            arguments: [
                record.note.id, record.note.title, record.note.archived, record.note.updatedAt, json,
                record.dirty, record.localVersion, base, record.createRequestID, record.patchRequestID, record.syncError,
                record.inflight?.method, record.inflight?.path, record.inflight?.body, record.inflight?.version, latest,
                record.conflictOf,
            ]
        )
    }

    func projects() async throws -> [Project] {
        try await pool.read { database in
            try Data.fetchAll(database, sql: "SELECT json FROM project_cache ORDER BY name")
                .map { try StateJSON.decoder.decode(Project.self, from: $0) }
        }
    }

    func policies() async throws -> [ExecutionPolicy] {
        try await pool.read { database in
            try Data.fetchAll(database, sql: "SELECT json FROM policy_cache ORDER BY name")
                .map { try StateJSON.decoder.decode(ExecutionPolicy.self, from: $0) }
        }
    }

    func runners() async throws -> [Runner] {
        try await pool.read { database in
            try Data.fetchAll(database, sql: "SELECT json FROM runner_cache ORDER BY display_name")
                .map { try StateJSON.decoder.decode(Runner.self, from: $0) }
        }
    }

    /// Replaces the project cache with the server's global list.
    func apply(projects: [Project]) async throws {
        let encoded = try projects.map { ($0, try StateJSON.encoder.encode($0)) }
        try await pool.write { database in
            try database.execute(sql: "DELETE FROM project_cache")
            for (project, json) in encoded {
                try database.execute(
                    sql: "INSERT INTO project_cache (id, name, json) VALUES (?, ?, ?)",
                    arguments: [project.id, project.name, json]
                )
            }
        }
    }

    /// Replaces the policy cache with the server's global list.
    func apply(policies: [ExecutionPolicy]) async throws {
        let encoded = try policies.map { ($0, try StateJSON.encoder.encode($0)) }
        try await pool.write { database in
            try database.execute(sql: "DELETE FROM policy_cache")
            for (policy, json) in encoded {
                try database.execute(
                    sql: "INSERT INTO policy_cache (id, name, enabled, json) VALUES (?, ?, ?, ?)",
                    arguments: [policy.id, policy.name, policy.enabled, json]
                )
            }
        }
    }

    /// Replaces the runner cache with the server's global list.
    func apply(runners: [Runner]) async throws {
        let encoded = try runners.map { ($0, try StateJSON.encoder.encode($0)) }
        try await pool.write { database in
            try database.execute(sql: "DELETE FROM runner_cache")
            for (runner, json) in encoded {
                try database.execute(
                    sql: "INSERT INTO runner_cache (id, display_name, json) VALUES (?, ?, ?)",
                    arguments: [runner.id, runner.displayName, json]
                )
            }
        }
    }

    /// Advances the sync cursor without a reminder detail, for change pages
    /// that carry only policy/project/runner events (no reminder ID).
    func advanceCursor(to cursor: Int64) async throws {
        try await pool.write { database in
            try database.execute(
                sql: "INSERT INTO metadata (key, value) VALUES ('cursor', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
                arguments: [String(cursor)]
            )
        }
    }

    func cursor() async throws -> Int64 {
        try await pool.read { database in
            let value: String? = try String.fetchOne(database, sql: "SELECT value FROM metadata WHERE key = 'cursor'")
            return Int64(value ?? "0") ?? 0
        }
    }

    func recordConflict(entityID: String, server: Data, local: Data, fields: [String]) async throws {
        let id = UUIDv7.generate().uuidString.lowercased()
        let encodedFields = try StateJSON.encoder.encode(fields)
        try await pool.write { database in
            try database.execute(
                sql: "INSERT INTO conflicts (id, entity_id, server_snapshot, local_snapshot, fields, created_at, resolved) VALUES (?, ?, ?, ?, ?, ?, 0)",
                arguments: [id, entityID, server, local, encodedFields, Date()]
            )
        }
    }

    func conflicts() async throws -> [StoredConflict] {
        try await pool.read { database in
            try Row.fetchAll(database, sql: "SELECT * FROM conflicts ORDER BY created_at DESC").map { row in
                let fieldsData: Data = row["fields"]
                return StoredConflict(
                    id: row["id"],
                    entityID: row["entity_id"],
                    serverSnapshot: row["server_snapshot"],
                    localSnapshot: row["local_snapshot"],
                    fields: try StateJSON.decoder.decode([String].self, from: fieldsData),
                    createdAt: row["created_at"],
                    resolved: row["resolved"]
                )
            }
        }
    }

    func resolveConflict(id: String) async throws {
        try await pool.write { database in
            try database.execute(sql: "UPDATE conflicts SET resolved = 1 WHERE id = ?", arguments: [id])
        }
    }

    private static func pendingMutation(row: Row) -> PendingMutation {
        PendingMutation(
            id: row["id"],
            method: row["method"],
            path: row["path"],
            body: row["body"],
            entityID: row["entity_id"],
            createdAt: row["created_at"],
            attempts: row["attempts"]
        )
    }

    private var migrator: DatabaseMigrator {
        var migrator = DatabaseMigrator()
        migrator.registerMigration("v1") { database in
            try database.create(table: "reminder_cache") { table in
                table.column("id", .text).primaryKey()
                table.column("title", .text).notNull()
                table.column("status", .text).notNull()
                table.column("revision", .integer).notNull()
                table.column("archived", .boolean).notNull()
                table.column("scheduled_at", .datetime)
                table.column("updated_at", .datetime).notNull()
                table.column("json", .blob).notNull()
            }
            try database.create(index: "reminder_schedule_idx", on: "reminder_cache", columns: ["archived", "scheduled_at"])
            try database.create(table: "comment_cache") { table in
                table.column("id", .text).primaryKey()
                table.column("reminder_id", .text).notNull().indexed().references("reminder_cache", onDelete: .cascade)
                table.column("created_at", .datetime).notNull()
                table.column("json", .blob).notNull()
            }
            try database.create(table: "occurrence_cache") { table in
                table.column("id", .text).primaryKey()
                table.column("reminder_id", .text).notNull().indexed().references("reminder_cache", onDelete: .cascade)
                table.column("scheduled_at", .datetime)
                table.column("status", .text).notNull()
                table.column("json", .blob).notNull()
            }
            try database.create(table: "audit_cache") { table in
                table.column("id", .text).primaryKey()
                table.column("reminder_id", .text).notNull().indexed().references("reminder_cache", onDelete: .cascade)
                table.column("server_time", .datetime).notNull().indexed()
                table.column("action", .text).notNull()
                table.column("json", .blob).notNull()
            }
            try database.create(table: "metadata") { table in
                table.column("key", .text).primaryKey()
                table.column("value", .text).notNull()
            }
            try database.create(table: "pending_mutations") { table in
                table.column("id", .text).primaryKey()
                table.column("method", .text).notNull()
                table.column("path", .text).notNull()
                table.column("body", .blob).notNull()
                table.column("entity_id", .text)
                table.column("created_at", .datetime).notNull().indexed()
                table.column("attempts", .integer).notNull().defaults(to: 0)
            }
            try database.create(table: "conflicts") { table in
                table.column("id", .text).primaryKey()
                table.column("entity_id", .text).notNull().indexed()
                table.column("server_snapshot", .blob).notNull()
                table.column("local_snapshot", .blob).notNull()
                table.column("fields", .blob).notNull()
                table.column("created_at", .datetime).notNull()
                table.column("resolved", .boolean).notNull().defaults(to: false)
            }
        }
        migrator.registerMigration("v2") { database in
            try database.create(table: "project_cache") { table in
                table.column("id", .text).primaryKey()
                table.column("name", .text).notNull()
                table.column("json", .blob).notNull()
            }
            try database.create(table: "policy_cache") { table in
                table.column("id", .text).primaryKey()
                table.column("name", .text).notNull()
                table.column("enabled", .boolean).notNull()
                table.column("json", .blob).notNull()
            }
            try database.create(table: "runner_cache") { table in
                table.column("id", .text).primaryKey()
                table.column("display_name", .text).notNull()
                table.column("json", .blob).notNull()
            }
            try database.create(table: "run_cache") { table in
                table.column("id", .text).primaryKey()
                table.column("reminder_id", .text).notNull().indexed().references("reminder_cache", onDelete: .cascade)
                table.column("status", .text).notNull().indexed()
                table.column("updated_at", .datetime).notNull()
                table.column("json", .blob).notNull()
            }
        }
        migrator.registerMigration("v3-notes") { database in
            try database.create(table: "note_cache") { table in
                table.column("id", .text).primaryKey()
                table.column("title", .text).notNull()
                table.column("archived", .boolean).notNull()
                table.column("updated_at", .datetime).notNull()
                table.column("json", .blob).notNull()
            }
            try database.create(index: "note_updated_idx", on: "note_cache", columns: ["archived", "updated_at"])
        }
        migrator.registerMigration("v4-note-sync") { database in
            try database.alter(table: "note_cache") { table in
                table.add(column: "dirty", .boolean).notNull().defaults(to: false)
                table.add(column: "local_version", .integer).notNull().defaults(to: 0)
                table.add(column: "base_json", .blob)
                table.add(column: "create_request_id", .text)
                table.add(column: "patch_request_id", .text)
                table.add(column: "sync_error", .text)
            }
            try database.create(table: "note_alias") { table in
                table.column("provisional_id", .text).primaryKey()
                table.column("server_id", .text).notNull()
            }
        }
        migrator.registerMigration("v5-note-inflight") { database in
            try database.alter(table: "note_cache") { table in
                table.add(column: "inflight_method", .text)
                table.add(column: "inflight_path", .text)
                table.add(column: "inflight_body", .blob)
                table.add(column: "inflight_version", .integer)
                table.add(column: "latest_json", .blob)
            }
        }
        migrator.registerMigration("v6-note-conflict-origin") { database in
            try database.alter(table: "note_cache") { table in
                table.add(column: "conflict_of", .text)
            }
        }
        return migrator
    }
}
