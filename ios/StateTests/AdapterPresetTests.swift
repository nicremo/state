import XCTest
@testable import State

/// A policy only runs when its adapter name matches an adapter the runner
/// ships (DefaultAdapters in internal/runner/adapters.go), so the picker must
/// offer exactly those names and not the harness pairing labels.
final class AdapterPresetTests: XCTestCase {
    func testAdapterPresetsMatchTheRunnerRegistry() {
        XCTAssertEqual(
            Set(HarnessCatalog.adapterPresets.map(\.id)),
            ["codex", "claude-code", "opencode", "pi-agent", "deepseek-harness"]
        )
    }

    func testAdapterPresetsAreValidLabels() {
        for preset in HarnessCatalog.adapterPresets {
            XCTAssertTrue(HarnessCatalog.isValid(preset.id), preset.id)
        }
    }

    func testHarnessPresetsOfferDeepSeekHarness() {
        XCTAssertTrue(HarnessCatalog.presets.contains { $0.id == "deepseek-harness" })
    }
}
