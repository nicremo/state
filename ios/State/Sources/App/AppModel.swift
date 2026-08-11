import Foundation
import Observation

struct ReminderDraft: Sendable {
    var title = ""
    var description = ""
    var scheduled = false
    var date = Date()
    var includesTime = true
    var prewarningMinutes = 0
    var recurrence: RecurrenceFrequency?
    var timeZoneMode: TimeZoneMode = .floating
    var timeZoneIdentifier = TimeZone.current.identifier
}

@MainActor
@Observable
final class AppModel {
    private let database: StateDatabase
    private let sessionRepository: SessionRepository
    private var api: APIClient?
    private var syncEngine: SyncEngine?

    private(set) var session: ServerSession?
    private(set) var reminders: [Reminder] = []
    private(set) var activity: [AuditEvent] = []
    private(set) var conflicts: [StoredConflict] = []
    private(set) var agents: [Actor] = []
    private(set) var devices: [Actor] = []
    private(set) var isSyncing = false
    private(set) var lastSyncAt: Date?
    var presentedError: String?

    init(database: StateDatabase, sessionRepository: SessionRepository = SessionRepository()) {
        self.database = database
        self.sessionRepository = sessionRepository
        do {
            if let (session, token) = try sessionRepository.load() {
                try configure(session: session, token: token)
            }
        } catch {
            presentedError = error.localizedDescription
        }
    }

    func start() async {
        await reloadCache()
        await synchronize()
    }

    func connect(
        serverURL: URL,
        bootstrapToken: String?,
        pairingCode: String?,
        displayName: String,
        deviceName: String
    ) async {
        do {
            let credential: Credential
            if let bootstrapToken, !bootstrapToken.isEmpty {
                credential = try await APIClient.bootstrapOwner(
                    serverURL: serverURL,
                    bootstrapToken: bootstrapToken,
                    displayName: displayName,
                    deviceName: deviceName
                )
            } else if let pairingCode, !pairingCode.isEmpty {
                credential = try await APIClient.exchangePairingCode(serverURL: serverURL, code: pairingCode)
            } else {
                throw StateAPIError.invalidResponse
            }
            let newSession = ServerSession(serverURL: serverURL, actor: credential.actor)
            try sessionRepository.save(session: newSession, token: credential.token)
            try configure(session: newSession, token: credential.token)
            await synchronize()
        } catch {
            presentedError = error.localizedDescription
        }
    }

    func disconnect() {
        do {
            try sessionRepository.clear()
            session = nil
            api = nil
            syncEngine = nil
        } catch {
            presentedError = error.localizedDescription
        }
    }

    func synchronize() async {
        guard let syncEngine, !isSyncing else { return }
        isSyncing = true
        defer { isSyncing = false }
        do {
            try await syncEngine.sync()
            lastSyncAt = Date()
            await reloadCache()
            await reloadActors()
        } catch is CancellationError {
        } catch {
            await reloadCache()
            if !(error is URLError) {
                presentedError = error.localizedDescription
            }
        }
    }

    func createReminder(_ draft: ReminderDraft) async {
        guard let actor = session?.actor else { return }
        do {
            let now = Date()
            let provisionalID = UUIDv7.generate().uuidString.lowercased()
            let schedule = draft.scheduled ? makeSchedule(from: draft) : nil
            let reminder = Reminder(
                id: provisionalID,
                title: draft.title.trimmingCharacters(in: .whitespacesAndNewlines),
                description: draft.description.isEmpty ? nil : draft.description,
                status: .active,
                schedule: schedule,
                recurrence: draft.recurrence.map { RecurrenceRule(frequency: $0, interval: 1, untilDate: nil) },
                revision: 1,
                archived: false,
                createdAt: now,
                updatedAt: now
            )
            let occurrence = schedule.flatMap { makeOccurrence(reminderID: provisionalID, schedule: $0, now: now) }
            try await database.apply(
                detail: ReminderDetail(reminder: reminder, comments: [], occurrences: occurrence.map { [$0] } ?? [], history: []),
                cursor: nil
            )
            let requestID = UUIDv7.generate().uuidString.lowercased()
            let request = CreateReminderRequest(
                title: reminder.title,
                description: reminder.description,
                schedule: reminder.schedule,
                recurrence: reminder.recurrence,
                clientTime: now,
                source: "ios",
                sourceExcerpt: nil,
                clientRequestID: requestID,
                correlationID: requestID
            )
            _ = try await database.enqueue(
                method: "POST",
                path: "/api/v1/reminders",
                body: StateJSON.encoder.encode(request),
                entityID: provisionalID
            )
            _ = actor
            await reloadCache()
            await synchronize()
        } catch {
            presentedError = error.localizedDescription
        }
    }

