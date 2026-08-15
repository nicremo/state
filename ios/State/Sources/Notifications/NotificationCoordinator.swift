import UIKit
import UserNotifications

@MainActor
final class NotificationCoordinator {
    // Reminders must survive Focus modes, so State asks for the time sensitive
    // permission alongside the standard alert options. The settings option adds
    // a link from the iOS notification settings back into the app.
    static let authorizationOptions: UNAuthorizationOptions = [
        .alert,
        .badge,
        .sound,
        .timeSensitive,
        .providesAppNotificationSettings,
    ]

    private let center: UNUserNotificationCenter

    init(center: UNUserNotificationCenter = .current()) {
        self.center = center
    }

    func activate(model: AppModel) async {
        do {
            let granted = try await center.requestAuthorization(options: Self.authorizationOptions)
            guard granted else { return }
            UIApplication.shared.registerForRemoteNotifications()
            await refresh(model: model)
        } catch {
            model.presentedError = error.localizedDescription
        }
    }

    func refresh(model: AppModel) async {
        let existing = await center.pendingNotificationRequests()
        let stateIdentifiers = existing.map(\.identifier).filter { $0.hasPrefix("state-occurrence-") }
        center.removePendingNotificationRequests(withIdentifiers: stateIdentifiers)

        let now = Date()
        let items = await model.notificationItems(limit: 64)
        var confirmed: [String] = []
        for (reminder, occurrence) in items.prefix(64) {
            guard let notificationDate = notificationDate(for: occurrence), notificationDate > now else { continue }
            let content = UNMutableNotificationContent()
            content.title = reminder.title
            content.body = reminder.description?.isEmpty == false
                ? reminder.description ?? ""
                : String(localized: "A reminder is due.")
            content.sound = .default
            content.interruptionLevel = .timeSensitive
            content.relevanceScore = 1
            content.categoryIdentifier = StateNotificationAction.category
            content.threadIdentifier = reminder.id
            content.userInfo = [
                "occurrence_id": occurrence.id,
                "reminder_id": reminder.id,
                "revision": occurrence.revision,
            ]
            let trigger = UNTimeIntervalNotificationTrigger(
                timeInterval: max(1, notificationDate.timeIntervalSince(now)),
                repeats: false
            )
            let request = UNNotificationRequest(
                identifier: "state-occurrence-\(occurrence.id)",
                content: content,
                trigger: trigger
            )
            do {
                try await center.add(request)
                confirmed.append(occurrence.id)
            } catch {
                model.presentedError = error.localizedDescription
            }
        }
        await model.confirmNotifications(confirmed)
    }

    private func notificationDate(for occurrence: Occurrence) -> Date? {
        if let snoozedUntil = occurrence.snoozedUntil {
            return snoozedUntil
        }
        guard let scheduledAt = occurrence.scheduledAt else { return nil }
        return scheduledAt.addingTimeInterval(TimeInterval(-(occurrence.prewarningMinutes ?? 0) * 60))
    }
}
