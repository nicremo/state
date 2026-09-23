import SwiftUI
import UIKit

/// The design tokens the whole app draws from. Everything here adapts to light
/// and dark appearance, because a self hosted tool gets opened at every hour.
enum StateTheme {
    /// Deep indigo, taken from the glowing core of the app icon and calmed down
    /// until it reads as ink rather than neon. Light appearance keeps it dark
    /// enough for white text on a filled button, dark appearance lifts it far
    /// enough to stay legible on near black.
    static let accent = Color(
        uiColor: UIColor { traits in
            traits.userInterfaceStyle == .dark
                ? UIColor(red: 0.62, green: 0.66, blue: 0.98, alpha: 1)
                : UIColor(red: 0.22, green: 0.26, blue: 0.62, alpha: 1)
        }
    )

    /// The accent at the strength a filled capsule or a tinted background needs.
    static let accentSoft = Color(
        uiColor: UIColor { traits in
            traits.userInterfaceStyle == .dark
                ? UIColor(red: 0.62, green: 0.66, blue: 0.98, alpha: 0.18)
                : UIColor(red: 0.22, green: 0.26, blue: 0.62, alpha: 0.10)
        }
    )

    /// Warm ivory in light appearance, echoing the glow behind the app icon.
    static let warmBackground = Color(
        uiColor: UIColor { traits in
            traits.userInterfaceStyle == .dark
                ? UIColor(red: 0.055, green: 0.059, blue: 0.071, alpha: 1)
                : UIColor(red: 0.973, green: 0.969, blue: 0.956, alpha: 1)
        }
    )

    /// The primary text color. Slightly warmer than pure label so it sits well
    /// on the ivory background.
    static let graphite = Color(
        uiColor: UIColor { traits in
            traits.userInterfaceStyle == .dark
                ? UIColor(red: 0.90, green: 0.91, blue: 0.94, alpha: 1)
                : UIColor(red: 0.12, green: 0.13, blue: 0.16, alpha: 1)
        }
    )

    /// The launch and onboarding backdrop. Always dark, because the app icon is
    /// a lit object on a dark ground and the first screen should be that object.
    static let markBackdrop = Color(red: 0.055, green: 0.059, blue: 0.071)

    /// The 4 point spacing scale. Named by role, not by number, so a change of
    /// rhythm stays a one line change.
    enum Space {
        /// 2 pt. Between a value and its own unit or badge.
        static let hairline: CGFloat = 2
        /// 4 pt. Inside a tight pair such as an icon and its label.
        static let tight: CGFloat = 4
        /// 6 pt. Between lines that belong to the same thought.
        static let snug: CGFloat = 6
        /// 8 pt. Between elements of one group.
        static let inner: CGFloat = 8
        /// 12 pt. Between two groups inside a row.
        static let group: CGFloat = 12
        /// 16 pt. Between blocks.
        static let block: CGFloat = 16
        /// 24 pt. Between sections of a screen.
        static let section: CGFloat = 24
        /// 32 pt. Around a screen's leading statement.
        static let stage: CGFloat = 32
    }

    /// The one motion curve the app uses for state changes.
    static let stateChange = Animation.smooth(duration: 0.22)
    /// Slightly longer, for content that appears or disappears.
    static let contentChange = Animation.smooth(duration: 0.3)
}

extension View {
    /// One ground for every screen. The system grouped background is faintly
    /// blue; State's is faintly warm, so the app and the lit mark on the launch
    /// screen belong to the same world.
    func stateBackground() -> some View {
        scrollContentBackground(.hidden)
            .background(StateTheme.warmBackground.ignoresSafeArea())
    }
}

/// SwiftUI's default `Label` reserves the width of a full size icon no matter
/// how small the text is, which leaves a visible hole between a caption sized
/// glyph and its word. This style sets the gap explicitly instead.
struct TightLabelStyle: LabelStyle {
    var spacing: CGFloat = StateTheme.Space.tight

    func makeBody(configuration: Configuration) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: spacing) {
            configuration.icon
                .imageScale(.small)
            configuration.title
        }
    }
}

