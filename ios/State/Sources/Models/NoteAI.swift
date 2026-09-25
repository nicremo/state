import Foundation

/// A photo or recording of a note. The original stays on the server; the
/// derived text is the OCR of a photo or the transcript of a recording.
struct NoteAttachment: Codable, Hashable, Identifiable, Sendable {
    let id: String
    let noteID: String
    let ordinal: Int
    let kind: String
    let mimeType: String
    let byteSize: Int64
    let sha256: String
    var durationMs: Int64?
    var derivedText: String?
    var derivedKind: String?
    var derivedModel: String?
    var createdAt: Date?

    var isImage: Bool { kind == "image" }
    var isAudio: Bool { kind == "audio" }
}

/// Where the notes AI is with a note.
struct NoteProcessing: Codable, Hashable, Sendable {
    var status: String
    var error: String?
    var model: String?
    var updatedAt: Date?

    static let idle = "idle"
    static let queued = "queued"
    static let running = "processing"
    static let ready = "ready"
    static let needsReview = "needs_review"
    static let failed = "failed"
    static let consentRequired = "consent_required"
    static let notConfigured = "not_configured"
    static let budgetExhausted = "budget_exhausted"

    var isWorking: Bool { status == Self.queued || status == Self.running }
}

/// The AI's title and summary with the model that made them.
struct NoteAIResult: Codable, Hashable, Sendable {
    var title: String?
    var summary: String?
    var model: String
    var sourceHash: String?
    var proposedDocument: String?
    var needsReview: Bool?
    var updatedAt: Date?
}

struct NoteRelation: Codable, Hashable, Identifiable, Sendable {
    let id: String
    let noteID: String
    let relatedNoteID: String
    var relatedTitle: String?
    var reason: String
    var confidence: Double?
}

/// A reminder the AI found in a note. It exists only once the owner
/// confirms it here.
struct ReminderProposal: Codable, Hashable, Identifiable, Sendable {
    let id: String
    let noteID: String
    var title: String
    var description: String?
    var localDate: String?
    var localTime: String?
    var reason: String?
    var status: String
    var reminderID: String?

    static let pending = "pending"
}

/// What the server allows right now. The photo limit comes from here and
/// is never built into the app.
struct NoteCapabilities: Codable, Hashable, Sendable {
    struct Agent: Codable, Hashable, Sendable {
        var model: String
        var available: Bool
        var toolCalls: Bool
    }

    struct Vision: Codable, Hashable, Sendable {
        var available: Bool
        var maxImages: Int
        var maxImageBytes: Int64
        var maxTotalBytes: Int64
        var model: String
        var limitSource: String?
    }

    struct Audio: Codable, Hashable, Sendable {
        var available: Bool
        var maxSegmentBytes: Int64
        var maxSegments: Int
        var segmentSeconds: Int
        var model: String
    }

    var aiAvailable: Bool
    var reason: String?
    var consent: Bool
    var agent: Agent
    var vision: Vision
    var audio: Audio

    /// Used while the server has not answered yet: one photo, five minutes
    /// of audio. The server checks every upload again.
    static let offline = NoteCapabilities(
        aiAvailable: false,
        reason: "offline",
        consent: false,
        agent: Agent(model: "", available: false, toolCalls: false),
        vision: Vision(available: false, maxImages: 1, maxImageBytes: 8 << 20, maxTotalBytes: 8 << 20, model: "", limitSource: nil),
        audio: Audio(available: false, maxSegmentBytes: 20 << 20, maxSegments: 12, segmentSeconds: 300, model: "")
    )

    /// Whether this server takes photos at all. It refuses them when its
    /// model runs but cannot read images.
    var acceptsPhotos: Bool { !(agent.available && !vision.available) }

    /// How many photos one note may hold: the model's limit, or one photo
    /// while the server has no model to ask, so a photo can still be kept.
    var photoLimit: Int {
        guard acceptsPhotos else { return 0 }
        return vision.available ? max(1, vision.maxImages) : 1
    }
}

struct NotesAISettings: Codable, Hashable, Sendable {
    var consent: Bool
    var consentAt: Date?
    var monthlyLimitUsd: Double
    var agentModel: String
    var transcriptionModel: String
    var month: String?
    var spentThisMonthUsd: Double
    var keyConfigured: Bool
}

/// A photo or recording waiting on this device for its upload.
struct NoteUpload: Hashable, Identifiable, Sendable {
    let id: String
    var noteID: String
    let ordinal: Int
    let kind: String
    let mimeType: String
    let fileName: String
    let sha256: String
    let byteSize: Int64
    let durationMs: Int64?
    let requestID: String
    var status: String
    var error: String?

    static let pending = "pending"
    static let done = "done"
    static let failed = "failed"
}
