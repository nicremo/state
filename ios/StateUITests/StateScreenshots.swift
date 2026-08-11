import XCTest

@MainActor
final class StateScreenshots: XCTestCase {
    func testAppStoreScreenshots() {
        continueAfterFailure = false
        let app = XCUIApplication()
        setupSnapshot(app)
        app.launch()
        let demoButton = app.buttons["explore-demo"]
        XCTAssertTrue(demoButton.waitForExistence(timeout: 10))
        demoButton.tap()
        XCTAssertTrue(app.tabBars.firstMatch.waitForExistence(timeout: 10))
        XCTAssertTrue(
            app.descendants(matching: .any)["reminder-01989f00-0000-7000-8000-000000000010"]
                .waitForExistence(timeout: 10)
        )
        snapshot("01-today")

        app.tabBars.buttons.element(boundBy: 1).tap()
        snapshot("02-planned")

        app.tabBars.buttons.element(boundBy: 2).tap()
        snapshot("03-activity")

        app.tabBars.buttons.element(boundBy: 3).tap()
        snapshot("04-settings")
    }
}
