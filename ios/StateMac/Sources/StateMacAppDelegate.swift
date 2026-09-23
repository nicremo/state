import AppKit
import UserNotifications

/// macOS has no APNs route for State, because the relay requires App Attest, so
/// the delegate only wires up the local notification categories and keeps the
/// app alive after its last window closes.
@MainActor
final class StateMacAppDelegate: NSObject, NSApplicationDelegate {
    private let notificationDelegate = NotificationCenterDelegate()

    func applicationDidFinishLaunching(_ notification: Notification) {
        notificationDelegate.activate()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        false
    }
}
