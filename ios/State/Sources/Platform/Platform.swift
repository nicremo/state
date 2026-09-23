import SwiftUI
#if os(iOS)
import UIKit
#elseif os(macOS)
import AppKit
#endif

/// The few platform services the shared UI needs. Everything else in the
/// app is plain SwiftUI and compiles unchanged on iOS, iPadOS and macOS.
/// Every member touches a main thread only framework, so the whole type is
/// main actor isolated and callable from view bodies.
@MainActor
enum Platform {
    static func copyToPasteboard(_ text: String) {
        #if os(iOS)
        UIPasteboard.general.string = text
        #elseif os(macOS)
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        #endif
    }

    /// The owner's name where the system knows it (the Mac account's full
    /// name), otherwise empty so the field asks for it.
    static var ownerName: String {
        #if os(macOS)
        NSFullUserName()
        #else
        ""
        #endif
    }

    static var deviceName: String {
        #if os(iOS)
        UIDevice.current.name
        #elseif os(macOS)
        Host.current().localizedName ?? "Mac"
        #endif
    }

    /// Opens the system settings page for this app's notifications.
    static var notificationSettingsURL: URL? {
        #if os(iOS)
        URL(string: UIApplication.openNotificationSettingsURLString)
        #elseif os(macOS)
        URL(string: "x-apple.systempreferences:com.apple.Notifications-Settings.extension")
        #endif
    }

    static var supportsQRScanning: Bool {
        #if os(iOS)
        true
        #else
        false
        #endif
    }

    /// The demonstrative device phrase the connect and onboarding screens use
    /// mid sentence, such as "Connect this Mac to your own server." German
    /// declines "this" by grammatical gender ("dieses iPad" but "diesen
    /// Mac"), so the whole phrase is localized per platform here instead of
    /// substituting a bare device name into one shared sentence template.
    static var deviceNoun: String {
        #if os(macOS)
        String(localized: "this Mac")
        #else
        switch UIDevice.current.userInterfaceIdiom {
        case .pad:
            String(localized: "this iPad")
        default:
            String(localized: "this iPhone")
        }
        #endif
    }
}

extension Color {
    /// A color that resolves differently in light and dark appearance.
    init(light: (Double, Double, Double, Double), dark: (Double, Double, Double, Double)) {
        #if os(iOS)
        self.init(uiColor: UIColor { traits in
            let value = traits.userInterfaceStyle == .dark ? dark : light
            return UIColor(red: value.0, green: value.1, blue: value.2, alpha: value.3)
        })
        #elseif os(macOS)
        self.init(nsColor: NSColor(name: nil) { appearance in
            let isDark = appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
            let value = isDark ? dark : light
            return NSColor(srgbRed: value.0, green: value.1, blue: value.2, alpha: value.3)
        })
        #endif
    }

    /// The background of a grouped form row, which is a system color on both
    /// platforms and therefore cannot live in `StateTheme` as fixed numbers.
    static var stateRowBackground: Color {
        #if os(iOS)
        Color(uiColor: .secondarySystemGroupedBackground)
        #elseif os(macOS)
        Color(nsColor: .controlBackgroundColor)
        #endif
    }
}

extension View {
    /// Inset grouped lists on iOS, the native inset style on macOS.
    @ViewBuilder
    func stateListStyle() -> some View {
        #if os(iOS)
        self.listStyle(.insetGrouped)
        #else
        self.listStyle(.inset)
        #endif
    }

    /// Inline navigation titles exist only on iOS.
    @ViewBuilder
    func stateInlineNavigationTitle() -> some View {
        #if os(iOS)
        self.navigationBarTitleDisplayMode(.inline)
        #else
        self
        #endif
    }

    /// Keeps forms at a readable width inside a large Mac window; iPhone
    /// and iPad forms already fit their container.
    @ViewBuilder
    func stateReadableWidth() -> some View {
        #if os(macOS)
        self.frame(maxWidth: 600).frame(maxWidth: .infinity)
        #else
        self
        #endif
    }

    @ViewBuilder
    func stateNoAutocapitalization() -> some View {
        #if os(iOS)
        self.textInputAutocapitalization(.never)
        #else
        self
        #endif
    }

    @ViewBuilder
    func stateSentenceAutocapitalization() -> some View {
        #if os(iOS)
        self.textInputAutocapitalization(.sentences)
        #else
        self
        #endif
    }

    @ViewBuilder
    func stateURLKeyboard() -> some View {
        #if os(iOS)
        self.keyboardType(.URL)
        #else
        self
        #endif
    }
}

extension ToolbarItemPlacement {
    /// Leading bar position on iOS, the navigation area on macOS.
    static var stateLeading: ToolbarItemPlacement {
        #if os(iOS)
        .topBarLeading
        #else
        .navigation
        #endif
    }
}
