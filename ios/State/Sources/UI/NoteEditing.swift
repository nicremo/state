import Foundation

/// Plain text operations behind the note editor's format bar and the tappable
/// checklist. They work on the Markdown source, which is the note itself.
enum NoteEditing {
    /// Flips `- [ ]` and `- [x]` on one line. Any other line stays untouched.
    static func toggleTask(atLine index: Int, in document: String) -> String {
        var lines = document.components(separatedBy: "\n")
        guard lines.indices.contains(index) else { return document }
        let line = lines[index]
        let indentation = line.prefix { $0 == " " || $0 == "\t" }
        let rest = line.dropFirst(indentation.count)
        // Matched without the trailing space, so an empty item "- [ ]" flips too.
        let trailing = rest.hasSuffix("\r") ? "\r" : ""
        let body = trailing.isEmpty ? Substring(rest) : rest.dropLast()
        for (open, done) in [("- [ ]", "- [x]"), ("* [ ]", "* [x]")] {
            let doneUpper = done.replacingOccurrences(of: "x", with: "X")
            for (from, to) in [(open, done), (done, open), (doneUpper, open)]
                where body == from || body.hasPrefix(from + " ") {
                lines[index] = indentation + to + body.dropFirst(from.count) + trailing
                return lines.joined(separator: "\n")
            }
        }
        return document
    }

    /// Puts a block marker such as `## ` or `- [ ] ` at the start of the line
    /// that holds the cursor, or takes it away when the line already has it.
    /// Returns the new text and the new cursor.
    static func toggleLinePrefix(_ prefix: String, in text: String, at range: Range<String.Index>) -> (String, String.Index) {
        let lineStart = text[..<range.lowerBound].lastIndex(where: \.isNewline).map { text.index(after: $0) } ?? text.startIndex
        let cursorOffset = text.distance(from: text.startIndex, to: range.lowerBound)
        var result = text
        if text[lineStart...].hasPrefix(prefix) {
            let end = text.index(lineStart, offsetBy: prefix.count)
            result.removeSubrange(lineStart..<end)
            let offset = max(text.distance(from: text.startIndex, to: lineStart), cursorOffset - prefix.count)
            return (result, result.index(result.startIndex, offsetBy: offset))
        }
        result.insert(contentsOf: prefix, at: lineStart)
        return (result, result.index(result.startIndex, offsetBy: cursorOffset + prefix.count))
    }

    /// Surrounds the selection with an inline marker such as `**`. The
    /// selection stays selected-width: the cursor lands after the closing
    /// marker, or between the markers when nothing was selected.
    static func wrap(_ marker: String, in text: String, range: Range<String.Index>) -> (String, String.Index) {
        let selected = text[range]
        var result = text
        result.replaceSubrange(range, with: marker + selected + marker)
        let start = text.distance(from: text.startIndex, to: range.lowerBound)
        let offset = selected.isEmpty ? start + marker.count : start + marker.count * 2 + selected.count
        return (result, result.index(result.startIndex, offsetBy: offset))
    }

    /// Inserts a divider on its own line after the cursor's line. In an empty
    /// note it simply becomes the first line.
    static func insertDivider(in text: String, at range: Range<String.Index>) -> (String, String.Index) {
        if text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            let result = "---\n"
            return (result, result.endIndex)
        }
        let lineEnd = text[range.upperBound...].firstIndex(where: \.isNewline) ?? text.endIndex
        let insertion = "\n\n---\n"
        var result = text
        result.insert(contentsOf: insertion, at: lineEnd)
        let offset = text.distance(from: text.startIndex, to: lineEnd) + insertion.count
        return (result, result.index(result.startIndex, offsetBy: offset))
    }

    /// Sets the paragraph style of the cursor's line: 0 is body text, 1 the
    /// title, 2 a heading, 3 a subheading, as the "Aa" menu of iPhone Notes.
    /// An existing heading marker is replaced, not stacked.
    static func setParagraphStyle(level: Int, in text: String, at range: Range<String.Index>) -> (String, String.Index) {
        let lineStart = text[..<range.lowerBound].lastIndex(where: \.isNewline).map { text.index(after: $0) } ?? text.startIndex
        let cursorOffset = text.distance(from: text.startIndex, to: range.lowerBound)
        let lineStartOffset = text.distance(from: text.startIndex, to: lineStart)
        let line = text[lineStart...]
        let hashes = line.prefix { $0 == "#" }.count
        var oldMarker = 0
        if (1...6).contains(hashes) {
            let rest = line.dropFirst(hashes)
            if rest.first == " " { oldMarker = hashes + 1 } else if rest.isEmpty || rest.first?.isNewline == true { oldMarker = hashes }
        }
        let newMarker = level > 0 ? String(repeating: "#", count: level) + " " : ""
        var result = text
        let markerEnd = text.index(lineStart, offsetBy: oldMarker)
        result.replaceSubrange(lineStart..<markerEnd, with: newMarker)
        let offset = max(lineStartOffset + newMarker.count, cursorOffset - oldMarker + newMarker.count)
        return (result, result.index(result.startIndex, offsetBy: min(offset, result.count)))
    }

    /// Inserts a two column table after the cursor's line, with the cursor
    /// in its first body cell.
    static func insertTable(in text: String, at range: Range<String.Index>, columns: [String]) -> (String, String.Index) {
        let header = "| " + columns.joined(separator: " | ") + " |"
        let separator = "| " + columns.map { _ in "---" }.joined(separator: " | ") + " |"
        let body = "|" + columns.map { _ in "  |" }.joined()
        let lineEnd = text[range.upperBound...].firstIndex(where: \.isNewline) ?? text.endIndex
        let lead = text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "" : "\n\n"
        let insertion = lead + header + "\n" + separator + "\n" + body + "\n"
        var result = text
        result.insert(contentsOf: insertion, at: lineEnd)
        let offset = text.distance(from: text.startIndex, to: lineEnd) + lead.count + header.count + separator.count + 2 + 2
        return (result, result.index(result.startIndex, offsetBy: offset))
    }
}
