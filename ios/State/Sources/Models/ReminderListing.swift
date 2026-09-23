import Foundation

/// What the lists need to know about a reminder's occurrences.
struct OccurrenceSummary: Equatable, Sendable {
    /// The earliest occurrence that is still pending or snoozed.
    var next: Occurrence?
    /// Whether the reminder has any occurrence at all, finished ones included.
    var hasAny: Bool
}

enum ReminderBucket: Equatable, Sendable {
    case today
    case planned
    case done
}

/// Decides where a reminder belongs. The next open occurrence wins over the
/// reminder's first date, so a finished one-off leaves the lists and a
/// monthly reminder shows the month that is actually due.
enum ReminderListing {
    static func bucket(for reminder: Reminder, summary: OccurrenceSummary?, today: String) -> ReminderBucket {
        if let next = summary?.next {
            return next.localDate <= today ? .today : .planned
        }
        if summary?.hasAny == true {
            return .done
        }
        guard let schedule = reminder.schedule else { return .planned }
        return schedule.localDate <= today ? .today : .planned
    }

    /// The date the row shows: the next open occurrence, else the reminder's own.
    static func dueSchedule(for reminder: Reminder, summary: OccurrenceSummary?) -> Schedule? {
        guard let next = summary?.next else { return reminder.schedule }
        return Schedule(
            localDate: next.localDate,
            localTime: next.localTime,
            timeZone: next.timeZone,
            mode: next.timeZoneMode,
            prewarningMinutes: next.prewarningMinutes
        )
    }
}
