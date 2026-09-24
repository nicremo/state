import Foundation

/// A block of Markdown as the reminder description needs it. SwiftUI `Text`
/// renders inline Markdown only and silently drops block structure, so
/// headings, paragraphs and lists ran together into one wall of text. The
/// description is split into blocks first; each block's inline Markdown is
/// then rendered on its own.
enum MarkdownBlock: Equatable {
    case heading(level: Int, text: String)
    case paragraph(String)
    case bullet(String)
    case numbered(marker: String, text: String)
    case code(String)
    /// A checklist item. `line` is its index in the source, so a tap can flip
    /// exactly this line of the document.
    case task(checked: Bool, text: String, line: Int)
    case divider
}

enum MarkdownBlocks {
    static func parse(_ source: String) -> [MarkdownBlock] {
        var blocks: [MarkdownBlock] = []
        var paragraph: [String] = []
        var code: [String]?

        func flushParagraph() {
            let text = paragraph.joined(separator: " ").trimmingCharacters(in: .whitespaces)
            if !text.isEmpty { blocks.append(.paragraph(text)) }
            paragraph = []
        }

        for (index, rawLine) in source.replacingOccurrences(of: "\r\n", with: "\n").components(separatedBy: "\n").enumerated() {
            let line = rawLine.trimmingCharacters(in: .whitespaces)

            if line.hasPrefix("```") {
                if let lines = code {
                    blocks.append(.code(lines.joined(separator: "\n")))
                    code = nil
                } else {
                    flushParagraph()
                    code = []
                }
                continue
            }
            if code != nil {
                code?.append(rawLine)
                continue
            }

            if line.isEmpty {
                flushParagraph()
            } else if let heading = heading(line) {
                flushParagraph()
                blocks.append(heading)
            } else if ["---", "***", "___"].contains(line) {
                flushParagraph()
                blocks.append(.divider)
            } else if let item = task(line, index: index) {
                flushParagraph()
                blocks.append(item)
            } else if let item = bullet(line) {
                flushParagraph()
                blocks.append(.bullet(item))
            } else if let item = numbered(line) {
                flushParagraph()
                blocks.append(item)
            } else {
                paragraph.append(line)
            }
        }
        if let lines = code, !lines.isEmpty {
            blocks.append(.code(lines.joined(separator: "\n")))
        }
        flushParagraph()
        return blocks
    }

    private static func heading(_ line: String) -> MarkdownBlock? {
        let hashes = line.prefix { $0 == "#" }.count
        guard (1...6).contains(hashes) else { return nil }
        let rest = line.dropFirst(hashes)
        guard rest.first == " " else { return nil }
        let text = rest.trimmingCharacters(in: .whitespaces)
        return text.isEmpty ? nil : .heading(level: hashes, text: text)
    }

    private static let taskMarkers: [(String, Bool)] = [
        ("- [ ]", false), ("- [x]", true), ("- [X]", true),
        ("* [ ]", false), ("* [x]", true), ("* [X]", true),
    ]

    /// A checklist item, also an empty one: the format bar inserts "- [ ] "
    /// and the trailing space is gone once the line is trimmed.
    private static func task(_ line: String, index: Int) -> MarkdownBlock? {
        for (marker, checked) in taskMarkers where line == marker || line.hasPrefix(marker + " ") {
            let text = line.dropFirst(marker.count).trimmingCharacters(in: .whitespaces)
            return .task(checked: checked, text: text, line: index)
        }
        return nil
    }

    private static func bullet(_ line: String) -> String? {
        for marker in ["- ", "* ", "+ "] where line.hasPrefix(marker) {
            let text = line.dropFirst(marker.count).trimmingCharacters(in: .whitespaces)
            return text.isEmpty ? nil : text
        }
        return nil
    }

    private static func numbered(_ line: String) -> MarkdownBlock? {
        let digits = line.prefix { $0.isNumber }
        guard !digits.isEmpty, digits.count <= 3 else { return nil }
        let rest = line.dropFirst(digits.count)
        guard let delimiter = rest.first, delimiter == "." || delimiter == ")",
              rest.dropFirst().first == " " else { return nil }
        let text = rest.dropFirst(2).trimmingCharacters(in: .whitespaces)
        return text.isEmpty ? nil : .numbered(marker: "\(digits).", text: text)
    }
}
