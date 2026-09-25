import Foundation
import UserNotifications

/// What State does when a notification arrives or is tapped. It lives apart
/// from the app delegate because iOS and macOS have different app delegates but
/// exactly the same notification behavior.
@MainActor
final class NotificationCenterDelegate: NSObject, @preconcurrency UNUserNotificationCenterDelegate {
    /// Becomes the notification center's delegate and registers the categories
    /// that give a reminder its Complete and Snooze actions.
    func activate() {
        let center = UNUserNotificationCenter.current()
        center.delegate = self
        center.setNotificationCategories(Self.categories)
    }

    private static var categories: Set<UNNotificationCategory> {
        [
            UNNotificationCategory(
                identifier: StateNotificationAction.category,
                actions: [
                    UNNotificationAction(
                        identifier: StateNotificationAction.complete,
                        title: String(localized: "Complete"),
                        options: []
                    ),
                    UNNotificationAction(
                        identifier: StateNotificationAction.snoozeTenMinutes,
                        title: String(localized: "Snooze 10 minutes"),
                        options: []
                    ),
                    UNNotificationAction(
                        identifier: StateNotificationAction.snoozeOneHour,
                        title: String(localized: "Snooze 1 hour"),
                        options: []
                    ),
                ],
                intentIdentifiers: [],
                options: [.customDismissAction]
            ),
            UNNotificationCategory(
                identifier: StateNotificationAction.runCategory,
                actions: [],
                intentIdentifiers: [],
                options: [.customDismissAction]
            ),
        ]
    }

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification
    ) async -> UNNotificationPresentationOptions {
        [.banner, .list, .sound]
    }

    // Answers the "Notification Settings" entry that the system shows because
    // State requests providesAppNotificationSettings.
    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        openSettingsFor notification: UNNotification?
    ) {
        NotificationCenter.default.post(name: .stateOpenNotificationSettings, object: nil)
    }

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse
    ) async {
        NotificationCenter.default.post(
            name: .stateNotificationAction,
            object: nil,
            userInfo: [
                "action": response.actionIdentifier,
                "occurrence_id": response.notification.request.content.userInfo["occurrence_id"] as? String ?? "",
                "reminder_id": response.notification.request.content.userInfo["reminder_id"] as? String ?? "",
                "agent_run_id": response.notification.request.content.userInfo["agent_run_id"] as? String ?? "",
                "agent_session_id": response.notification.request.content.userInfo["agent_session_id"] as? String ?? "",
            ]
        )
    }
}
