import Foundation
import XCTest
@testable import State

final class LocalPairingTests: XCTestCase {
    func testQRPreservesLocalCertificateIdentity() throws {
        let pin = String(repeating: "ab", count: 32)
        let payload = try XCTUnwrap(PairingPayload(value: "state://pair?server=https%3A%2F%2Fmac.local%3A9847&code=sample&fingerprint=\(pin)"))
        XCTAssertEqual(payload.serverURL.absoluteString, "https://mac.local:9847")
        XCTAssertEqual(payload.certificateFingerprint, pin)
        XCTAssertEqual(payload.pairingCode, "sample")
    }

    func testRejectsInvalidPinsAndKeepsLegacyQRCompatible() {
        XCTAssertNil(PairingPayload(value: "state://pair?server=https://mac.local&code=sample&fingerprint=bad"))
        XCTAssertNil(PairingPayload(value: "state://pair?server=http://mac.local&code=sample&fingerprint=\(String(repeating: "a", count: 64))"))
        let legacy = PairingPayload(value: "state://pair?server=https://state.example.com&code=sample")
        XCTAssertNotNil(legacy)
        XCTAssertNil(legacy?.certificateFingerprint)
    }
}
