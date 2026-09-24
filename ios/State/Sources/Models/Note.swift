import Foundation

/// A note is unstructured knowledge next to reminders. The Markdown document
/// is canonical; title and summary are either written by a person (`user`) or
/// derived from the document (`derived`). The server owns the final values,
/// the app mirrors the derivation so an offline note already looks right.
struct Note: Codable, Hashable, Identifiable, Sendable {
    let id: String
    var title: String
    var titleSource: String
    var document: String
    var plainText: String
    var summary: String
    var summarySource: String
    var archived: Bool
    var revision: Int64
    var createdAt: Date
    var updatedAt: Date

    static let userSource = "user"
    static let derivedSource = "derived"

    /// Builds a local note the way the server would store it.
    static func local(id: String, title: String, document: String, at date: Date) -> Note {
        var note = Note(
            id: id,
            title: "",
            titleSource: derivedSource,
            document: document,
            plainText: "",
            summary: "",
            summarySource: derivedSource,
            archived: false,
            revision: 1,
            createdAt: date,
            updatedAt: date
        )
        note.apply(title: title, document: document)
        return note
    }

    /// Applies an edit: a written title stays, a derived one follows the text.
    mutating func apply(title: String?, document: String) {
        self.document = document
        plainText = NoteText.plainText(document)
        if let title {
            let trimmed = title.trimmingCharacters(in: .whitespacesAndNewlines)
            titleSource = trimmed.isEmpty ? Self.derivedSource : Self.userSource
            self.title = trimmed
        }
        if titleSource == Self.derivedSource {
            self.title = NoteText.title(document)
        }
        if summarySource == Self.derivedSource {
            summary = titleSource == Self.derivedSource
                ? NoteText.summary(document)
                : NoteText.summarize(NoteText.lines(plainText))
        }
    }

    /// Whether every search term occurs in the title, summary or text. Case
    /// and diacritics are ignored, as the server's search does: "kase" finds
    /// "Käse".
    func matches(_ query: String) -> Bool {
        let haystack = Self.searchFolded("\(title)\n\(summary)\n\(plainText)")
        return Self.searchFolded(query).split(whereSeparator: \.isWhitespace).allSatisfy { haystack.contains($0) }
    }

    private static func searchFolded(_ text: String) -> String {
        text.folding(options: [.caseInsensitive, .diacriticInsensitive], locale: nil)
    }
}

/// Mirrors `NotePlainText`, `DeriveNoteTitle` and `DeriveNoteSummary` in
/// `internal/state/notes.go`. Both sides are checked against the same golden
/// file, `internal/state/testdata/note_derivation.json`. Character classes are
/// spelled out as ASCII, because Go's RE2 treats \d, \s and \w as ASCII and
/// NSRegularExpression does not; lengths count Unicode scalars, as Go counts runes.
enum NoteText {
    static let titleLimit = 200
    static let summaryLimit = 160
    static let documentByteLimit = 262_144

