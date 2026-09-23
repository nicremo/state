import Foundation
import XCTest
@testable import State

final class RunnerStatusTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_790_000_000)

    func testOnlineWithinTwoMinutes() {
        XCTAssertTrue(Runner.isOnline(lastSeenAt: now, now: now))
        XCTAssertTrue(Runner.isOnline(lastSeenAt: now.addingTimeInterval(-30), now: now))
        XCTAssertTrue(Runner.isOnline(lastSeenAt: now.addingTimeInterval(-119), now: now))
    }

    func testOfflineAtTwoMinutesAndLater() {
        XCTAssertFalse(Runner.isOnline(lastSeenAt: now.addingTimeInterval(-120), now: now))
        XCTAssertFalse(Runner.isOnline(lastSeenAt: now.addingTimeInterval(-121), now: now))
        XCTAssertFalse(Runner.isOnline(lastSeenAt: now.addingTimeInterval(-3600), now: now))
    }

    func testMissingTimestampIsOffline() {
        XCTAssertFalse(Runner.isOnline(lastSeenAt: nil, now: now))
    }

    func testTimestampFromTheFutureCountsAsOnline() {
        XCTAssertTrue(Runner.isOnline(lastSeenAt: now.addingTimeInterval(45), now: now))
    }

    func testRunnerInstanceUsesItsOwnTimestamp() {
        let onlineRunner = makeRunner(lastSeenAt: now.addingTimeInterval(-10))
        XCTAssertTrue(onlineRunner.isOnline(now: now))

        let staleRunner = makeRunner(lastSeenAt: now.addingTimeInterval(-300))
        XCTAssertFalse(staleRunner.isOnline(now: now))
    }

    private func makeRunner(lastSeenAt: Date) -> Runner {
        Runner(
            id: "01989f00-0000-7000-8000-0000000000aa",
            displayName: "mac-mini",
            projects: [],
            adapters: ["claude-code"],
            registeredAt: now.addingTimeInterval(-86400),
            lastSeenAt: lastSeenAt,
            revision: 1
        )
    }
}
