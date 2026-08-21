import UserNotifications

final class NotificationService: UNNotificationServiceExtension {
    private var contentHandler: ((UNNotificationContent) -> Void)?
    private var bestAttemptContent: UNMutableNotificationContent?

    override func didReceive(
        _ request: UNNotificationRequest,
        withContentHandler contentHandler: @escaping (UNNotificationContent) -> Void
    ) {
        self.contentHandler = contentHandler
        bestAttemptContent = request.content.mutableCopy() as? UNMutableNotificationContent
        guard let content = bestAttemptContent else {
            contentHandler(request.content)
            return
        }
        // The relay already marks reminder pushes as time sensitive. Restate it
        // here so a mutated payload keeps breaking through Focus modes.
        content.interruptionLevel = .timeSensitive
        content.relevanceScore = 1
        do {
            let state = try statePayload(from: request.content.userInfo)
            guard
                let privateKey = try SharedKeychain.get(account: SharedKeychain.pushPrivateKeyAccount),
                let routeID = state["route_id"] as? String,
                let envelopeObject = state["envelope"],
                JSONSerialization.isValidJSONObject(envelopeObject)
            else {
                throw PushEnvelopeError.invalidEnvelope
            }
            let envelopeData = try JSONSerialization.data(withJSONObject: envelopeObject, options: [.sortedKeys])
            let decoder = JSONDecoder()
            decoder.keyDecodingStrategy = .convertFromSnakeCase
            let envelope = try decoder.decode(PushEnvelope.self, from: envelopeData)
            let plaintext = try envelope.open(recipientPrivateKey: privateKey, routeID: routeID)
            if try decoder.decode(PushKindProbe.self, from: plaintext).kind == "run_finished" {
                let payload = try decoder.decode(RunPushPayload.self, from: plaintext)
                content.title = "State"
                content.body = "\(payload.title) — \(RunPushStatusText.localized(payload.status, language: languageCode))"
                content.userInfo["agent_run_id"] = payload.runId
                content.userInfo["reminder_id"] = payload.reminderId
            } else {
                // Any other kind — present and future — keeps the reminder
                // shape, which is also what older servers send without a kind.
                let payload = try decoder.decode(ReminderPushPayload.self, from: plaintext)
                content.title = payload.title
                content.body = payload.description?.isEmpty == false ? payload.description ?? "" : localizedFallback(state: state)
                content.userInfo["reminder_id"] = payload.reminderId
                content.userInfo["occurrence_id"] = payload.occurrenceId
                content.userInfo["revision"] = payload.revision
            }
        } catch {
            content.title = "State"
            content.body = localizedFallback(state: (try? statePayload(from: request.content.userInfo)) ?? [:])
        }
        contentHandler(content)
    }

    override func serviceExtensionTimeWillExpire() {
        if let contentHandler, let bestAttemptContent {
            contentHandler(bestAttemptContent)
        }
    }

    private func statePayload(from userInfo: [AnyHashable: Any]) throws -> [String: Any] {
        guard let state = userInfo["state"] as? [String: Any] else {
            throw PushEnvelopeError.invalidEnvelope
        }
        return state
    }

    private var languageCode: String {
        Locale.current.language.languageCode?.identifier ?? "en"
    }

    private func localizedFallback(state: [String: Any]) -> String {
        guard let fallback = state["fallback"] as? [String: String] else {
            return "New reminder"
        }
        return fallback[languageCode] ?? fallback["en"] ?? "New reminder"
    }
}