    func updateReminder(
        _ reminder: Reminder,
        title: String,
        description: String,
        schedule: Schedule?,
        recurrence: RecurrenceRule? = nil
    ) async {
        do {
            guard var detail = try await database.detail(id: reminder.id) else { return }
            let expectedRevision = detail.reminder.revision
            detail = ReminderDetail(
                reminder: Reminder(
                    id: detail.reminder.id,
                    title: title,
                    description: description.isEmpty ? nil : description,
                    status: detail.reminder.status,
                    schedule: schedule,
                    recurrence: recurrence,
                    revision: expectedRevision + 1,
                    archived: detail.reminder.archived,
                    createdAt: detail.reminder.createdAt,
                    updatedAt: Date()
                ),
                comments: detail.comments,
                occurrences: detail.occurrences,
                history: detail.history
            )
            try await database.apply(detail: detail, cursor: nil)
            let requestID = UUIDv7.generate().uuidString.lowercased()
            let request = UpdateReminderRequest(
                title: title,
                description: description,
                schedule: schedule,
                recurrence: recurrence,
                expectedRevision: expectedRevision,
                clientTime: Date(),
                source: "ios",
                clientRequestID: requestID,
                correlationID: requestID
            )
            _ = try await database.enqueue(
                method: "PATCH",
                path: "/api/v1/reminders/\(reminder.id)",
                body: StateJSON.encoder.encode(request),
                entityID: reminder.id
            )
            await reloadCache()
            await synchronize()
        } catch {
            presentedError = error.localizedDescription
        }
    }

    func updateReminder(_ reminder: Reminder, draft: ReminderDraft) async {
        await updateReminder(
            reminder,
            title: draft.title.trimmingCharacters(in: .whitespacesAndNewlines),
            description: draft.description,
            schedule: draft.scheduled ? makeSchedule(from: draft) : nil,
            recurrence: draft.recurrence.map { RecurrenceRule(frequency: $0, interval: 1, untilDate: nil) }
        )
    }

    func addComment(reminderID: String, body: String) async {
        guard let actor = session?.actor else { return }
        do {
            guard var detail = try await database.detail(id: reminderID) else { return }
            let now = Date()
            let requestID = UUIDv7.generate().uuidString.lowercased()
            let comment = Comment(
                id: requestID,
                reminderID: reminderID,
                body: body,
                actor: actor,
                revision: 1,
                createdAt: now,
                updatedAt: now
            )
            detail = ReminderDetail(
                reminder: detail.reminder,
                comments: detail.comments + [comment],
                occurrences: detail.occurrences,
                history: detail.history
            )
            try await database.apply(detail: detail, cursor: nil)
            let request = AddCommentRequest(
                body: body,
                clientTime: now,
                source: "ios",
                clientRequestID: requestID,
                correlationID: requestID
            )
            _ = try await database.enqueue(
                method: "POST",
                path: "/api/v1/reminders/\(reminderID)/comments",
                body: StateJSON.encoder.encode(request),
                entityID: reminderID
            )
            await reloadCache()
            await synchronize()
        } catch {
            presentedError = error.localizedDescription
        }
    }

