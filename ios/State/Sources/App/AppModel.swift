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
    var executionPolicyID: String?
}

/// Editable fields of an execution policy. The project is fixed at creation.
struct PolicyDraft: Sendable {
    var name = ""
    var projectID = ""
    var adapter = "claude-code"
    var mode: ExecutionMode = .supervised
    var allowedCapabilities: [String] = []
    var markOccurrenceDoneOnSuccess = true
    var notifyOnStart = false
    var notifyOnCompletion = true
    var notifyOnFailure = true
    var timeoutMinutes = 30
    var enabled = true
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
    private(set) var projects: [Project] = []
    private(set) var policies: [ExecutionPolicy] = []
    private(set) var runners: [Runner] = []
    private(set) var isSyncing = false
    private(set) var lastSyncAt: Date?
    private(set) var isDemo = false
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
            isDemo = false
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
            await syncExecutionState()
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

    /// Fetches the global project/policy/runner lists after the change pull.
    /// Best-effort: a failure keeps the cached lists and stays silent, so a
    /// transient error never masks a successful reminder sync.
    private func syncExecutionState() async {
        guard let api else { return }
        do {
            async let fetchedProjects = api.listProjects()
            async let fetchedPolicies = api.listPolicies()
            async let fetchedRunners = api.listRunners()
            let (projects, policies, runners) = try await (fetchedProjects, fetchedPolicies, fetchedRunners)
            try await database.apply(projects: projects)
            try await database.apply(policies: policies)
            try await database.apply(runners: runners)
        } catch {
            // Cached lists stay in place until the next successful sync.
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
                executionPolicyID: draft.executionPolicyID,
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
            if isDemo {
                await reloadCache()
                return
            }
            let requestID = UUIDv7.generate().uuidString.lowercased()
            let request = CreateReminderRequest(
                title: reminder.title,
                description: reminder.description,
                schedule: reminder.schedule,
                recurrence: reminder.recurrence,
                executionPolicyID: reminder.executionPolicyID,
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
        recurrence: RecurrenceRule? = nil,
        executionPolicyID: String?
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
                    executionPolicyID: executionPolicyID,
                    revision: expectedRevision + 1,
                    archived: detail.reminder.archived,
                    createdAt: detail.reminder.createdAt,
                    updatedAt: Date()
                ),
                comments: detail.comments,
                occurrences: detail.occurrences,
                history: detail.history,
                runs: detail.runs
            )
            try await database.apply(detail: detail, cursor: nil)
            if isDemo {
                await reloadCache()
                return
            }
            let requestID = UUIDv7.generate().uuidString.lowercased()
            let request = UpdateReminderRequest(
                title: title,
                description: description,
                schedule: schedule,
                recurrence: recurrence,
                executionPolicyID: executionPolicyID,
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
            recurrence: draft.recurrence.map { RecurrenceRule(frequency: $0, interval: 1, untilDate: nil) },
            executionPolicyID: draft.executionPolicyID
        )
    }

