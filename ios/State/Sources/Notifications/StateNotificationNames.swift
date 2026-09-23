import Foundation

// The names both app delegates post through, so iOS and macOS share them.
extension Notification.Name {
    static let stateRemoteSync = Notification.Name("state.remote-sync")
    static let stateNotificationAction = Notification.Name("state.notification-action")
    static let stateAPNSToken = Notification.Name("state.apns-token")
    static let stateOpenNotificationSettings = Notification.Name("state.open-notification-settings")
}

enum StateNotificationAction {
    static let category = "STATE_REMINDER"
    /// Run notifications are view-only: no actions, the tap just opens State.
    static let runCategory = "STATE_RUN"
    static let complete = "STATE_COMPLETE"
    static let snoozeTenMinutes = "STATE_SNOOZE_10"
    static let snoozeOneHour = "STATE_SNOOZE_60"
}