    private static let headingMarker = regex(#"^#{1,6}([ \t]+|$)"#)
    private static let orderedListMarker = regex(#"^[0-9]{1,3}[.)][ \t]+"#)
    private static let emptyTaskMarkers: Set<String> = ["- [ ]", "- [x]", "- [X]", "* [ ]", "* [x]", "* [X]"]
    private static let linePrefixes = ["- [ ] ", "- [x] ", "- [X] ", "* [ ] ", "* [x] ", "* [X] ", "- ", "* ", "+ ", "> "]
    private static let emphasis: [(NSRegularExpression, String)] = [
        (regex("`([^`]+)`"), "$1"),
        (regex(#"\*\*([^ \t*](?:[^*]*[^ \t*])?)\*\*"#), "$1"),
        (regex(#"(^|[^A-Za-z0-9_])__([^ \t_](?:[^_]*[^ \t_])?)__([^A-Za-z0-9_]|$)"#), "$1$2$3"),
        (regex(#"~~([^ \t~](?:[^~]*[^ \t~])?)~~"#), "$1"),
        (regex(#"\*([^ \t*](?:[^*]*[^ \t*])?)\*"#), "$1"),
        (regex(#"(^|[^A-Za-z0-9_])_([^ \t_](?:[^_]*[^ \t_])?)_([^A-Za-z0-9_]|$)"#), "$1$2$3"),
    ]

    static func plainText(_ document: String) -> String {
        var output: [String] = []
        var fence: String?
        for raw in document.replacingOccurrences(of: "\r\n", with: "\n").components(separatedBy: "\n") {
            var line = trimSpace(raw)
            if let marker = codeFence(line), fence == nil || fence == marker {
                fence = fence == nil ? marker : nil
                continue
            }
            if fence != nil {
                output.append(trimTrailing(raw))
                continue
            }
            if ["---", "***", "___"].contains(line) { continue }
            line = replace(headingMarker, in: line, with: "")
            if emptyTaskMarkers.contains(line) { continue }
            for prefix in linePrefixes where hasScalarPrefix(line, prefix) {
                line = String(String.UnicodeScalarView(line.unicodeScalars.dropFirst(prefix.unicodeScalars.count)))
                break
            }
            line = replace(orderedListMarker, in: line, with: "")
            for (pattern, template) in emphasis {
                line = replace(pattern, in: line, with: template)
            }
            output.append(trimSpace(line))
        }
        return trimSpace(output.joined(separator: "\n"))
    }

    static func title(_ document: String) -> String {
        guard let first = lines(plainText(document)).first else { return "" }
        return truncate(singleLine(first), to: titleLimit)
    }

    static func summary(_ document: String) -> String {
        summarize(Array(lines(plainText(document)).dropFirst()))
    }

    static func summarize(_ lines: [String]) -> String {
        truncate(singleLine(lines.joined(separator: " ")), to: summaryLimit)
    }

    /// Go's singleLine: tabs and line breaks become spaces, other control
    /// characters disappear, and runs of spaces collapse like strings.Fields.
    static func singleLine(_ text: String) -> String {
        var cleaned = String.UnicodeScalarView()
        for scalar in text.unicodeScalars {
            switch scalar.value {
            case 0x09, 0x0A, 0x0D: cleaned.append(" ")
            case 0x00..<0x20, 0x7F: continue
            default: cleaned.append(scalar)
            }
        }
        var words: [String] = []
        var current = String.UnicodeScalarView()
        for scalar in cleaned {
            if isGoSpace(scalar) {
                if !current.isEmpty { words.append(String(current)); current = String.UnicodeScalarView() }
            } else {
                current.append(scalar)
            }
        }
        if !current.isEmpty { words.append(String(current)) }
        return words.joined(separator: " ")
    }

    static func lines(_ text: String) -> [String] {
        text.components(separatedBy: "\n").map(trimSpace).filter { !$0.isEmpty }
    }

    /// The title of the note that keeps a conflicting local text. It must
    /// stay within the server's title limit, or the copy could never sync.
    static func conflictCopyTitle(for title: String) -> String {
        let suffix = String(localized: "(conflict copy)")
        return clampTitle(truncate(title, to: titleLimit - suffix.unicodeScalars.count - 2) + " " + suffix)
    }

    /// Cuts a typed title to the server limit, counted as Go counts runes.
    static func clampTitle(_ title: String) -> String {
        var scalars = String.UnicodeScalarView()
        for scalar in title.unicodeScalars.prefix(titleLimit) { scalars.append(scalar) }
        return String(scalars)
    }

    static func exceedsDocumentLimit(_ document: String) -> Bool {
        document.utf8.count > documentByteLimit
    }

    private static func truncate(_ text: String, to limit: Int) -> String {
        guard text.unicodeScalars.count > limit else { return text }
        var scalars = String.UnicodeScalarView()
        for scalar in text.unicodeScalars.prefix(limit - 1) { scalars.append(scalar) }
        return trimSpace(String(scalars)) + "…"
    }

    private static func codeFence(_ line: String) -> String? {
        if hasScalarPrefix(line, "```") { return "```" }
        if hasScalarPrefix(line, "~~~") { return "~~~" }
        return nil
    }

    /// Prefix test on Unicode scalars, as Go compares bytes. Swift's hasPrefix
    /// compares characters, so "- " followed by a combining mark would not match.
    private static func hasScalarPrefix(_ text: String, _ prefix: String) -> Bool {
        text.unicodeScalars.starts(with: prefix.unicodeScalars)
    }

    /// Go's strings.TrimSpace, which trims what unicode.IsSpace reports.
    private static func trimSpace(_ text: String) -> String {
        let scalars = text.unicodeScalars
        guard let start = scalars.firstIndex(where: { !isGoSpace($0) }),
              let end = scalars.lastIndex(where: { !isGoSpace($0) }) else { return "" }
        return String(scalars[start...end])
    }

    private static func isGoSpace(_ scalar: Unicode.Scalar) -> Bool {
        switch scalar.value {
        case 0x09...0x0D, 0x20, 0x85, 0xA0, 0x1680, 0x2000...0x200A, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000:
            return true
        default:
            return false
        }
    }

    private static func trimTrailing(_ text: String) -> String {
        let scalars = text.unicodeScalars
        guard let end = scalars.lastIndex(where: { $0 != " " && $0 != "\t" }) else { return "" }
        return String(scalars[...end])
    }

    private static func regex(_ pattern: String) -> NSRegularExpression {
        try! NSRegularExpression(pattern: pattern)
    }

    private static func replace(_ pattern: NSRegularExpression, in text: String, with template: String) -> String {
        pattern.stringByReplacingMatches(in: text, range: NSRange(text.startIndex..., in: text), withTemplate: template)
    }
}

struct NoteListResponse: Decodable, Sendable {
    let notes: [Note]
}

struct CreateNoteRequest: Encodable, Sendable {
    let title: String?
    let document: String
    let clientTime: Date
    let source: String
    let clientRequestID: String
}

struct UpdateNoteRequest: Encodable, Sendable {
    let title: String?
    let document: String?
    let archived: Bool?
    let expectedRevision: Int64
    let clientTime: Date
    let source: String
    let clientRequestID: String
}
