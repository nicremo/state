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
        let baseURL = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: "group.com.fabincrm.state")
            ?? FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
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
        return migrator
    }
}
