#if os(iOS)
import UIKit

/// The iOS app delegate. Everything it does beyond launching is remote
/// notification plumbing, which exists only on iOS: the relay requires App
/// Attest, and App Attest has no Mac equivalent.
@MainActor
final class StateAppDelegate: NSObject, UIApplicationDelegate {
    private let notificationDelegate = NotificationCenterDelegate()

    func application(
        _ application: UIApplication,
        didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
    ) -> Bool {
        notificationDelegate.activate()
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
}
#endif
