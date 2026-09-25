import SwiftUI
import XCTest
@testable import State

/// The formatting surface of iPhone Notes, stored as Markdown: tables,
/// underline, highlight, paragraph styles and collapsible sections.
final class NoteFormattingTests: XCTestCase {
    func testTableBecomesOneBlock() {
        let source = "Vorher\n| A | B |\n|---|:-:|\n| 1 | **2** |\n| 3 |\nNachher"
        XCTAssertEqual(MarkdownBlocks.parse(source), [
            .paragraph("Vorher"),
            .table(header: ["A", "B"], rows: [["1", "**2**"], ["3"]]),
            .paragraph("Nachher"),
        ])
    }

    func testPipeWithoutSeparatorStaysText() {
        XCTAssertEqual(MarkdownBlocks.parse("| nur | Text |\nweiter"), [
            .paragraph("| nur | Text | weiter"),
        ])
    }

    func testUnderlineAndHighlightBecomeAttributes() {
        let text = InlineMarkup.attributed("a ++unter++ und ==hell== und **fett** 2 ++ 3")
        XCTAssertEqual(String(text.characters), "a unter und hell und fett 2 ++ 3")
        let underlined = text.runs.filter { $0.underlineStyle != nil }.map { String(text[$0.range].characters) }
        let highlighted = text.runs.filter { $0.backgroundColor != nil }.map { String(text[$0.range].characters) }
        XCTAssertEqual(underlined, ["unter"])
        XCTAssertEqual(highlighted, ["hell"])
    }

    func testCollapsedHeadingHidesItsSectionOnly() {
        let blocks = MarkdownBlocks.parse("# Titel\n## Eins\na\n### Unter\nb\n## Zwei\nc")
        // Collapse "Eins" (index 1): its paragraph and subsection disappear,
        // the next heading of the same level stays.
        let visible = MarkdownSections.visible(blocks, collapsed: [1]).map(\.offset)
        XCTAssertEqual(visible, [0, 1, 5, 6])
        XCTAssertTrue(MarkdownSections.hasContent(blocks, heading: 1))
        XCTAssertFalse(MarkdownSections.hasContent(MarkdownBlocks.parse("## Leer\n## Next"), heading: 0))
    }

    func testParagraphStyleReplacesTheExistingMarker() {
        let text = "## Alt\nzweite"
        let (styled, _) = NoteEditing.setParagraphStyle(level: 3, in: text, at: text.startIndex..<text.startIndex)
        XCTAssertEqual(styled, "### Alt\nzweite")
        let (plain, _) = NoteEditing.setParagraphStyle(level: 0, in: styled, at: styled.startIndex..<styled.startIndex)
        XCTAssertEqual(plain, "Alt\nzweite")
        let (title, cursor) = NoteEditing.setParagraphStyle(level: 1, in: "Neu", at: "Neu".endIndex..<"Neu".endIndex)
        XCTAssertEqual(title, "# Neu")
        XCTAssertEqual(title.distance(from: title.startIndex, to: cursor), 5)
    }

    func testInsertTableAddsAParsableTable() {
        let text = "Zeile"
        let (result, cursor) = NoteEditing.insertTable(in: text, at: text.endIndex..<text.endIndex, columns: ["A", "B"])
        XCTAssertEqual(result, "Zeile\n\n| A | B |\n| --- | --- |\n|  |  |\n")
        XCTAssertEqual(result.distance(from: result.startIndex, to: cursor), "Zeile\n\n| A | B |\n| --- | --- |\n| ".count)
        XCTAssertEqual(MarkdownBlocks.parse(result).last, .table(header: ["A", "B"], rows: [["", ""]]))
    }
}
