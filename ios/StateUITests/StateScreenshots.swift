import XCTest

@MainActor
final class StateScreenshots: XCTestCase {
    func testAppStoreScreenshots() {
        continueAfterFailure = false
        let app = XCUIApplication()
        setupSnapshot(app)
        // Skips the launch animation and the first run introduction, so every
        // screenshot run starts from the same screen.
        app.launchArguments.append("-stateUITesting")
        app.launch()
        let demoButton = app.buttons["explore-demo"]
        XCTAssertTrue(demoButton.waitForExistence(timeout: 10))
        demoButton.tap()
        XCTAssertTrue(
            app.descendants(matching: .any)["reminder-01989f00-0000-7000-8000-000000000010"]
                .waitForExistence(timeout: 10)
        )
        XCTAssertTrue(sectionEntry(app, 0).waitForExistence(timeout: 10), app.debugDescription)
        snapshot("01-today")

        sectionEntry(app, 1).tap()
        snapshot("02-planned")

        sectionEntry(app, 2).tap()
        snapshot("03-activity")

        sectionEntry(app, 3).tap()
        snapshot("04-settings")
    }

    /// The iPhone shows the sections in a tab bar, the iPad and the Mac in the
    /// sidebar. Their titles are localized and every layout keeps the same
    /// order, so this picks the entry by position instead of by name. The
    /// reminder above only exists once the app is laid out, which makes telling
    /// the layouts apart here reliable.
    private func sectionEntry(_ app: XCUIApplication, _ index: Int) -> XCUIElement {
        let tabBar = app.tabBars.firstMatch
        if tabBar.exists {
            return tabBar.buttons.element(boundBy: index)
        }
        let sidebar = app.collectionViews["Sidebar"].cells
        if sidebar.firstMatch.exists {
            return sidebar.element(boundBy: index)
        }
        // A regular width iPad without the split layout falls back to the tab
        // view, and iPadOS exposes its tabs as plain buttons named after the
        // symbol of their icon.
        return app.buttons[Self.wideTabIdentifiers[index]].firstMatch
    }

    private static let wideTabIdentifiers = [
        "sun.max.fill",
        "calendar",
        "clock.arrow.circlepath",
        "gearshape"
    ]
}
