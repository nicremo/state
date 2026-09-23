import Foundation

/// Launch time switches. The screenshot run needs the app to land on the
/// connection screen immediately, so it skips the launch animation and the
/// introduction rather than trying to tap through them frame by frame.
enum StateLaunch {
    static let uiTestingArgument = "-stateUITesting"

    static var isUITesting: Bool {
        ProcessInfo.processInfo.arguments.contains(uiTestingArgument)
    }
}
