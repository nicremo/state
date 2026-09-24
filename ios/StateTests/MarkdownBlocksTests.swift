import XCTest
@testable import State

/// Agents write descriptions as sectioned Markdown. The detail view must keep
/// those sections apart instead of running them into one paragraph.
final class MarkdownBlocksTests: XCTestCase {
    func testAgentDescriptionKeepsItsSections() {
        let source = """
        ## Objective
        Monatsreport bauen.

        ## Procedure and references
        Anleitung: reporting-system/CLAUDE.md
        1. Export ziehen.
        2. `python3 karla_report.py prepare`
        - kein Versand
        """
        XCTAssertEqual(MarkdownBlocks.parse(source), [
            .heading(level: 2, text: "Objective"),
            .paragraph("Monatsreport bauen."),
            .heading(level: 2, text: "Procedure and references"),
            .paragraph("Anleitung: reporting-system/CLAUDE.md"),
            .numbered(marker: "1.", text: "Export ziehen."),
            .numbered(marker: "2.", text: "`python3 karla_report.py prepare`"),
            .bullet("kein Versand"),
        ])
    }

    func testSoftLineBreaksJoinIntoOneParagraph() {
        XCTAssertEqual(MarkdownBlocks.parse("eins\nzwei\n\ndrei"), [
            .paragraph("eins zwei"),
            .paragraph("drei"),
        ])
    }

    func testCodeFenceKeepsLinesVerbatim() {
        XCTAssertEqual(MarkdownBlocks.parse("```bash\n# not a heading\n  two\n```"), [
            .code("# not a heading\n  two"),
        ])
    }

    func testPlainTextStaysOneParagraph() {
        XCTAssertEqual(MarkdownBlocks.parse("Nur ein Satz."), [.paragraph("Nur ein Satz.")])
    }

    func testHashWithoutSpaceIsNotAHeading() {
        XCTAssertEqual(MarkdownBlocks.parse("#hashtag"), [.paragraph("#hashtag")])
    }
}
