import UIKit
import UserNotifications

extension Notification.Name {
    static let stateRemoteSync = Notification.Name("state.remote-sync")
    static let stateNotificationAction = Notification.Name("state.notification-action")
    static let stateAPNSToken = Notification.Name("state.apns-token")
}

enum StateNotificationAction {
    static let category = "STATE_REMINDER"
    static let complete = "STATE_COMPLETE"
    static let snoozeTenMinutes = "STATE_SNOOZE_10"
    static let snoozeOneHour = "STATE_SNOOZE_60"
}

@MainActor
final class StateAppDelegate: NSObject, UIApplicationDelegate, @preconcurrency UNUserNotificationCenterDelegate {
    func application(
        _ application: UIApplication,
        didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
    ) -> Bool {
        let center = UNUserNotificationCenter.current()
        center.delegate = self
        center.setNotificationCategories([
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
        ])
        return true
    }

    func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        NotificationCenter.default.post(name: .stateAPNSToken, object: deviceToken)
    }

    func application(_ application: UIApplication, didFailToRegisterForRemoteNotificationsWithError error: Error) {
        NotificationCenter.default.post(name: .stateAPNSToken, object: error)
    }

    func application(
        _ application: UIApplication,
        didReceiveRemoteNotification userInfo: [AnyHashable: Any],
        fetchCompletionHandler completionHandler: @escaping (UIBackgroundFetchResult) -> Void
    ) {
        NotificationCenter.default.post(name: .stateRemoteSync, object: nil)
        completionHandler(.newData)
    }

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification
    ) async -> UNNotificationPresentationOptions {
        [.banner, .list, .sound]
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
            ]
        )
    }
}
