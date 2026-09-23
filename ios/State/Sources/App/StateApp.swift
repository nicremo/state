import SwiftUI

@main
struct StateApp: App {
    #if os(iOS)
    @UIApplicationDelegateAdaptor(StateAppDelegate.self) private var appDelegate
    #elseif os(macOS)
    @NSApplicationDelegateAdaptor(StateMacAppDelegate.self) private var appDelegate
    #endif
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
                #if os(macOS)
                .frame(minWidth: 720, minHeight: 520)
                #endif
        }
        #if os(macOS)
        .defaultSize(width: 1080, height: 720)
        .commands {
            CommandGroup(replacing: .newItem) {
                Button(String(localized: "New reminder")) {
                    NotificationCenter.default.post(name: .stateCreateReminder, object: nil)
                }
                .keyboardShortcut("n")
            }
            CommandMenu(String(localized: "Sync")) {
                Button(String(localized: "Synchronize now")) {
                    NotificationCenter.default.post(name: .stateSynchronizeNow, object: nil)
                }
                .keyboardShortcut("r")
            }
        }
        #endif
    }
}
