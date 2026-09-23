import XCTest
@testable import State

/// Lists follow the next open occurrence, not the reminder's first date: a
/// finished one-off leaves the lists, a monthly one shows its next date.
final class ReminderListingTests: XCTestCase {
    private let today = "2026-09-23"

    func testOpenOccurrenceDecidesTheList() {
        let reminder = Reminder.fixture(id: "r1", revision: 1)
        XCTAssertEqual(ReminderListing.bucket(for: reminder, summary: summary(next: "2026-09-23"), today: today), .today)
        XCTAssertEqual(ReminderListing.bucket(for: reminder, summary: summary(next: "2026-09-20"), today: today), .today)
        XCTAssertEqual(ReminderListing.bucket(for: reminder, summary: summary(next: "2026-10-01"), today: today), .planned)
    }

    func testReminderWithOnlyCompletedOccurrencesIsDone() {
        let reminder = Reminder.fixture(id: "r1", revision: 1)
        XCTAssertEqual(
            ReminderListing.bucket(for: reminder, summary: OccurrenceSummary(next: nil, hasAny: true), today: today),
            .done
        )
    }

    func testWithoutOccurrencesTheReminderScheduleDecides() {
        var dated = Reminder.fixture(id: "r1", revision: 1)
        dated.schedule = Schedule(localDate: "2026-09-22", localTime: nil, timeZone: "Europe/Berlin", mode: .fixed, prewarningMinutes: nil)
        XCTAssertEqual(ReminderListing.bucket(for: dated, summary: nil, today: today), .today)

        var undated = Reminder.fixture(id: "r2", revision: 1)
        undated.schedule = nil
        XCTAssertEqual(ReminderListing.bucket(for: undated, summary: nil, today: today), .planned)
    }

    func testDueScheduleShowsTheNextOccurrence() {
        var reminder = Reminder.fixture(id: "r1", revision: 1)
        reminder.schedule = Schedule(localDate: "2026-01-01", localTime: "09:00", timeZone: "Europe/Berlin", mode: .fixed, prewarningMinutes: nil)
        let due = ReminderListing.dueSchedule(for: reminder, summary: summary(next: "2026-10-01"))
        XCTAssertEqual(due?.localDate, "2026-10-01")
    }

    private func summary(next localDate: String) -> OccurrenceSummary {
        OccurrenceSummary(
            next: Occurrence(
                id: "o-\(localDate)",
                reminderID: "r1",
                localDate: localDate,
                localTime: "09:00",
                timeZone: "Europe/Berlin",
                timeZoneMode: .fixed,
                prewarningMinutes: nil,
                scheduledAt: nil,
                status: .pending,
                completedAt: nil,
                snoozedUntil: nil,
                revision: 1,
                createdAt: Date(),
                updatedAt: Date()
            ),
            hasAny: true
        )
    }
}
