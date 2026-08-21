import Foundation

enum ActorKind: String, Codable, Sendable {
    case owner
    case device
    case harness
    case system
    case runner
}

struct Actor: Codable, Hashable, Sendable {
    let id: String
    let kind: ActorKind
    let displayName: String?
    let harness: String?
    let deviceName: String?
}

enum ReminderStatus: String, Codable, Sendable {
    case active
    case completed
}

enum TimeZoneMode: String, Codable, Sendable {
    case floating
    case fixed
}

struct Schedule: Codable, Hashable, Sendable {
    var localDate: String
    var localTime: String?
    var timeZone: String
    var mode: TimeZoneMode
    var prewarningMinutes: Int?
}

enum RecurrenceFrequency: String, Codable, Sendable {
    case daily
    case weekly
    case monthly
    case yearly
}

struct RecurrenceRule: Codable, Hashable, Sendable {
    var frequency: RecurrenceFrequency
    var interval: Int
    var untilDate: String?
}

struct Reminder: Codable, Hashable, Identifiable, Sendable {
    let id: String
    var title: String
    var description: String?
    var status: ReminderStatus
    var schedule: Schedule?
    var recurrence: RecurrenceRule?
    var executionPolicyID: String?
    var revision: Int64
    var archived: Bool
    var createdAt: Date
    var updatedAt: Date

    static func fixture(
        id: String,
        revision: Int64,
        title: String = "Reminder"
    ) -> Reminder {
        Reminder(
            id: id,
            title: title,
            description: nil,
            status: .active,
            schedule: nil,
            recurrence: nil,
            executionPolicyID: nil,
            revision: revision,
            archived: false,
            createdAt: Date(timeIntervalSince1970: 1_786_000_000),
            updatedAt: Date(timeIntervalSince1970: 1_786_000_000)
        )
    }
}

struct Comment: Codable, Hashable, Identifiable, Sendable {
    let id: String
    let reminderID: String
    var body: String
    let actor: Actor
    let revision: Int64
    let createdAt: Date
    let updatedAt: Date
}

enum OccurrenceStatus: String, Codable, Sendable {
    case pending
    case completed
    case snoozed
}

struct Occurrence: Codable, Hashable, Identifiable, Sendable {
    let id: String
    let reminderID: String
    let localDate: String
    let localTime: String?
    let timeZone: String
    let timeZoneMode: TimeZoneMode
    let prewarningMinutes: Int?
    let scheduledAt: Date?
    var status: OccurrenceStatus
    var completedAt: Date?
    var snoozedUntil: Date?
    var revision: Int64
    let createdAt: Date
    var updatedAt: Date
}

struct AuditEvent: Codable, Hashable, Identifiable, Sendable {
    let id: String
    /// Nil for policy, project and runner events, which carry no reminder. The
    /// server stores NULL in the audit column and an empty string inside the
    /// event JSON, so both decode as nil here.
    let reminderID: String?
    let action: String
    let actor: Actor
    let serverTime: Date
    let clientTime: Date?
    let source: String?
    let sourceExcerpt: String?
    let changedFields: [String]
    let revision: Int64
    let correlationID: String
    let clientRequestID: String
    let previousHash: String?
    let hash: String
    let signature: String

    private enum CodingKeys: String, CodingKey {
        case id
        case reminderID
        case action
        case actor
        case serverTime
        case clientTime
        case source
        case sourceExcerpt
        case changedFields
        case revision
        case correlationID
        case clientRequestID
        case previousHash
        case hash
        case signature
    }

