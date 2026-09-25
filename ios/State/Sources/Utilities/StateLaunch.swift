import Foundation

/// Launch time switches. The screenshot run needs the app to land on the
/// connection screen immediately, so it skips the launch animation and the
/// introduction rather than trying to tap through them frame by frame.
enum StateLaunch {
    static let uiTestingArgument = "-stateUITesting"

    static var isUITesting: Bool {
        ProcessInfo.processInfo.arguments.contains(uiTestingArgument)
    }

    #if DEBUG
    /// Debug builds only: open straight into the demo on a chosen tab, so
    /// design reviews can capture every screen without tapping.
    static var opensDemo: Bool {
        ProcessInfo.processInfo.arguments.contains("-stateDemo")
    }

    static var initialTab: String? {
        let arguments = ProcessInfo.processInfo.arguments
        guard let index = arguments.firstIndex(of: "-stateTab"), arguments.indices.contains(index + 1) else { return nil }
        return arguments[index + 1]
    }

    /// `-stateNote first` opens the newest note, `-stateNote new` the editor.
    static var initialNote: String? {
        let arguments = ProcessInfo.processInfo.arguments
        guard let index = arguments.firstIndex(of: "-stateNote"), arguments.indices.contains(index + 1) else { return nil }
        return arguments[index + 1]
    }
    /// `-stateCapture image` or `audio` opens that capture flow on the Notes tab.
    static var initialCapture: String? {
        let arguments = ProcessInfo.processInfo.arguments
        guard let index = arguments.firstIndex(of: "-stateCapture"), arguments.indices.contains(index + 1) else { return nil }
        return arguments[index + 1]
    }

    /// `-stateBootstrap <server URL> <bootstrap token>` connects to a local
    /// test server as its owner, for end-to-end runs in the simulator.
    static var bootstrap: (url: URL, token: String)? {
        let arguments = ProcessInfo.processInfo.arguments
        guard let index = arguments.firstIndex(of: "-stateBootstrap"), arguments.indices.contains(index + 2),
              let url = URL(string: arguments[index + 1]) else { return nil }
        return (url, arguments[index + 2])
    }
    #endif
}