    func archiveReminder(_ reminder: Reminder, archived: Bool) async {
        do {
            guard var detail = try await database.detail(id: reminder.id) else { return }
            let expectedRevision = detail.reminder.revision
            detail = ReminderDetail(
                reminder: Reminder(
                    id: detail.reminder.id,
                    title: detail.reminder.title,
                    description: detail.reminder.description,
                    status: detail.reminder.status,
                    schedule: detail.reminder.schedule,
                    recurrence: detail.reminder.recurrence,
                    revision: expectedRevision + 1,
                    archived: archived,
                    createdAt: detail.reminder.createdAt,
                    updatedAt: Date()
                ),
                comments: detail.comments,
                occurrences: detail.occurrences,
                history: detail.history
            )
            try await database.apply(detail: detail, cursor: nil)
            let requestID = UUIDv7.generate().uuidString.lowercased()
            let request = ArchiveReminderRequest(
                archived: archived,
                expectedRevision: expectedRevision,
                clientTime: Date(),
                source: "ios",
                clientRequestID: requestID,
                correlationID: requestID
            )
            _ = try await database.enqueue(
                method: "PATCH",
                path: "/api/v1/reminders/\(reminder.id)",
                body: StateJSON.encoder.encode(request),
                entityID: reminder.id
            )
            await reloadCache()
            await synchronize()
        } catch {
            presentedError = error.localizedDescription
        }
    }

    func completeOccurrence(id: String) async {
        await mutateOccurrence(id: id, snoozeUntil: nil)
    }

    func snoozeOccurrence(id: String, until: Date) async {
        await mutateOccurrence(id: id, snoozeUntil: until)
    }

    func notificationItems(limit: Int = 64) async -> [(Reminder, Occurrence)] {
        do {
            let occurrences = try await database.pendingOccurrences(limit: limit)
            var result: [(Reminder, Occurrence)] = []
            for occurrence in occurrences {
                if let reminder = try await database.reminder(id: occurrence.reminderID) {
                    result.append((reminder, occurrence))
                }
            }
            return result
        } catch {
            presentedError = error.localizedDescription
            return []
        }
    }

    func confirmNotifications(_ occurrenceIDs: [String]) async {
        guard let api, !occurrenceIDs.isEmpty else { return }
        do {
            try await api.confirmOccurrences(occurrenceIDs)
        } catch {
            if !(error is URLError) {
                presentedError = error.localizedDescription
            }
        }
    }

    func registerPushRoute(
        relayURL: URL,
        routeID: String,
        authorization: String,
        publicKey: Data
    ) async throws {
        guard let api else { throw StateAPIError.unauthorized }
        _ = try await api.registerPushRoute(
            relayURL: relayURL,
            routeID: routeID,
            authorization: authorization,
            publicKey: publicKey
        )
    }

    func reminderDetail(id: String) async -> ReminderDetail? {
        try? await database.detail(id: id)
    }

    func resolveConflict(_ conflict: StoredConflict, keepLocal: Bool) async {
        do {
            if keepLocal {
                let local = try StateJSON.decoder.decode(Reminder.self, from: conflict.localSnapshot)
                let server = try StateJSON.decoder.decode(Reminder.self, from: conflict.serverSnapshot)
                let requestID = UUIDv7.generate().uuidString.lowercased()
                let request = UpdateReminderRequest(
                    title: local.title,
                    description: local.description,
                    schedule: local.schedule,
                    recurrence: local.recurrence,
                    expectedRevision: server.revision,
                    clientTime: Date(),
                    source: "ios-conflict-resolution",
                    clientRequestID: requestID,
                    correlationID: requestID
                )
                _ = try await database.enqueue(
                    method: "PATCH",
                    path: "/api/v1/reminders/\(local.id)",
                    body: StateJSON.encoder.encode(request),
                    entityID: local.id
                )
            } else if let api {
                let detail = try await api.getReminder(id: conflict.entityID)
                try await database.apply(detail: detail, cursor: nil)
            }
            try await database.resolveConflict(id: conflict.id)
            await synchronize()
        } catch {
            presentedError = error.localizedDescription
        }
    }

