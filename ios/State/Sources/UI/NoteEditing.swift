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
}
