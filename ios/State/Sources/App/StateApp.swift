import SwiftUI

@main
struct StateApp: App {
    @UIApplicationDelegateAdaptor(StateAppDelegate.self) private var appDelegate
    @State private var model: AppModel

    init() {
        do {
            let database = try StateDatabase.applicationDatabase()
            _model = State(initialValue: AppModel(database: database))
        } catch {
            fatalError("State database initialization failed: \(error.localizedDescription)")
        }
    }

    var body: some Scene {
        WindowGroup {
            StateRootView(model: model)
        }
    }
}
