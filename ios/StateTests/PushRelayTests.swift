import Foundation
import XCTest
@testable import State

final class PushRelayTests: XCTestCase {
    func testRelayBelongsToTheSessionAndIsOnlyDerivedForPublicHosts() throws {
        let cases: [(server: String, configured: String?, expected: String?)] = [
            ("https://state.example.com", nil, "https://relay.example.com"),
            ("https://example.com", nil, "https://relay.example.com"),
            ("https://mac.local:9847", nil, nil),
            ("https://192.168.1.20:9847", nil, nil),
            ("https://mac.local:9847", "https://relay.example.com", "https://relay.example.com"),
            ("https://state.example.com", "https://push.other.org", "https://push.other.org"),
        ]
        for item in cases {
            let serverURL = try XCTUnwrap(URL(string: item.server))
            let configured = try item.configured.map { try XCTUnwrap(URL(string: $0)) }
            let resolved = PushRegistrationService.relayURL(for: serverURL, configured: configured)
            XCTAssertEqual(resolved?.absoluteString, item.expected, "\(item.server) with \(item.configured ?? "no relay")")
        }
    }

    func testLocalAndPrivateHostsNeverGetARelay() throws {
        for server in [
            "https://Fabians-Mac.local:9847",
            "https://mac.local",
            "https://localhost:9847",
            "https://127.0.0.1:9847",
            "https://10.0.0.5:9847",
            "https://172.16.4.4:9847",
            "https://[fd00::1]:9847",
            "https://[fe80::1]:9847",
        ] {
            let serverURL = try XCTUnwrap(URL(string: server))
            XCTAssertNil(PushRegistrationService.relayURL(for: serverURL, configured: nil), server)
        }
    }

    func testAnUnusableConfiguredRelayStopsRegistrationInsteadOfDerivingOne() throws {
        let serverURL = try XCTUnwrap(URL(string: "https://state.example.com"))
        let plain = try XCTUnwrap(URL(string: "http://push.other.org"))
        XCTAssertNil(PushRegistrationService.relayURL(for: serverURL, configured: plain))
        let withCredentials = try XCTUnwrap(URL(string: "https://user@push.other.org"))
        XCTAssertNil(PushRegistrationService.relayURL(for: serverURL, configured: withCredentials))
    }

    func testRelayResolutionDoesNotTouchTheObsoleteGlobalCache() throws {
        let suite = "state.tests.relay-resolution"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defaults.removePersistentDomain(forName: suite)
        defer { defaults.removePersistentDomain(forName: suite) }
        defaults.set("https://stale.example.com", forKey: "state.relay-url")

        let serverURL = try XCTUnwrap(URL(string: "https://state.example.com"))
        _ = PushRegistrationService.relayURL(for: serverURL, configured: nil)

        XCTAssertEqual(defaults.string(forKey: "state.relay-url"), "https://stale.example.com")
    }

    func testLegacyCachedRelayIsRemovedOnce() throws {
        let suite = "state.tests.legacy-relay-cache"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defaults.removePersistentDomain(forName: suite)
        defer { defaults.removePersistentDomain(forName: suite) }
        defaults.set("https://relay.example.com", forKey: "state.relay-url")

        PushRegistrationService.removeLegacyCachedRelay(in: defaults)
        XCTAssertNil(defaults.string(forKey: "state.relay-url"))
        PushRegistrationService.removeLegacyCachedRelay(in: defaults)
        XCTAssertNil(defaults.string(forKey: "state.relay-url"))
    }

    func testPairingPayloadCarriesAnHTTPSRelay() throws {
        let payload = try XCTUnwrap(PairingPayload(
            value: "state://pair?server=https%3A%2F%2Fmac.local%3A9847&code=sample&relay=https%3A%2F%2Frelay.example.com"
        ))
        XCTAssertEqual(payload.relayURL?.absoluteString, "https://relay.example.com")
        XCTAssertEqual(payload.serverURL.absoluteString, "https://mac.local:9847")
    }

    func testPairingPayloadWithoutRelayStaysValid() throws {
        let payload = try XCTUnwrap(PairingPayload(value: "state://pair?server=https%3A%2F%2Fmac.local%3A9847&code=sample"))
        XCTAssertNil(payload.relayURL)
    }

    func testPairingPayloadRejectsAnUnusableRelay() {
        XCTAssertNil(PairingPayload(
            value: "state://pair?server=https%3A%2F%2Fmac.local%3A9847&code=sample&relay=http%3A%2F%2Frelay.example.com"
        ))
        XCTAssertNil(PairingPayload(
            value: "state://pair?server=https%3A%2F%2Fmac.local%3A9847&code=sample&relay=https%3A%2F%2Fuser%40relay.example.com"
        ))
        XCTAssertNil(PairingPayload(
            value: "state://pair?server=https%3A%2F%2Fmac.local%3A9847&code=sample&relay=not%20a%20url"
        ))
    }

    func testStoredSessionsFromBeforeTheRelayStillDecode() throws {
        let legacy = Data("""
        {"serverURL":"https://mac.local:9847","actor":{"id":"01989f00-0000-7000-8000-000000000001","kind":"owner","display_name":"Fabian"},"certificateFingerprint":"ab"}
        """.utf8)
        let session = try StateJSON.decoder.decode(ServerSession.self, from: legacy)
        XCTAssertNil(session.relayURL)
        XCTAssertEqual(session.serverURL.absoluteString, "https://mac.local:9847")
    }

    func testStoredSessionsKeepTheirRelay() throws {
        let original = ServerSession(
            serverURL: try XCTUnwrap(URL(string: "https://mac.local:9847")),
            actor: Actor(id: "01989f00-0000-7000-8000-000000000001", kind: .owner, displayName: "Fabian", harness: nil, deviceName: nil),
            certificateFingerprint: "ab",
            relayURL: URL(string: "https://relay.example.com")
        )
        let data = try StateJSON.encoder.encode(original)
        let decoded = try StateJSON.decoder.decode(ServerSession.self, from: data)
        XCTAssertEqual(decoded.relayURL?.absoluteString, "https://relay.example.com")
    }
}
