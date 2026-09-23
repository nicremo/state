import Foundation
import Testing
@testable import StateServerCore

struct RestartPolicyTests {
    @Test func backsOffExponentiallyUpToTheCap() {
        var policy = RestartPolicy(baseDelay: 2, maxDelay: 60, healthyAfter: 60)
        let start = Date(timeIntervalSince1970: 1_000)
        let delays = (0..<7).map { _ in policy.delayAfterExit(at: start.addingTimeInterval(1), startedAt: start) }
        #expect(delays == [2, 4, 8, 16, 32, 60, 60])
        #expect(policy.consecutiveFailures == 7)
    }

    @Test func aHealthyRunResetsTheBackoff() {
        var policy = RestartPolicy(baseDelay: 2, maxDelay: 60, healthyAfter: 60)
        let start = Date(timeIntervalSince1970: 1_000)
        _ = policy.delayAfterExit(at: start.addingTimeInterval(1), startedAt: start)
        _ = policy.delayAfterExit(at: start.addingTimeInterval(1), startedAt: start)
        let delay = policy.delayAfterExit(at: start.addingTimeInterval(120), startedAt: start)
        #expect(delay == 2)
        #expect(policy.consecutiveFailures == 1)
    }

    @Test func resetClearsFailures() {
        var policy = RestartPolicy()
        let start = Date()
        _ = policy.delayAfterExit(at: start, startedAt: start)
        policy.reset()
        #expect(policy.consecutiveFailures == 0)
    }
}
