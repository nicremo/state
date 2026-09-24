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

    /// Whether every search term occurs in the title, summary or text.
    func matches(_ query: String) -> Bool {
        let haystack = "\(title)\n\(summary)\n\(plainText)".lowercased()
        return query.lowercased().split(whereSeparator: \.isWhitespace).allSatisfy { haystack.contains($0) }
    }
}

/// Mirrors `NotePlainText`, `DeriveNoteTitle` and `DeriveNoteSummary` in
/// `internal/state/notes.go`.
enum NoteText {
    static let titleLimit = 200
    static let summaryLimit = 160

    static func plainText(_ document: String) -> String {
        var output: [String] = []
        var inCode = false
        for raw in document.replacingOccurrences(of: "\r\n", with: "\n").components(separatedBy: "\n") {
            var line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("```") {
                inCode.toggle()
                continue
            }
            if inCode {
                output.append(raw)
                continue
            }
            if ["---", "***", "___"].contains(line) { continue }
            if let range = line.range(of: #"^#{1,6}\s+"#, options: .regularExpression) {
                line.removeSubrange(range)
            }
            for prefix in ["- [ ] ", "- [x] ", "- [X] ", "- ", "* ", "+ ", "> "] where line.hasPrefix(prefix) {
                line.removeFirst(prefix.count)
                break
            }
            if let range = line.range(of: #"^\d{1,3}[.)]\s+"#, options: .regularExpression) {
                line.removeSubrange(range)
            }
            for marker in ["**", "__", "~~", "`", "*"] {
                line = line.replacingOccurrences(of: marker, with: "")
            }
            output.append(line.trimmingCharacters(in: .whitespaces))
        }
        return output.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
    }

    static func title(_ document: String) -> String {
        guard let first = lines(plainText(document)).first else { return "" }
        return truncate(first, to: titleLimit)
    }

    static func summary(_ document: String) -> String {
        summarize(Array(lines(plainText(document)).dropFirst()))
    }

    static func summarize(_ lines: [String]) -> String {
        truncate(lines.joined(separator: " "), to: summaryLimit)
    }

    static func lines(_ text: String) -> [String] {
        text.components(separatedBy: "\n")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
    }

    private static func truncate(_ text: String, to limit: Int) -> String {
        guard text.count > limit else { return text }
        return String(text.prefix(limit - 1)).trimmingCharacters(in: .whitespaces) + "…"
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
