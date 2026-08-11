import SwiftUI

enum StateTheme {
    static let accent = Color(red: 0.31, green: 0.32, blue: 0.91)
    static let warmBackground = Color(
        uiColor: UIColor { traits in
            traits.userInterfaceStyle == .dark
                ? UIColor(red: 0.055, green: 0.059, blue: 0.071, alpha: 1)
                : UIColor(red: 0.976, green: 0.969, blue: 0.949, alpha: 1)
        }
    )
    static let graphite = Color(
        uiColor: UIColor { traits in
            traits.userInterfaceStyle == .dark
                ? UIColor(red: 0.90, green: 0.91, blue: 0.94, alpha: 1)
                : UIColor(red: 0.12, green: 0.13, blue: 0.16, alpha: 1)
        }
    )
}

struct OriginBadge: View {
    let actor: Actor

    var body: some View {
        Label(label, systemImage: icon)
            .font(.caption.weight(.semibold))
            .foregroundStyle(color)
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
            .background(color.opacity(0.12), in: Capsule())
            .accessibilityLabel(String(localized: "Origin: \(label)"))
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
        }
    }

    private var color: Color {
        switch actor.kind {
        case .owner: .blue
        case .device: .teal
        case .harness: StateTheme.accent
        case .system: .secondary
        }
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
}
