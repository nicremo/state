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

        for rawLine in source.replacingOccurrences(of: "\r\n", with: "\n").components(separatedBy: "\n") {
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
