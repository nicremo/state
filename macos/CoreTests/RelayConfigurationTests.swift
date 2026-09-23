import Foundation
import Testing
@testable import StateServerCore

struct RelayConfigurationTests {
    @Test func acceptsEmptyInputAndAbsoluteHTTPSAddresses() {
        #expect(DesktopRelay.isValid(""))
        #expect(DesktopRelay.isValid("   "))
        #expect(DesktopRelay.isValid("https://relay.example.com"))
        #expect(DesktopRelay.isValid("https://relay.example.com:8443"))
        #expect(DesktopRelay.isValid("https://relay.example.com/push"))
        #expect(!DesktopRelay.isValid("http://relay.example.com"))
        #expect(!DesktopRelay.isValid("https://user@relay.example.com"))
        #expect(!DesktopRelay.isValid("https://user:pw@relay.example.com"))
        #expect(!DesktopRelay.isValid("https://relay.example.com/?x=1"))
        #expect(!DesktopRelay.isValid("https://relay.example.com/#frag"))
        #expect(!DesktopRelay.isValid("relay.example.com"))
        #expect(!DesktopRelay.isValid("not a url"))
    }

    @Test func normalizesValidAddressesAndDropsEverythingElse() {
        #expect(DesktopRelay.normalized("  https://relay.example.com ") == "https://relay.example.com")
        #expect(DesktopRelay.normalized("https://relay.example.com/push") == "https://relay.example.com/push")
        #expect(DesktopRelay.normalized("") == nil)
        #expect(DesktopRelay.normalized("   ") == nil)
        #expect(DesktopRelay.normalized("http://relay.example.com") == nil)
    }

    @Test func storesTheAddressWithTheDesktopKeyAndClearsItOnEmptyInput() {
        let suite = "state.tests.desktop-relay"
        let defaults = UserDefaults(suiteName: suite)!
        defaults.removePersistentDomain(forName: suite)
        defer { defaults.removePersistentDomain(forName: suite) }

        #expect(DesktopRelay.stored(in: defaults).isEmpty)
        #expect(DesktopRelay.store(" https://relay.example.com ", in: defaults))
        #expect(defaults.string(forKey: "state.desktop.relay-url") == "https://relay.example.com")
        #expect(DesktopRelay.stored(in: defaults) == "https://relay.example.com")

        #expect(!DesktopRelay.store("http://relay.example.com", in: defaults))
        #expect(DesktopRelay.stored(in: defaults) == "https://relay.example.com")

        #expect(DesktopRelay.store("", in: defaults))
        #expect(defaults.string(forKey: "state.desktop.relay-url") == nil)
        #expect(DesktopRelay.stored(in: defaults).isEmpty)
    }

    @Test func ignoresAnInvalidStoredAddress() {
        let suite = "state.tests.desktop-relay-invalid"
        let defaults = UserDefaults(suiteName: suite)!
        defaults.removePersistentDomain(forName: suite)
        defer { defaults.removePersistentDomain(forName: suite) }

        defaults.set("http://relay.example.com", forKey: "state.desktop.relay-url")
        #expect(DesktopRelay.stored(in: defaults).isEmpty)
    }

    @Test func launchArgumentsOnlyCarryAConfiguredRelay() {
        let without = DesktopRelay.launchArguments(dataDirectory: "/tmp/state", host: "mac.local", relayURL: "")
        #expect(without == ["desktop", "--data", "/tmp/state", "--host", "mac.local"])

        let with = DesktopRelay.launchArguments(
            dataDirectory: "/tmp/state",
            host: "mac.local",
            relayURL: "https://relay.example.com"
        )
        #expect(with == ["desktop", "--data", "/tmp/state", "--host", "mac.local", "--relay-url", "https://relay.example.com"])

        let invalid = DesktopRelay.launchArguments(dataDirectory: "/tmp/state", host: "mac.local", relayURL: "http://relay.example.com")
        #expect(invalid == ["desktop", "--data", "/tmp/state", "--host", "mac.local"])
    }
}
