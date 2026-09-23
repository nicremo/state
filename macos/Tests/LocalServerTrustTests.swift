import Foundation
import Testing
@testable import StateLocalTransport

struct LocalServerTrustTests {
    @Test func rejectsMalformedPinsAndInsecureOrigins() throws {
        let valid = String(repeating: "ab", count: 32)
        #expect(LocalServerTrust.isValidFingerprint(valid))
        #expect(!LocalServerTrust.isValidFingerprint(String(repeating: "z", count: 64)))
        #expect(!LocalServerTrust.isValidFingerprint(""))
        #expect(throws: (any Error).self) {
            try LocalServerTrust(serverURL: URL(string: "http://mac.local:9847")!, fingerprint: valid)
        }
        #expect(throws: (any Error).self) {
            try LocalServerTrust(serverURL: URL(string: "https://mac.local:9847")!, fingerprint: "bad")
        }
        #expect(throws: (any Error).self) {
            try LocalServerTrust(serverURL: URL(string: "https://user@mac.local")!, fingerprint: valid)
        }
    }

    /// The integration script supplies a fresh test server. No persisted user
    /// credential or application database is consulted.
    @Test func liveServerTrustMatchesOnlyScannedCertificate() async throws {
        guard let origin = ProcessInfo.processInfo.environment["STATE_TEST_ORIGIN"],
              let pin = ProcessInfo.processInfo.environment["STATE_TEST_PIN"],
              let url = URL(string: origin) else { return }
        let endpoint = url.appendingPathComponent("health/ready")
        let valid = try LocalServerTrust.session(serverURL: url, fingerprint: pin)
        defer { valid.invalidateAndCancel() }
        let (_, response) = try await valid.data(from: endpoint)
        #expect((response as? HTTPURLResponse)?.statusCode == 200)

        let invalid = try LocalServerTrust.session(serverURL: url, fingerprint: String(repeating: "0", count: 64))
        defer { invalid.invalidateAndCancel() }
        do {
            _ = try await invalid.data(from: endpoint)
            Issue.record("An untrusted certificate was accepted")
        } catch { #expect(error is URLError) }

        let wrongOrigin = try LocalServerTrust.session(serverURL: URL(string: "https://different.local:9847")!, fingerprint: pin)
        defer { wrongOrigin.invalidateAndCancel() }
        do {
            _ = try await wrongOrigin.data(from: endpoint)
            Issue.record("A different origin was accepted")
        } catch { #expect(error is URLError) }
    }
}