    func createPairingCode(harness: String, displayName: String, deviceName: String) async -> PairingCode? {
        guard let api else { return nil }
        do {
            return try await api.createPairingCode(kind: .harness, harness: harness, displayName: displayName, deviceName: deviceName)
        } catch {
            presentedError = error.localizedDescription
            return nil
        }
    }

    func revokeActor(_ actor: Actor) async {
        guard let api else { return }
        do {
            try await api.revokeActor(id: actor.id, kind: actor.kind)
            await reloadActors()
        } catch {
            presentedError = error.localizedDescription
        }
    }

    private func configure(session: ServerSession, token: String) throws {
        let api = try APIClient(serverURL: session.serverURL, token: token)
        self.session = session
        self.api = api
        syncEngine = SyncEngine(database: database, api: api)
    }

    private func reloadCache() async {
        do {
            reminders = try await database.reminders()
            activity = try await database.activity()
            conflicts = try await database.conflicts().filter { !$0.resolved }
        } catch {
            presentedError = error.localizedDescription
        }
    }

    private func reloadActors() async {
        guard let api, session?.actor.kind == .owner else { return }
        do {
            async let loadedAgents = api.listActors(kind: .harness)
            async let loadedDevices = api.listActors(kind: .device)
            agents = try await loadedAgents
            devices = try await loadedDevices
        } catch StateAPIError.unauthorized {
        } catch {
            presentedError = error.localizedDescription
        }
    }

    private func mutateOccurrence(id: String, snoozeUntil: Date?) async {
        do {
            guard
                let occurrence = try await database.occurrence(id: id),
                var detail = try await database.detail(id: occurrence.reminderID)
            else {
                return
            }
            let now = Date()
            let updated = Occurrence(
                id: occurrence.id,
                reminderID: occurrence.reminderID,
                localDate: occurrence.localDate,
                localTime: occurrence.localTime,
                timeZone: occurrence.timeZone,
                timeZoneMode: occurrence.timeZoneMode,
                prewarningMinutes: occurrence.prewarningMinutes,
                scheduledAt: occurrence.scheduledAt,
                status: snoozeUntil == nil ? .completed : .snoozed,
                completedAt: snoozeUntil == nil ? now : nil,
                snoozedUntil: snoozeUntil,
                revision: occurrence.revision + 1,
                createdAt: occurrence.createdAt,
                updatedAt: now
            )
            detail = ReminderDetail(
                reminder: detail.reminder,
                comments: detail.comments,
                occurrences: detail.occurrences.map { $0.id == id ? updated : $0 },
                history: detail.history
            )
            try await database.apply(detail: detail, cursor: nil)
            let requestID = UUIDv7.generate().uuidString.lowercased()
            let body: Data
            let path: String
            if let snoozeUntil {
                body = try StateJSON.encoder.encode(
                    SnoozeOccurrenceRequest(
                        until: snoozeUntil,
                        expectedRevision: occurrence.revision,
                        clientTime: now,
                        source: "ios",
                        clientRequestID: requestID,
                        correlationID: requestID
                    )
                )
                path = "/api/v1/occurrences/\(id)/snooze"
            } else {
                body = try StateJSON.encoder.encode(
                    CompleteOccurrenceRequest(
                        expectedRevision: occurrence.revision,
                        clientTime: now,
                        source: "ios",
                        clientRequestID: requestID,
                        correlationID: requestID
                    )
                )
                path = "/api/v1/occurrences/\(id)/complete"
            }
            _ = try await database.enqueue(method: "POST", path: path, body: body, entityID: occurrence.reminderID)
            await reloadCache()
            await synchronize()
        } catch {
            presentedError = error.localizedDescription
        }
    }