extension LabelStyle where Self == TightLabelStyle {
    /// An icon sitting directly next to its text, for captions and badges.
    static var tight: TightLabelStyle { TightLabelStyle() }
}

/// A caption sized icon and text pair used for metadata such as a due date.
struct MetaLabel: View {
    let text: String
    let systemImage: String
    var tint: Color = .secondary

    var body: some View {
        Label(text, systemImage: systemImage)
            .labelStyle(.tight)
            .font(.caption)
            .foregroundStyle(tint)
    }
}

/// Names who caused something: the owner, a device, an agent or the server.
struct OriginBadge: View {
    let actor: Actor

    var body: some View {
        HStack(spacing: StateTheme.Space.tight) {
            Image(systemName: icon)
                .font(.system(size: 9, weight: .semibold))
            Text(label)
                .font(.caption2.weight(.semibold))
                .lineLimit(1)
        }
        .foregroundStyle(color)
        .padding(.horizontal, StateTheme.Space.snug)
        .padding(.vertical, 3)
        .background(color.opacity(0.12), in: Capsule())
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(String(format: String(localized: "Origin: %@"), label))
    }

    private var label: String {
        actor.displayName ?? actor.harness ?? actor.deviceName ?? actor.kind.rawValue.capitalized
    }

    private var icon: String {
        switch actor.kind {
        case .owner: "person.fill"
        case .device: "iphone"
        case .harness: "terminal.fill"
        case .system: "gearshape.fill"
        case .runner: "desktopcomputer"
        }
    }

    private var color: Color {
        switch actor.kind {
        case .owner: .blue
        case .device: .teal
        case .harness: StateTheme.accent
        case .system: .secondary
        case .runner: .purple
        }
    }
}

/// The app icon, shown inside the app. It is the same artwork the home screen
/// shows, so the launch moment and the icon the owner tapped are one object
/// rather than two drawings that almost match.
struct StateMark: View {
    var size: CGFloat = 96
    /// 0 while the mark is still arriving, 1 once it has settled.
    var progress: Double = 1

    var body: some View {
        Image("AppMark")
            .resizable()
            .scaledToFit()
            .frame(width: size, height: size)
            .clipShape(RoundedRectangle(cornerRadius: size * 0.225, style: .continuous))
            .shadow(color: .black.opacity(0.35), radius: size * 0.1, y: size * 0.04)
            .opacity(progress)
            .scaleEffect(0.9 + 0.1 * progress)
            .accessibilityHidden(true)
    }
}

enum StateDateFormatter {
    static func date(from schedule: Schedule) -> Date? {
        let dates = schedule.localDate.split(separator: "-").compactMap { Int($0) }
        guard dates.count == 3 else { return nil }
        let times = schedule.localTime?.split(separator: ":").compactMap { Int($0) } ?? []
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: schedule.timeZone) ?? .current
        return calendar.date(
            from: DateComponents(
                year: dates[0],
                month: dates[1],
                day: dates[2],
                hour: times.first,
                minute: times.count > 1 ? times[1] : nil
            )
        )
    }

    static func label(for schedule: Schedule) -> String {
        guard let date = date(from: schedule) else { return schedule.localDate }
        if schedule.localTime == nil {
            return date.formatted(date: .abbreviated, time: .omitted)
        }
        return date.formatted(date: .abbreviated, time: .shortened)
    }

    /// A short relative label for a list row: today and tomorrow read better as
    /// words, everything else keeps its date.
    static func rowLabel(for schedule: Schedule) -> String {
        guard let date = date(from: schedule) else { return schedule.localDate }
        let calendar = Calendar.current
        let time = schedule.localTime == nil
            ? nil
            : date.formatted(date: .omitted, time: .shortened)
        let day: String
        if calendar.isDateInToday(date) {
            day = String(localized: "Today")
        } else if calendar.isDateInTomorrow(date) {
            day = String(localized: "Tomorrow")
        } else if calendar.isDateInYesterday(date) {
            day = String(localized: "Yesterday")
        } else {
            day = date.formatted(date: .abbreviated, time: .omitted)
        }
        guard let time else { return day }
        return "\(day), \(time)"
    }
}