    init(
        id: String,
        reminderID: String?,
        action: String,
        actor: Actor,
        serverTime: Date,
        clientTime: Date?,
        source: String?,
        sourceExcerpt: String?,
        changedFields: [String],
        revision: Int64,
        correlationID: String,
        clientRequestID: String,
        previousHash: String?,
        hash: String,
        signature: String
    ) {
        self.id = id
        self.reminderID = reminderID
        self.action = action
        self.actor = actor
        self.serverTime = serverTime
        self.clientTime = clientTime
        self.source = source
        self.sourceExcerpt = sourceExcerpt
        self.changedFields = changedFields
        self.revision = revision
        self.correlationID = correlationID
        self.clientRequestID = clientRequestID
        self.previousHash = previousHash
        self.hash = hash
        self.signature = signature
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        let rawReminderID = try container.decodeIfPresent(String.self, forKey: .reminderID)
        reminderID = rawReminderID?.isEmpty == true ? nil : rawReminderID
        action = try container.decode(String.self, forKey: .action)
        actor = try container.decode(Actor.self, forKey: .actor)
        serverTime = try container.decode(Date.self, forKey: .serverTime)
        clientTime = try container.decodeIfPresent(Date.self, forKey: .clientTime)
        source = try container.decodeIfPresent(String.self, forKey: .source)
        sourceExcerpt = try container.decodeIfPresent(String.self, forKey: .sourceExcerpt)
        changedFields = try container.decodeIfPresent([String].self, forKey: .changedFields) ?? []
        revision = try container.decode(Int64.self, forKey: .revision)
        correlationID = try container.decode(String.self, forKey: .correlationID)
        clientRequestID = try container.decode(String.self, forKey: .clientRequestID)
        previousHash = try container.decodeIfPresent(String.self, forKey: .previousHash)
        hash = try container.decode(String.self, forKey: .hash)
        signature = try container.decode(String.self, forKey: .signature)
    }
}

struct ReminderDetail: Codable, Sendable {
    let reminder: Reminder
    let comments: [Comment]
    let occurrences: [Occurrence]
    let history: [AuditEvent]
    var runs: [AgentRun] = []
}

struct Change: Codable, Sendable {
    let cursor: Int64
    let event: AuditEvent
}

struct ChangesResponse: Codable, Sendable {
    let changes: [Change]
    let cursor: Int64
}

struct ReminderListResponse: Codable, Sendable {
    let reminders: [Reminder]
}

struct Credential: Codable, Sendable {
    let actor: Actor
    let token: String
}

struct PairingCode: Codable, Sendable {
    let code: String
    let expiresAt: Date
}

struct DeviceRoute: Codable, Sendable {
    let actorID: String
    let relayURL: String
    let routeID: String
    let publicKey: Data
    let createdAt: Date
    let updatedAt: Date
}

struct ActorRecord: Codable, Sendable {
    let actor: Actor
    let createdAt: Date
}

struct ActorListResponse: Codable, Sendable {
    let actors: [ActorRecord]
}

struct Project: Codable, Hashable, Identifiable, Sendable {
    let id: String
    var name: String
    var description: String?
    var rootPathHint: String?
    var revision: Int64
    let createdAt: Date
    var updatedAt: Date
}

struct ProjectListResponse: Codable, Sendable {
    let projects: [Project]
}

enum ExecutionMode: String, Codable, Sendable {
    case supervised
    case unattendedLowRisk = "unattended-low-risk"
}

struct ExecutionPolicy: Codable, Hashable, Identifiable, Sendable {
    let id: String
    var name: String
    var projectID: String
    var adapter: String
    var mode: ExecutionMode
    var allowedCapabilities: [String]
    var markOccurrenceDoneOnSuccess: Bool
    var notifyOnStart: Bool
    var notifyOnCompletion: Bool
    var notifyOnFailure: Bool
    var timeoutMinutes: Int
    var enabled: Bool
    var revision: Int64
    let createdAt: Date
    var updatedAt: Date

    private enum CodingKeys: String, CodingKey {
        case id
        case name
        case projectID
        case adapter
        case mode
        case allowedCapabilities
        case markOccurrenceDoneOnSuccess
        case notifyOnStart
        case notifyOnCompletion
        case notifyOnFailure
        case timeoutMinutes
        case enabled
        case revision
        case createdAt
        case updatedAt
    }