    private func makeSchedule(from draft: ReminderDraft) -> Schedule {
        let zone = TimeZone(identifier: draft.timeZoneIdentifier) ?? .current
        let calendar = Calendar(identifier: .gregorian)
        let dateComponents = calendar.dateComponents(in: zone, from: draft.date)
        let localDate = String(format: "%04d-%02d-%02d", dateComponents.year ?? 0, dateComponents.month ?? 0, dateComponents.day ?? 0)
        let localTime = draft.includesTime
            ? String(format: "%02d:%02d", dateComponents.hour ?? 0, dateComponents.minute ?? 0)
            : nil
        return Schedule(
            localDate: localDate,
            localTime: localTime,
            timeZone: zone.identifier,
            mode: draft.timeZoneMode,
            prewarningMinutes: draft.prewarningMinutes
        )
    }

    private func makeOccurrence(reminderID: String, schedule: Schedule, now: Date) -> Occurrence? {
        let scheduledAt = Self.date(schedule: schedule)
        return Occurrence(
            id: UUIDv7.generate().uuidString.lowercased(),
            reminderID: reminderID,
            localDate: schedule.localDate,
            localTime: schedule.localTime,
            timeZone: schedule.timeZone,
            timeZoneMode: schedule.mode,
            prewarningMinutes: schedule.prewarningMinutes,
            scheduledAt: scheduledAt,
            status: .pending,
            completedAt: nil,
            snoozedUntil: nil,
            revision: 1,
            createdAt: now,
            updatedAt: now
        )
    }

    private static func date(schedule: Schedule) -> Date? {
        guard let localTime = schedule.localTime, let zone = TimeZone(identifier: schedule.timeZone) else { return nil }
        let dateValues = schedule.localDate.split(separator: "-").compactMap { Int($0) }
        let timeValues = localTime.split(separator: ":").compactMap { Int($0) }
        guard dateValues.count == 3, timeValues.count == 2 else { return nil }
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = zone
        return calendar.date(
            from: DateComponents(
                year: dateValues[0],
                month: dateValues[1],
                day: dateValues[2],
                hour: timeValues[0],
                minute: timeValues[1]
            )
        )
    }
}

private struct CreateReminderRequest: Codable {
    let title: String
    let description: String?
    let schedule: Schedule?
    let recurrence: RecurrenceRule?
    let clientTime: Date
    let source: String
    let sourceExcerpt: String?
    let clientRequestID: String
    let correlationID: String
}

private struct UpdateReminderRequest: Codable {
    let title: String
    let description: String?
    let schedule: Schedule?
    let recurrence: RecurrenceRule?
    let expectedRevision: Int64
    let clientTime: Date
    let source: String
    let clientRequestID: String
    let correlationID: String

    private enum CodingKeys: String, CodingKey {
        case title
        case description
        case schedule
        case recurrence
        case expectedRevision
        case clientTime
        case source
        case clientRequestID
        case correlationID
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(title, forKey: .title)
        try container.encodeIfPresent(description, forKey: .description)
        if let schedule {
            try container.encode(schedule, forKey: .schedule)
        } else {
            try container.encodeNil(forKey: .schedule)
        }
        if let recurrence {
            try container.encode(recurrence, forKey: .recurrence)
        } else {
            try container.encodeNil(forKey: .recurrence)
        }
        try container.encode(expectedRevision, forKey: .expectedRevision)
        try container.encode(clientTime, forKey: .clientTime)
        try container.encode(source, forKey: .source)
        try container.encode(clientRequestID, forKey: .clientRequestID)
        try container.encode(correlationID, forKey: .correlationID)
    }
}

private struct AddCommentRequest: Codable {
    let body: String
    let clientTime: Date
    let source: String
    let clientRequestID: String
    let correlationID: String
}

private struct ArchiveReminderRequest: Codable {
    let archived: Bool
    let expectedRevision: Int64
    let clientTime: Date
    let source: String
    let clientRequestID: String
    let correlationID: String
}

private struct CompleteOccurrenceRequest: Codable {
    let expectedRevision: Int64
    let clientTime: Date
    let source: String
    let clientRequestID: String
    let correlationID: String
}

private struct SnoozeOccurrenceRequest: Codable {
    let until: Date
    let expectedRevision: Int64
    let clientTime: Date
    let source: String
    let clientRequestID: String
    let correlationID: String
}