    /// Attaches or detaches the reminder's execution policy through the
    /// regular update path; nil detaches and is encoded as an explicit null,
    /// exactly like the schedule and recurrence fields.
    func setExecutionPolicy(_ policyID: String?, on reminder: Reminder) async {
        await updateReminder(
            reminder,
            title: reminder.title,
            description: reminder.description ?? "",
            schedule: reminder.schedule,
            recurrence: reminder.recurrence,
            executionPolicyID: policyID
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
                history: detail.history,
                runs: detail.runs
            )
            try await database.apply(detail: detail, cursor: nil)
            if isDemo {
                await reloadCache()
                return
            }
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
                    executionPolicyID: detail.reminder.executionPolicyID,
                    revision: expectedRevision + 1,
                    archived: archived,
                    createdAt: detail.reminder.createdAt,
                    updatedAt: Date()
                ),
                comments: detail.comments,
                occurrences: detail.occurrences,
                history: detail.history,
                runs: detail.runs
            )
            try await database.apply(detail: detail, cursor: nil)
            if isDemo {
                await reloadCache()
                return
            }
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
                    executionPolicyID: local.executionPolicyID,
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

    func createPolicy(_ draft: PolicyDraft) async {
        do {
            let now = Date()
            let provisionalID = UUIDv7.generate().uuidString.lowercased()
            let policy = ExecutionPolicy(
                id: provisionalID,
                name: draft.name.trimmingCharacters(in: .whitespacesAndNewlines),
                projectID: draft.projectID,
                adapter: draft.adapter,
                mode: draft.mode,
                allowedCapabilities: draft.allowedCapabilities,
                markOccurrenceDoneOnSuccess: draft.markOccurrenceDoneOnSuccess,
                notifyOnStart: draft.notifyOnStart,
                notifyOnCompletion: draft.notifyOnCompletion,
                notifyOnFailure: draft.notifyOnFailure,
                timeoutMinutes: draft.timeoutMinutes,
                enabled: draft.enabled,
                revision: 1,
                createdAt: now,
                updatedAt: now
            )
            try await database.apply(policies: policies + [policy])
            if isDemo {
                await reloadCache()
                return
            }
            let requestID = UUIDv7.generate().uuidString.lowercased()
            let request = CreatePolicyRequest(
                name: policy.name,
                projectID: policy.projectID,
                adapter: policy.adapter,
                mode: policy.mode,
                allowedCapabilities: policy.allowedCapabilities,
                markOccurrenceDoneOnSuccess: policy.markOccurrenceDoneOnSuccess,
                notifyOnStart: policy.notifyOnStart,
                notifyOnCompletion: policy.notifyOnCompletion,
                notifyOnFailure: policy.notifyOnFailure,
                timeoutMinutes: policy.timeoutMinutes,
                clientTime: now,
                source: "ios",
                clientRequestID: requestID,
                correlationID: requestID
            )
            // The server assigns the real ID; the next sync's replace-all
            // policy fetch retires the provisional row.
            _ = try await database.enqueue(
                method: "POST",
                path: "/api/v1/policies",
                body: StateJSON.encoder.encode(request),
                entityID: provisionalID
            )
            await reloadCache()
            await synchronize()
        } catch {
            presentedError = error.localizedDescription
        }
    }

    func updatePolicy(_ policy: ExecutionPolicy, draft: PolicyDraft) async {
        do {
            let expectedRevision = policy.revision
            let updated = ExecutionPolicy(
                id: policy.id,
                name: draft.name.trimmingCharacters(in: .whitespacesAndNewlines),
                projectID: policy.projectID,
                adapter: draft.adapter,
                mode: draft.mode,
                allowedCapabilities: draft.allowedCapabilities,
                markOccurrenceDoneOnSuccess: draft.markOccurrenceDoneOnSuccess,
                notifyOnStart: draft.notifyOnStart,
                notifyOnCompletion: draft.notifyOnCompletion,
                notifyOnFailure: draft.notifyOnFailure,
                timeoutMinutes: draft.timeoutMinutes,
                enabled: draft.enabled,
                revision: expectedRevision + 1,
                createdAt: policy.createdAt,
                updatedAt: Date()
            )
            try await database.apply(policies: policies.map { $0.id == policy.id ? updated : $0 })
            if isDemo {
                await reloadCache()
                return
            }
            let requestID = UUIDv7.generate().uuidString.lowercased()
            let request = UpdatePolicyRequest(
                name: updated.name,
                adapter: updated.adapter,
                mode: updated.mode,
                allowedCapabilities: updated.allowedCapabilities,
                markOccurrenceDoneOnSuccess: updated.markOccurrenceDoneOnSuccess,
                notifyOnStart: updated.notifyOnStart,
                notifyOnCompletion: updated.notifyOnCompletion,
                notifyOnFailure: updated.notifyOnFailure,
                timeoutMinutes: updated.timeoutMinutes,
                enabled: updated.enabled,
                expectedRevision: expectedRevision,
                clientTime: Date(),
                source: "ios",
                clientRequestID: requestID,
                correlationID: requestID
            )
            _ = try await database.enqueue(
                method: "PATCH",
                path: "/api/v1/policies/\(policy.id)",
                body: StateJSON.encoder.encode(request),
                entityID: policy.id
            )
            await reloadCache()
            await synchronize()
        } catch {
            presentedError = error.localizedDescription
        }
    }

    // Run mutations are direct API calls rather than queued offline
    // mutations: a run only makes sense against the live server, and the
    // response plus the following sync bring the cache up to date.
    func triggerManualRun(reminderID: String, policyID: String) async {
        guard let api, !isDemo else { return }
        do {
            _ = try await api.createManualRun(reminderID: reminderID, policyID: policyID)
            await synchronize()
        } catch {
            presentedError = error.localizedDescription
        }
    }

    func approveRun(_ run: AgentRun, approved: Bool) async {
        guard let api, !isDemo else { return }
        do {
            _ = try await api.approveRun(id: run.id, approved: approved, expectedRevision: run.revision)
            await synchronize()
        } catch {
            presentedError = error.localizedDescription
        }
    }

    func cancelRun(_ run: AgentRun) async {
        guard let api, !isDemo else { return }
        do {
            _ = try await api.cancelRun(id: run.id, expectedRevision: run.revision)
            await synchronize()
        } catch {
            presentedError = error.localizedDescription
        }
    }

    func enterDemo() async {
        do {
            isDemo = true
            session = ServerSession(
                serverURL: URL(string: "https://demo.state.invalid")!,
                actor: Actor(
                    id: "01989f00-0000-7000-8000-000000000001",
                    kind: .owner,
                    displayName: "Fabian",
                    harness: nil,
                    deviceName: "iPhone"
                )
            )
            api = nil
            syncEngine = nil
            try await seedDemoData()
            await reloadCache()
        } catch {
            presentedError = error.localizedDescription
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
        isDemo = false
        self.session = session
        self.api = api
        syncEngine = SyncEngine(database: database, api: api)
    }

    private func reloadCache() async {
        do {
            let loadedReminders = try await database.reminders()
            let loadedActivity = try await database.activity()
            projects = try await database.projects()
            policies = try await database.policies()
            runners = try await database.runners()
            if isDemo {
                reminders = loadedReminders.filter { Self.demoReminderIDs.contains($0.id) }
                activity = loadedActivity.filter { $0.reminderID.map(Self.demoReminderIDs.contains) ?? false }
                conflicts = []
            } else {
                reminders = loadedReminders
                activity = loadedActivity
                conflicts = try await database.conflicts().filter { !$0.resolved }
            }
        } catch {
            presentedError = error.localizedDescription
        }
    }

    private func seedDemoData() async throws {
        let calendar = Calendar(identifier: .gregorian)
        let now = Date()
        let today = now.formatted(.iso8601.year().month().day())
        let nextWeek = calendar.date(byAdding: .day, value: 6, to: now) ?? now
        let nextWeekText = nextWeek.formatted(.iso8601.year().month().day())
        let claude = Actor(
            id: "01989f00-0000-7000-8000-000000000002",
            kind: .harness,
            displayName: "Claude Code",
            harness: "claude-code",
            deviceName: "MacBook Pro"
        )
        let fabian = Actor(
            id: "01989f00-0000-7000-8000-000000000001",
            kind: .owner,
            displayName: "Fabian",
            harness: nil,
            deviceName: "iPhone"
        )
        let first = Reminder(
            id: Self.demoReminderIDs[0],
            title: String(localized: "Prepare agent workflow review"),
            description: String(localized: "Check the MCP result and add the final notes."),
            status: .active,
            schedule: Schedule(
                localDate: today,
                localTime: "21:00",
                timeZone: TimeZone.current.identifier,
                mode: .floating,
                prewarningMinutes: 10
            ),
            recurrence: nil,
            executionPolicyID: Self.demoPolicyID,
            revision: 2,
            archived: false,
            createdAt: now.addingTimeInterval(-7_200),
            updatedAt: now.addingTimeInterval(-1_800)
        )
        let second = Reminder(
            id: Self.demoReminderIDs[1],
            title: String(localized: "Renew VPS backup test"),
            description: String(localized: "Verify the encrypted archive and restore it into an empty volume."),
            status: .active,
            schedule: Schedule(
                localDate: nextWeekText,
                localTime: "09:00",
                timeZone: TimeZone.current.identifier,
                mode: .fixed,
                prewarningMinutes: 60
            ),
            recurrence: RecurrenceRule(frequency: .monthly, interval: 1, untilDate: nil),
            executionPolicyID: nil,
            revision: 1,
            archived: false,
            createdAt: now.addingTimeInterval(-86_400),
            updatedAt: now.addingTimeInterval(-86_400)
        )
        let firstOccurrence = demoOccurrence(reminder: first, id: "01989f00-0000-7000-8000-000000000020", now: now)
        let secondOccurrence = demoOccurrence(reminder: second, id: "01989f00-0000-7000-8000-000000000021", now: now)
        let created = demoEvent(
            id: "01989f00-0000-7000-8000-000000000030",
            reminder: first,
            action: "reminder.created",
            actor: claude,
            serverTime: now.addingTimeInterval(-7_200),
            sourceExcerpt: String(localized: "Remind me to prepare the agent workflow review")
        )
        let changed = demoEvent(
            id: "01989f00-0000-7000-8000-000000000031",
            reminder: first,
            action: "reminder.updated",
            actor: fabian,
            serverTime: now.addingTimeInterval(-1_800),
            sourceExcerpt: nil
        )
        let secondCreated = demoEvent(
            id: "01989f00-0000-7000-8000-000000000032",
            reminder: second,
            action: "reminder.created",
            actor: Actor(
                id: "01989f00-0000-7000-8000-000000000003",
                kind: .harness,
                displayName: "Codex",
                harness: "codex",
                deviceName: "MacBook Pro"
            ),
            serverTime: now.addingTimeInterval(-86_400),
            sourceExcerpt: String(localized: "Remind me to renew the backup restore test")
        )
        let project = Project(
            id: "01989f00-0000-7000-8000-000000000050",
            name: "customer-api",
            description: String(localized: "Customer API service"),
            rootPathHint: "~/src/customer-api",
            revision: 1,
            createdAt: now.addingTimeInterval(-86_400),
            updatedAt: now.addingTimeInterval(-86_400)
        )
        let policy = ExecutionPolicy(
            id: Self.demoPolicyID,
            name: "nightly-maintenance",
            projectID: project.id,
            adapter: "claude-code",
            mode: .supervised,
            allowedCapabilities: ["read_repository", "run_tests", "edit_repository"],
            markOccurrenceDoneOnSuccess: true,
            notifyOnStart: false,
            notifyOnCompletion: true,
            notifyOnFailure: true,
            timeoutMinutes: 30,
            enabled: true,
            revision: 1,
            createdAt: now.addingTimeInterval(-86_400),
            updatedAt: now.addingTimeInterval(-86_400)
        )
        let runner = Runner(
            id: "01989f00-0000-7000-8000-000000000052",
            displayName: "Mac mini",
            projects: [project.id],
            adapters: ["claude-code", "codex"],
            registeredAt: now.addingTimeInterval(-86_400),
            lastSeenAt: now.addingTimeInterval(-300),
            revision: 1
        )
        let runnerActor = Actor(
            id: runner.id,
            kind: .runner,
            displayName: runner.displayName,
            harness: nil,
            deviceName: nil
        )
        let succeededRun = AgentRun(
            id: "01989f00-0000-7000-8000-000000000060",
            reminderID: first.id,
            occurrenceID: firstOccurrence.id,
            policyID: policy.id,
            policyRevision: 1,
            projectID: project.id,
            adapter: policy.adapter,
            runnerID: runner.id,
            status: .succeeded,
            idempotencyKey: "01989f00-0000-7000-8000-000000000060",
            taskContract: demoContract(runID: "01989f00-0000-7000-8000-000000000060", policy: policy, project: project, objective: first.title),
            contextCursor: 3,
            leaseExpiresAt: nil,
            requestedAt: now.addingTimeInterval(-3_600),
            claimedAt: now.addingTimeInterval(-3_590),
            startedAt: now.addingTimeInterval(-3_580),
            finishedAt: now.addingTimeInterval(-3_300),
            resultSummary: String(localized: "Notes added and the test suite is green."),
            resultArtifactRef: nil,
            failureCode: nil,
            approvalCapability: nil,
            createdByActor: Actor(id: "system", kind: .system, displayName: "State", harness: nil, deviceName: nil),
            completedByActor: runnerActor,
            revision: 4,
            createdAt: now.addingTimeInterval(-3_600),
            updatedAt: now.addingTimeInterval(-3_300)
        )
        let approvalRun = AgentRun(
            id: "01989f00-0000-7000-8000-000000000061",
            reminderID: first.id,
            occurrenceID: nil,
            policyID: policy.id,
            policyRevision: 1,
            projectID: project.id,
            adapter: policy.adapter,
            runnerID: runner.id,
            status: .needsApproval,
            idempotencyKey: "01989f00-0000-7000-8000-000000000061",
            taskContract: demoContract(runID: "01989f00-0000-7000-8000-000000000061", policy: policy, project: project, objective: first.title),
            contextCursor: 4,
            leaseExpiresAt: nil,
            requestedAt: now.addingTimeInterval(-900),
            claimedAt: now.addingTimeInterval(-890),
            startedAt: now.addingTimeInterval(-880),
            finishedAt: nil,
            resultSummary: nil,
            resultArtifactRef: nil,
            failureCode: nil,
            approvalCapability: "deploy",
            createdByActor: fabian,
            completedByActor: nil,
            revision: 3,
            createdAt: now.addingTimeInterval(-900),
            updatedAt: now.addingTimeInterval(-600)
        )
        try await database.apply(projects: [project])
        try await database.apply(policies: [policy])
        try await database.apply(runners: [runner])
        try await database.apply(
            detail: ReminderDetail(
                reminder: first,
                comments: [
                    Comment(
                        id: "01989f00-0000-7000-8000-000000000040",
                        reminderID: first.id,
                        body: String(localized: "Focus on the complete cross-agent history."),
                        actor: fabian,
                        revision: 1,
                        createdAt: now.addingTimeInterval(-1_200),
                        updatedAt: now.addingTimeInterval(-1_200)
                    ),
                ],
                occurrences: [firstOccurrence],
                history: [created, changed],
                runs: [approvalRun, succeededRun]
            ),
            cursor: nil
        )
        try await database.apply(
            detail: ReminderDetail(
                reminder: second,
                comments: [],
                occurrences: [secondOccurrence],
                history: [secondCreated]
            ),
            cursor: nil
        )
    }

    private func demoOccurrence(reminder: Reminder, id: String, now: Date) -> Occurrence {        let schedule = reminder.schedule!
        return Occurrence(
            id: id,
            reminderID: reminder.id,
            localDate: schedule.localDate,
            localTime: schedule.localTime,
            timeZone: schedule.timeZone,
            timeZoneMode: schedule.mode,
            prewarningMinutes: schedule.prewarningMinutes,
            scheduledAt: Self.date(schedule: schedule),
            status: .pending,
            completedAt: nil,
            snoozedUntil: nil,
            revision: 1,
            createdAt: reminder.createdAt,
            updatedAt: now
        )
    }

    private func demoEvent(
        id: String,
        reminder: Reminder,
        action: String,
        actor: Actor,
        serverTime: Date,
        sourceExcerpt: String?
    ) -> AuditEvent {
        AuditEvent(
            id: id,
            reminderID: reminder.id,
            action: action,
            actor: actor,
            serverTime: serverTime,
            clientTime: serverTime,
            source: actor.kind == .harness ? "mcp" : "ios",
            sourceExcerpt: sourceExcerpt,
            changedFields: action == "reminder.created" ? ["title", "description", "schedule"] : ["schedule"],
            revision: reminder.revision,
            correlationID: id,
            clientRequestID: id,
            previousHash: nil,
            hash: "demo",
            signature: "demo"
        )
    }

    private func demoContract(runID: String, policy: ExecutionPolicy, project: Project, objective: String) -> TaskContract {
        TaskContract(
            runID: runID,
            correlationID: runID,
            objective: objective,
            acceptanceCriteria: [],
            projectID: project.id,
            projectName: project.name,
            policyID: policy.id,
            policyRevision: policy.revision,
            contractHash: "demo",
            allowedCapabilities: policy.allowedCapabilities,
            timeoutMinutes: policy.timeoutMinutes,
            completionRule: "mark_occurrence_done_on_success"
        )
    }

    private static let demoReminderIDs = [
        "01989f00-0000-7000-8000-000000000010",
        "01989f00-0000-7000-8000-000000000011",
    ]

    private static let demoPolicyID = "01989f00-0000-7000-8000-000000000051"

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
                history: detail.history,
                runs: detail.runs
            )
            try await database.apply(detail: detail, cursor: nil)
            if isDemo {
                await reloadCache()
                return
            }
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
    let executionPolicyID: String?
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
    let executionPolicyID: String?
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
        case executionPolicyID
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
        if let executionPolicyID {
            try container.encode(executionPolicyID, forKey: .executionPolicyID)
        } else {
            try container.encodeNil(forKey: .executionPolicyID)
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

private struct CreatePolicyRequest: Codable {
    let name: String
    let projectID: String
    let adapter: String
    let mode: ExecutionMode
    let allowedCapabilities: [String]
    let markOccurrenceDoneOnSuccess: Bool
    let notifyOnStart: Bool
    let notifyOnCompletion: Bool
    let notifyOnFailure: Bool
    let timeoutMinutes: Int
    let clientTime: Date
    let source: String
    let clientRequestID: String
    let correlationID: String
}

private struct UpdatePolicyRequest: Codable {
    let name: String
    let adapter: String
    let mode: ExecutionMode
    let allowedCapabilities: [String]
    let markOccurrenceDoneOnSuccess: Bool
    let notifyOnStart: Bool
    let notifyOnCompletion: Bool
    let notifyOnFailure: Bool
    let timeoutMinutes: Int
    let enabled: Bool
    let expectedRevision: Int64
    let clientTime: Date
    let source: String
    let clientRequestID: String
    let correlationID: String
}
