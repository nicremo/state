import AppKit
import SwiftUI

@main
struct StateServerApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @State private var controller = ServerController()

    var body: some Scene {
        Window("State Server", id: "server") {
            ServerView(controller: controller)
                .task {
                    delegate.controller = controller
                    controller.launch()
                }
        }
        .defaultSize(width: 620, height: 680)
        .windowResizability(.contentSize)
        MenuBarExtra {
            MenuContent(controller: controller)
        } label: {
            Image(systemName: controller.phase == .running ? "externaldrive.fill.badge.checkmark" : "externaldrive.badge.exclamationmark")
                .accessibilityLabel("State Server: \(controller.label)")
        }
    }
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    var controller: ServerController?

    func applicationDidFinishLaunching(_ notification: Notification) {
        let others = NSRunningApplication.runningApplications(withBundleIdentifier: Bundle.main.bundleIdentifier ?? "")
        if let existing = others.first(where: { $0.processIdentifier != ProcessInfo.processInfo.processIdentifier }) {
            existing.activate()
            NSApplication.shared.terminate(nil)
            return
        }
        NSApp.activate()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard let controller, controller.phase != .stopped else { return .terminateNow }
        controller.prepareToQuit()
        // EOF and SIGTERM both request an orderly database shutdown.
        Task {
            try? await Task.sleep(for: .seconds(15))
            NSApplication.shared.reply(toApplicationShouldTerminate: true)
        }
        return .terminateLater
    }
}

private struct MenuContent: View {
    @Bindable var controller: ServerController
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Text(controller.label)
        Button("State Server öffnen") {
            openWindow(id: "server")
            NSApp.activate()
            controller.refreshLoginStatus()
        }
        Divider()
        if controller.phase == .running {
            Button("Serveradresse kopieren") { controller.copyAddress() }
            Button("Server stoppen") { controller.stop() }
        } else if controller.phase != .stopping {
            Button("Server starten") { controller.start() }
        }
        Divider()
        Button("State Server beenden") { NSApp.terminate(nil) }
            .keyboardShortcut("q")
    }
}
