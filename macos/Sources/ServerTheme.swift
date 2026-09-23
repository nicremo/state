import AppKit
import SwiftUI

/// The State design tokens for the server app: ink on cool paper, the same
/// values the State client uses (see DESIGN.md), without sharing its target.
enum ServerTheme {
    static let ink = adaptive(light: NSColor(srgbRed: 0.071, green: 0.075, blue: 0.086, alpha: 1),
                              dark: NSColor(srgbRed: 0.955, green: 0.960, blue: 0.970, alpha: 1))
    static let onInk = adaptive(light: .white,
                                dark: NSColor(srgbRed: 0.043, green: 0.047, blue: 0.055, alpha: 1))
    static let inkSoft = adaptive(light: NSColor(srgbRed: 0.071, green: 0.075, blue: 0.086, alpha: 0.06),
                                  dark: NSColor(srgbRed: 0.955, green: 0.960, blue: 0.970, alpha: 0.12))
    static let ground = adaptive(light: NSColor(srgbRed: 0.965, green: 0.970, blue: 0.978, alpha: 1),
                                 dark: NSColor(srgbRed: 0.043, green: 0.047, blue: 0.055, alpha: 1))

    private static func adaptive(light: NSColor, dark: NSColor) -> Color {
        Color(nsColor: NSColor(name: nil) { appearance in
            appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua ? dark : light
        })
    }
}

/// Filled ink action, one per section at most.
struct InkButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.body.weight(.semibold))
            .foregroundStyle(ServerTheme.onInk)
            .padding(.horizontal, 18)
            .frame(minHeight: 34)
            .background(ServerTheme.ink, in: RoundedRectangle(cornerRadius: 9, style: .continuous))
            .contentShape(RoundedRectangle(cornerRadius: 9, style: .continuous))
            .opacity(isEnabled ? (configuration.isPressed ? 0.8 : 1) : 0.3)
    }
}

/// Quiet companion to the ink action.
struct SoftButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.body.weight(.medium))
            .foregroundStyle(ServerTheme.ink)
            .padding(.horizontal, 14)
            .frame(minHeight: 30)
            .background(ServerTheme.inkSoft, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            .contentShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
            .opacity(isEnabled ? (configuration.isPressed ? 0.6 : 1) : 0.35)
    }
}
