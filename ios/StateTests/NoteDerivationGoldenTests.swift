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
        XCTAssertGreaterThan(cases.count, 3000)
        // Compared by Unicode scalars: String == treats canonically
        // equivalent text as equal and would hide real differences.
        func scalars(_ text: String) -> [UInt32] { text.unicodeScalars.map(\.value) }
        var mismatches: [String] = []
        for (index, testCase) in cases.enumerated() {
            if scalars(NoteText.plainText(testCase.document)) != scalars(testCase.plain_text) { mismatches.append("plain text, case \(index)") }
            if scalars(NoteText.title(testCase.document)) != scalars(testCase.title) { mismatches.append("title, case \(index)") }
            if scalars(NoteText.summary(testCase.document)) != scalars(testCase.summary) { mismatches.append("summary, case \(index)") }
        }
        XCTAssertTrue(mismatches.isEmpty, "\(mismatches.count) mismatches, first: \(mismatches.prefix(10))")
    }
}
