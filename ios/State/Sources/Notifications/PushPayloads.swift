import Foundation

/// Wire contracts for the encrypted push payloads the server hands to the
/// relay. The file is compiled into the app and the notification service
/// extension, so the structs decode with the extension's plain
/// `.convertFromSnakeCase` decoder: unlike StateJSON there is no acronym
/// fixup, which is why the properties are `runId`, not `runID`.
struct ReminderPushPayload: Codable, Sendable {
    let kind: String
    let reminderId: String
    let occurrenceId: String
    let title: String
    let description: String?
    let notifyAt: String
    let revision: Int64
}

/// Payload of a `run_finished` push, mirroring NotifyRunFinished in
/// internal/push/service.go. `occurrence_id` is absent for manual runs.
struct RunPushPayload: Codable, Sendable {
    let kind: String
    let runId: String
    let reminderId: String
    let occurrenceId: String?
    let status: String
    let title: String
    let finishedAt: String
}

/// A probe that reads only the payload kind, so the extension can dispatch
/// before committing to a concrete payload shape.
struct PushKindProbe: Codable, Sendable {
    let kind: String
}

/// Localized status text for run notifications. The extension cannot reach
/// the app's string catalog, so it carries its own small de/en map; any
/// other language falls back to English.
enum RunPushStatusText {
    static func localized(_ status: String, language: String) -> String {
        let table = translations[language] ?? translations["en"] ?? [:]
        return table[status] ?? status
    }

    private static let translations: [String: [String: String]] = [
        "en": [
            "succeeded": "succeeded",
            "failed": "failed",
            "needs_approval": "needs approval",
        ],
        "de": [
            "succeeded": "erfolgreich",
            "failed": "fehlgeschlagen",
            "needs_approval": "wartet auf Freigabe",
        ],
    ]
}
