import Foundation

enum ActorKind: String, Codable, Sendable {
    case owner
    case device
    case harness
    case system
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
    let reminderID: String
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
}

struct ReminderDetail: Codable, Sendable {
    let reminder: Reminder
    let comments: [Comment]
    let occurrences: [Occurrence]
    let history: [AuditEvent]
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