    init(
        id: String,
        name: String,
        projectID: String,
        adapter: String,
        mode: ExecutionMode,
        allowedCapabilities: [String],
        markOccurrenceDoneOnSuccess: Bool,
        notifyOnStart: Bool,
        notifyOnCompletion: Bool,
        notifyOnFailure: Bool,
        timeoutMinutes: Int,
        enabled: Bool,
        revision: Int64,
        createdAt: Date,
        updatedAt: Date
    ) {
        self.id = id
        self.name = name
        self.projectID = projectID
        self.adapter = adapter
        self.mode = mode
        self.allowedCapabilities = allowedCapabilities
        self.markOccurrenceDoneOnSuccess = markOccurrenceDoneOnSuccess
        self.notifyOnStart = notifyOnStart
        self.notifyOnCompletion = notifyOnCompletion
        self.notifyOnFailure = notifyOnFailure
        self.timeoutMinutes = timeoutMinutes
        self.enabled = enabled
        self.revision = revision
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        name = try container.decode(String.self, forKey: .name)
        projectID = try container.decode(String.self, forKey: .projectID)
        adapter = try container.decode(String.self, forKey: .adapter)
        mode = try container.decode(ExecutionMode.self, forKey: .mode)
        // The server omits or nulls empty capability lists.
        allowedCapabilities = try container.decodeIfPresent([String].self, forKey: .allowedCapabilities) ?? []
        markOccurrenceDoneOnSuccess = try container.decode(Bool.self, forKey: .markOccurrenceDoneOnSuccess)
        notifyOnStart = try container.decode(Bool.self, forKey: .notifyOnStart)
        notifyOnCompletion = try container.decode(Bool.self, forKey: .notifyOnCompletion)
        notifyOnFailure = try container.decode(Bool.self, forKey: .notifyOnFailure)
        timeoutMinutes = try container.decode(Int.self, forKey: .timeoutMinutes)
        enabled = try container.decode(Bool.self, forKey: .enabled)
        revision = try container.decode(Int64.self, forKey: .revision)
        createdAt = try container.decode(Date.self, forKey: .createdAt)
        updatedAt = try container.decode(Date.self, forKey: .updatedAt)
    }
}

struct PolicyListResponse: Codable, Sendable {
    let policies: [ExecutionPolicy]
}

struct Runner: Codable, Hashable, Identifiable, Sendable {
    let id: String
    var displayName: String
    var projects: [String]
    var adapters: [String]
    let registeredAt: Date
    var lastSeenAt: Date
    var revision: Int64

    private enum CodingKeys: String, CodingKey {
        case id
        case displayName
        case projects
        case adapters
        case registeredAt
        case lastSeenAt
        case revision
    }

    init(
        id: String,
        displayName: String,
        projects: [String],
        adapters: [String],
        registeredAt: Date,
        lastSeenAt: Date,
        revision: Int64
    ) {
        self.id = id
        self.displayName = displayName
        self.projects = projects
        self.adapters = adapters
        self.registeredAt = registeredAt
        self.lastSeenAt = lastSeenAt
        self.revision = revision
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        displayName = try container.decode(String.self, forKey: .displayName)
        // The server nulls empty project and adapter lists.
        projects = try container.decodeIfPresent([String].self, forKey: .projects) ?? []
        adapters = try container.decodeIfPresent([String].self, forKey: .adapters) ?? []
        registeredAt = try container.decode(Date.self, forKey: .registeredAt)
        lastSeenAt = try container.decode(Date.self, forKey: .lastSeenAt)
        revision = try container.decode(Int64.self, forKey: .revision)
    }
}

struct RunnerListResponse: Codable, Sendable {
    let runners: [Runner]
}

enum AgentRunStatus: String, Codable, Sendable {
    case planned
    case eligible
    case claimed
    case running
    case succeeded
    case failed
    case cancelled
    case needsApproval = "needs_approval"
    case expired
}

struct TaskContract: Codable, Hashable, Sendable {
    let runID: String
    let correlationID: String
    var objective: String
    var acceptanceCriteria: [String]
    let projectID: String
    var projectName: String
    let policyID: String
    let policyRevision: Int64
    var contractHash: String
    var allowedCapabilities: [String]
    var timeoutMinutes: Int
    var completionRule: String?

    private enum CodingKeys: String, CodingKey {
        case runID
        case correlationID
        case objective
        case acceptanceCriteria
        case projectID
        case projectName
        case policyID
        case policyRevision
        case contractHash
        case allowedCapabilities
        case timeoutMinutes
        case completionRule
    }

