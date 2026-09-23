import Foundation

/// Decides how long the menu bar app waits before restarting a server
/// process that exited unexpectedly. It never gives up: a Mac server that
/// silently stays down is worse than one that keeps retrying slowly.
public struct RestartPolicy: Sendable {
    public private(set) var consecutiveFailures = 0
    private let baseDelay: Double
    private let maxDelay: Double
    private let healthyAfter: Double

    public init(baseDelay: Double = 2, maxDelay: Double = 60, healthyAfter: Double = 60) {
        self.baseDelay = baseDelay
        self.maxDelay = maxDelay
        self.healthyAfter = healthyAfter
    }

    public mutating func delayAfterExit(at now: Date, startedAt: Date) -> Double {
        if now.timeIntervalSince(startedAt) >= healthyAfter {
            consecutiveFailures = 1
        } else {
            consecutiveFailures += 1
        }
        let exponent = Double(max(consecutiveFailures - 1, 0))
        return min(maxDelay, baseDelay * pow(2, exponent))
    }

    public mutating func reset() {
        consecutiveFailures = 0
    }
}
