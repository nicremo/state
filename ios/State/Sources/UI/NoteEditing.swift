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
        for (open, done) in [("- [ ] ", "- [x] "), ("* [ ] ", "* [x] ")] {
            if rest.hasPrefix(open) {
                lines[index] = indentation + done + rest.dropFirst(open.count)
                return lines.joined(separator: "\n")
            }
            if rest.hasPrefix(done) || rest.hasPrefix(done.replacingOccurrences(of: "x", with: "X")) {
                lines[index] = indentation + open + rest.dropFirst(done.count)
                return lines.joined(separator: "\n")
            }
        }
        return document
    }

    /// Puts a block marker such as `# ` or `- [ ] ` at the start of the line
    /// that holds the cursor. Returns the new text and the new cursor.
    static func insertLinePrefix(_ prefix: String, in text: String, at range: Range<String.Index>) -> (String, String.Index) {
        let lineStart = text[..<range.lowerBound].lastIndex(of: "\n").map { text.index(after: $0) } ?? text.startIndex
        var result = text
        result.insert(contentsOf: prefix, at: lineStart)
        let offset = text.distance(from: text.startIndex, to: range.lowerBound) + prefix.count
        return (result, result.index(result.startIndex, offsetBy: offset))
    }

    /// Surrounds the selection with an inline marker such as `**`. An empty
    /// selection leaves the cursor between the two markers.
    static func wrap(_ marker: String, in text: String, range: Range<String.Index>) -> (String, String.Index) {
        let selected = text[range]
        var result = text
        result.replaceSubrange(range, with: marker + selected + marker)
        let offset = text.distance(from: text.startIndex, to: range.lowerBound) + marker.count + selected.count
        return (result, result.index(result.startIndex, offsetBy: offset))
    }

    /// Inserts a divider on its own line after the cursor's line.
    static func insertDivider(in text: String, at range: Range<String.Index>) -> (String, String.Index) {
        let lineEnd = text[range.upperBound...].firstIndex(of: "\n") ?? text.endIndex
        let insertion = "\n\n---\n"
        var result = text
        result.insert(contentsOf: insertion, at: lineEnd)
        let offset = text.distance(from: text.startIndex, to: lineEnd) + insertion.count
        return (result, result.index(result.startIndex, offsetBy: offset))
    }
}
