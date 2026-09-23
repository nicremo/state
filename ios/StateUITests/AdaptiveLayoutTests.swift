import XCTest

/// Proves the layout follows the available width: tabs on a compact iPhone,
/// the three column split with a detail placeholder on a regular iPad, and the
/// same split on the Mac.
///
/// One scheme drives every destination, so this test branches on what the app
/// actually shows rather than on the device name. It therefore fails when a
/// device ends up with the wrong layout. Everything it looks for is an
/// accessibility identifier, so a German simulator does not break it.
@MainActor
final class AdaptiveLayoutTests: XCTestCase {
    func testLayoutMatchesTheAvailableWidth() {
        continueAfterFailure = false
        let app = XCUIApplication()
        app.launchArguments.append("-stateUITesting")
        app.launch()

        let demoButton = app.buttons["explore-demo"]
        XCTAssertTrue(demoButton.waitForExistence(timeout: 20))
        demoButton.tap()

        // The detail placeholder exists only in the split layout, the tab bar
        // only in the compact one.
        let placeholder = app.descendants(matching: .any)["split-detail-placeholder"]
        let tabs = app.tabBars.firstMatch
        guard placeholder.waitForExistence(timeout: 15) else {
            XCTAssertTrue(tabs.exists, "Neither the split layout nor the tab bar appeared.")
            XCTAssertEqual(tabs.buttons.count, 4, "The iPhone lost a tab.")
            return
        }

        XCTAssertTrue(app.descendants(matching: .any)["sidebar-today"].exists, "The sidebar lost Today.")
        XCTAssertTrue(app.descendants(matching: .any)["sidebar-planned"].exists, "The sidebar lost Planned.")
        XCTAssertTrue(app.descendants(matching: .any)["sidebar-activity"].exists, "The sidebar lost Activity.")
        XCTAssertTrue(app.descendants(matching: .any)["sidebar-settings"].exists, "The sidebar lost Settings.")
        XCTAssertFalse(tabs.exists, "An iPad in regular width must not show the tab bar.")
        attach(XCUIScreen.main.screenshot(), named: "wp09-split-sidebar-and-placeholder")

        // Selecting a reminder fills the detail column.
        let firstReminder = app.descendants(matching: .any)
            .matching(NSPredicate(format: "identifier BEGINSWITH 'reminder-'"))
            .firstMatch
        XCTAssertTrue(firstReminder.waitForExistence(timeout: 10))
        firstReminder.tap()
        XCTAssertTrue(
            app.descendants(matching: .any)["split-detail"].waitForExistence(timeout: 10),
            "The detail column did not show the selected reminder."
        )
        XCTAssertFalse(
            placeholder.exists,
            "The placeholder stayed visible after a reminder was selected."
        )
        attach(XCUIScreen.main.screenshot(), named: "wp09-split-detail")
    }

    private func attach(_ screenshot: XCUIScreenshot, named name: String) {
        let attachment = XCTAttachment(screenshot: screenshot)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
