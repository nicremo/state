import Foundation
import XCTest
@testable import State

/// The server and the app derive titles, summaries and search text from the
/// same Markdown. Both check themselves against the golden file the Go tests
/// write, internal/state/testdata/note_derivation.json.
final class NoteDerivationGoldenTests: XCTestCase {
    private struct Case: Decodable {
        let document: String
        let plain_text: String
        let title: String
        let summary: String
    }

    func testDerivationMatchesTheServer() throws {
        let golden = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .appending(path: "internal/state/testdata/note_derivation.json")
        let cases = try JSONDecoder().decode([Case].self, from: Data(contentsOf: golden))
        XCTAssertGreaterThan(cases.count, 10)
        for (index, testCase) in cases.enumerated() {
            XCTAssertEqual(NoteText.plainText(testCase.document), testCase.plain_text, "plain text, case \(index)")
            XCTAssertEqual(NoteText.title(testCase.document), testCase.title, "title, case \(index)")
            XCTAssertEqual(NoteText.summary(testCase.document), testCase.summary, "summary, case \(index)")
        }
    }
}
