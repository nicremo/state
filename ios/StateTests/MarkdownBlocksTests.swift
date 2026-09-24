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

/// Notes add checklists and dividers, and a tapped checklist item must flip
/// exactly its own line of the document.
final class NoteMarkdownTests: XCTestCase {
    func testChecklistItemsAndDividersAreBlocks() {
        XCTAssertEqual(MarkdownBlocks.parse("- [ ] offen\n- [x] erledigt\n---\n- normal"), [
            .task(checked: false, text: "offen", line: 0),
            .task(checked: true, text: "erledigt", line: 1),
            .divider,
            .bullet("normal"),
        ])
    }

    func testTogglingATaskFlipsOnlyItsLine() {
        let document = "# Liste\n- [ ] Milch\n- [x] Brot\n- [ ] Milch"
        XCTAssertEqual(NoteEditing.toggleTask(atLine: 1, in: document), "# Liste\n- [x] Milch\n- [x] Brot\n- [ ] Milch")
        XCTAssertEqual(NoteEditing.toggleTask(atLine: 2, in: document), "# Liste\n- [ ] Milch\n- [ ] Brot\n- [ ] Milch")
        XCTAssertEqual(NoteEditing.toggleTask(atLine: 0, in: document), document)
        XCTAssertEqual(NoteEditing.toggleTask(atLine: 9, in: document), document)
    }

    func testLinePrefixGoesToTheStartOfTheCurrentLineAndToggles() {
        let text = "Erste\nZweite Zeile"
        let cursor = text.range(of: "Zeile")!.lowerBound
        let (added, addedCursor) = NoteEditing.toggleLinePrefix("- [ ] ", in: text, at: cursor..<cursor)
        XCTAssertEqual(added, "Erste\n- [ ] Zweite Zeile")
        let (removed, _) = NoteEditing.toggleLinePrefix("- [ ] ", in: added, at: addedCursor..<addedCursor)
        XCTAssertEqual(removed, text)
    }

    func testLinePrefixFindsCRLFLineStarts() {
        let text = "Erste\r\nZweite"
        let cursor = text.range(of: "Zweite")!.lowerBound
        let (result, _) = NoteEditing.toggleLinePrefix("- ", in: text, at: cursor..<cursor)
        XCTAssertEqual(result, "Erste\r\n- Zweite")
    }

    func testEmptyChecklistItemIsATask() {
        XCTAssertEqual(MarkdownBlocks.parse("- [ ] \n* [X] fertig"), [
            .task(checked: false, text: "", line: 0),
            .task(checked: true, text: "fertig", line: 1),
        ])
    }

    func testDividerInAnEmptyNoteIsTheFirstLine() {
        let (result, _) = NoteEditing.insertDivider(in: "", at: "".startIndex..<"".endIndex)
        XCTAssertEqual(result, "---\n")
    }

    func testReaderBlanksOnlyATitleLine() {
        let fenced = Note.local(id: "n", title: "", document: "```\nlet a = 1\n```\n- [ ] echt", at: Date())
        XCTAssertEqual(NoteDetailView.body(of: fenced), fenced.document, "a fence is structure, not the title")
        let titled = Note.local(id: "t", title: "", document: "# Einkauf\n- [ ] Milch", at: Date())
        XCTAssertEqual(NoteDetailView.body(of: titled), "\n- [ ] Milch")
    }

    func testWrapSurroundsTheSelection() {
        let text = "ein wichtiges Wort"
        let range = text.range(of: "wichtiges")!
        let (result, _) = NoteEditing.wrap("**", in: text, range: range)
        XCTAssertEqual(result, "ein **wichtiges** Wort")
    }

    func testAnEmptyChecklistItemCanBeTicked() {
        XCTAssertEqual(NoteEditing.toggleTask(atLine: 1, in: "Liste\n- [ ]"), "Liste\n- [x]")
        XCTAssertEqual(NoteEditing.toggleTask(atLine: 0, in: "- [X] fertig"), "- [ ] fertig")
        XCTAssertEqual(NoteEditing.toggleTask(atLine: 0, in: "- [ ] a\r\nb"), "- [x] a\r\nb")
    }

    func testTildeFencesKeepChecklistsInsideAsCode() {
        XCTAssertEqual(MarkdownBlocks.parse("~~~\n- [ ] kein Häkchen\n~~~"), [.code("- [ ] kein Häkchen")])
    }
}
