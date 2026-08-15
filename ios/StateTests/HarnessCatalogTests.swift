import XCTest
@testable import State

final class HarnessCatalogTests: XCTestCase {
    // These cases mirror TestValidHarness in internal/state/harness_test.go.
    // The app must never offer a label the server would reject.
    func testAcceptsSlugLabels() {
        for harness in [
            "codex",
            "claude-code",
            "opencode",
            "pi",
            "my-own-agent",
            "agent7",
            "ab",
            "a234567890123456789012345678901b",
        ] {
            XCTAssertTrue(HarnessCatalog.isValid(harness), "expected \(harness) to be valid")
        }
    }

    func testRejectsAmbiguousLabels() {
        for harness in [
            "",
            "a",
            "Codex",
            "claude code",
            "-codex",
            "codex-",
            "codex_cli",
            "agent.one",
            "a234567890123456789012345678901bc",
        ] {
            XCTAssertFalse(HarnessCatalog.isValid(harness), "expected \(harness) to be rejected")
        }
    }

    func testNormalizeMakesOwnerInputServerReady() {
        XCTAssertEqual(HarnessCatalog.normalize("  Claude-Code \n"), "claude-code")
        XCTAssertTrue(HarnessCatalog.isValid(HarnessCatalog.normalize(" PI ")))
    }

    func testPresetsAreValidAndShippedIntegrationsAreKnown() {
        for preset in HarnessCatalog.presets {
            XCTAssertTrue(HarnessCatalog.isValid(preset.id), "preset \(preset.id) must be valid")
        }
        for harness in HarnessCatalog.shippedIntegrations {
            XCTAssertTrue(HarnessCatalog.isValid(harness))
            XCTAssertTrue(HarnessCatalog.hasShippedIntegration(harness))
        }
        XCTAssertFalse(HarnessCatalog.hasShippedIntegration("pi"))
        XCTAssertFalse(HarnessCatalog.isValid(HarnessCatalog.customTag))
    }
}