    init(
        runID: String,
        correlationID: String,
        objective: String,
        acceptanceCriteria: [String],
        projectID: String,
        projectName: String,
        policyID: String,
        policyRevision: Int64,
        contractHash: String,
        allowedCapabilities: [String],
        timeoutMinutes: Int,
        completionRule: String?
    ) {
        self.runID = runID
        self.correlationID = correlationID
        self.objective = objective
        self.acceptanceCriteria = acceptanceCriteria
        self.projectID = projectID
        self.projectName = projectName
        self.policyID = policyID
        self.policyRevision = policyRevision
        self.contractHash = contractHash
        self.allowedCapabilities = allowedCapabilities
        self.timeoutMinutes = timeoutMinutes
        self.completionRule = completionRule
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        runID = try container.decode(String.self, forKey: .runID)
        correlationID = try container.decode(String.self, forKey: .correlationID)
        objective = try container.decode(String.self, forKey: .objective)
        // Empty lists arrive omitted or null on the wire.
        acceptanceCriteria = try container.decodeIfPresent([String].self, forKey: .acceptanceCriteria) ?? []
        projectID = try container.decode(String.self, forKey: .projectID)
        projectName = try container.decode(String.self, forKey: .projectName)
        policyID = try container.decode(String.self, forKey: .policyID)
        policyRevision = try container.decode(Int64.self, forKey: .policyRevision)
        contractHash = try container.decode(String.self, forKey: .contractHash)
        allowedCapabilities = try container.decodeIfPresent([String].self, forKey: .allowedCapabilities) ?? []
        timeoutMinutes = try container.decode(Int.self, forKey: .timeoutMinutes)
        completionRule = try container.decodeIfPresent(String.self, forKey: .completionRule)
    }
}

struct AgentRun: Codable, Hashable, Identifiable, Sendable {
    let id: String
    let reminderID: String
    let occurrenceID: String?
    let policyID: String
    let policyRevision: Int64
    let projectID: String
    var adapter: String
    var runnerID: String?
    var status: AgentRunStatus
    var idempotencyKey: String
    var taskContract: TaskContract
    var contextCursor: Int64
    var leaseExpiresAt: Date?
    var requestedAt: Date?
    var claimedAt: Date?
    var startedAt: Date?
    var finishedAt: Date?
    var resultSummary: String?
    var resultArtifactRef: String?
    var failureCode: String?
    var approvalCapability: String?
    var createdByActor: Actor?
    var completedByActor: Actor?
    var revision: Int64
    let createdAt: Date
    var updatedAt: Date
}

struct RunListResponse: Codable, Sendable {
    let runs: [AgentRun]
}

private struct StateCodingKey: CodingKey {
    let stringValue: String
    let intValue: Int?

    init?(stringValue: String) {
        self.stringValue = stringValue
        intValue = nil
    }

    init?(intValue: Int) {
        stringValue = String(intValue)
        self.intValue = intValue
    }
}

enum StateJSON {
    static var decoder: JSONDecoder {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .custom { codingPath in
            let rawKey = codingPath.last?.stringValue ?? ""
            let components = rawKey.split(separator: "_", omittingEmptySubsequences: false)
            var converted = components.first.map(String.init) ?? rawKey
            for component in components.dropFirst() where !component.isEmpty {
                converted += component.prefix(1).uppercased() + component.dropFirst()
            }
            if converted.hasSuffix("Id"), converted != "id" {
                converted = String(converted.dropLast(2)) + "ID"
            }
            if converted.hasSuffix("Url"), converted != "url" {
                converted = String(converted.dropLast(3)) + "URL"
            }
            return StateCodingKey(stringValue: converted)!
        }
        decoder.dateDecodingStrategy = .custom { decoder in
            let value = try decoder.singleValueContainer().decode(String.self)
            let fractional = Date.ISO8601FormatStyle(includingFractionalSeconds: true)
            if let date = try? Date(value, strategy: fractional) {
                return date
            }
            return try Date(value, strategy: .iso8601)
        }
        return decoder
    }

    static var encoder: JSONEncoder {
        let encoder = JSONEncoder()
        encoder.keyEncodingStrategy = .convertToSnakeCase
        encoder.dateEncodingStrategy = .custom { date, encoder in
            var container = encoder.singleValueContainer()
            try container.encode(date.formatted(.iso8601.year().month().day().time(includingFractionalSeconds: true).timeZone(separator: .colon)))
        }
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        return encoder
    }
}
