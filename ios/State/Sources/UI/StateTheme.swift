import SwiftUI

/// The design tokens the whole app draws from. Everything here adapts to light
/// and dark appearance, because a self hosted tool gets opened at every hour.
enum StateTheme {
    /// Ink. The one interactive color, taken from the black mark of the app
    /// icon: near black on light ground, near white on dark ground, so every
    /// control reads as printed rather than tinted.
    static let accent = Color(
        light: (0.071, 0.075, 0.086, 1),
        dark: (0.955, 0.960, 0.970, 1)
    )

    /// The accent at the strength a filled capsule or a tinted background needs.
    static let accentSoft = Color(
        light: (0.071, 0.075, 0.086, 0.06),
        dark: (0.955, 0.960, 0.970, 0.12)
    )

    /// Text and glyphs that sit on an ink filled control.
    static let onAccent = Color(
        light: (1, 1, 1, 1),
        dark: (0.043, 0.047, 0.055, 1)
    )

    /// The ground of every screen: a cool, almost white paper in light
    /// appearance and the launch ink in dark appearance.
    static let ground = Color(
        light: (0.965, 0.970, 0.978, 1),
        dark: (0.043, 0.047, 0.055, 1)
    )

    /// The pale blue grey behind the icon's mark. Launch screen, splash and the
    /// welcome moment stand on it, so the icon the owner tapped seems to open.
    static let mist = Color(
        light: (0.871, 0.906, 0.941, 1),
        dark: (0.043, 0.047, 0.055, 1)
    )

    /// The yellow dot of the icon. Used once per screen at most, never for text.
    static let signal = Color(red: 0.878, green: 0.867, blue: 0.157)

    /// The primary text color.
    static let graphite = Color(
        light: (0.071, 0.075, 0.086, 1),
        dark: (0.920, 0.928, 0.940, 1)
    )

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
    /// One ground for every screen, shared with the launch screen.
    func stateBackground() -> some View {
        scrollContentBackground(.hidden)
            .background(StateTheme.ground.ignoresSafeArea())
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
            .shadow(color: .black.opacity(0.10), radius: size * 0.06, y: size * 0.03)
            .shadow(color: .black.opacity(0.08), radius: size * 0.18, y: size * 0.10)
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

/// The one filled action per screen: ink on paper, printed rather than tinted.
struct StatePrimaryButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.body.weight(.semibold))
            .foregroundStyle(StateTheme.onAccent)
            .frame(maxWidth: .infinity, minHeight: StateControlMetrics.height)
            .background(StateTheme.accent, in: RoundedRectangle(cornerRadius: StateControlMetrics.radius, style: .continuous))
            .contentShape(RoundedRectangle(cornerRadius: StateControlMetrics.radius, style: .continuous))
            .opacity(isEnabled ? (configuration.isPressed ? 0.8 : 1) : 0.25)
            .scaleEffect(configuration.isPressed ? 0.985 : 1)
            .animation(.smooth(duration: 0.16), value: configuration.isPressed)
            .animation(StateTheme.stateChange, value: isEnabled)
    }
}

/// The quiet second choice next to a primary action.
struct StateSecondaryButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.body.weight(.semibold))
            .foregroundStyle(StateTheme.accent)
            .frame(maxWidth: .infinity, minHeight: StateControlMetrics.height)
            .background(StateTheme.accentSoft, in: RoundedRectangle(cornerRadius: StateControlMetrics.radius, style: .continuous))
            .contentShape(RoundedRectangle(cornerRadius: StateControlMetrics.radius, style: .continuous))
            .opacity(isEnabled ? (configuration.isPressed ? 0.6 : 1) : 0.35)
            .animation(.smooth(duration: 0.16), value: configuration.isPressed)
    }
}

/// A compact filled action for empty states: the primary style's ink and
/// onAccent pairing, sized to its label. `.borderedProminent` would keep a
/// white label on the near-white dark-mode ink and make it unreadable.
struct StatePillButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.subheadline.weight(.semibold))
            .foregroundStyle(StateTheme.onAccent)
            .padding(.horizontal, StateTheme.Space.block)
            .frame(minHeight: 36)
            .background(StateTheme.accent, in: Capsule())
            .contentShape(Capsule())
            .opacity(isEnabled ? (configuration.isPressed ? 0.8 : 1) : 0.25)
            .animation(.smooth(duration: 0.16), value: configuration.isPressed)
    }
}

/// Button geometry per platform: a thumb sized bar on iPhone and iPad, a
/// pointer sized one on the Mac.
enum StateControlMetrics {
    #if os(macOS)
    static let height: CGFloat = 40
    static let radius: CGFloat = 10
    #else
    static let height: CGFloat = 54
    static let radius: CGFloat = 16
    #endif
}

extension ButtonStyle where Self == StatePrimaryButtonStyle {
    static var statePrimary: StatePrimaryButtonStyle { StatePrimaryButtonStyle() }
}

extension ButtonStyle where Self == StateSecondaryButtonStyle {
    static var stateSecondary: StateSecondaryButtonStyle { StateSecondaryButtonStyle() }
}

extension ButtonStyle where Self == StatePillButtonStyle {
    static var statePill: StatePillButtonStyle { StatePillButtonStyle() }
}
