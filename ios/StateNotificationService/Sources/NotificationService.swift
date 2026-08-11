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
            let payload = try decoder.decode(ReminderPushPayload.self, from: plaintext)
            content.title = payload.title
            content.body = payload.description?.isEmpty == false ? payload.description ?? "" : localizedFallback(state: state)
            content.userInfo["reminder_id"] = payload.reminderID
            content.userInfo["occurrence_id"] = payload.occurrenceID
            content.userInfo["revision"] = payload.revision
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

    private func localizedFallback(state: [String: Any]) -> String {
        guard let fallback = state["fallback"] as? [String: String] else {
            return "New reminder"
        }
        let language = Locale.current.language.languageCode?.identifier ?? "en"
        return fallback[language] ?? fallback["en"] ?? "New reminder"
    }
}

private struct ReminderPushPayload: Codable {
    let kind: String
    let reminderID: String
    let occurrenceID: String
    let title: String
    let description: String?
    let notifyAt: String
    let revision: Int64
}
