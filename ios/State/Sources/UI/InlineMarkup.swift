import SwiftUI

/// Inline Markdown plus the two marks iPhone Notes has and Markdown lacks:
/// `++underline++` and `==highlight==`. They are applied after Markdown
/// parsing, with the same pairing rule the server uses to strip them, so a
/// stray "2 ++ 3" stays as typed.
enum InlineMarkup {
    static func attributed(_ source: String) -> AttributedString {
        let options = AttributedString.MarkdownParsingOptions(interpretedSyntax: .inlineOnlyPreservingWhitespace)
        var text = (try? AttributedString(markdown: source, options: options)) ?? AttributedString(source)
        apply(marker: "++", to: &text) { $0.underlineStyle = .single }
        apply(marker: "==", to: &text) { $0.backgroundColor = StateTheme.highlight }
        return text
    }

    private static func apply(marker: String, to text: inout AttributedString, style: (inout AttributedSubstring) -> Void) {
        let escaped = NSRegularExpression.escapedPattern(for: marker)
        let character = NSRegularExpression.escapedPattern(for: String(marker.first!))
        guard let pattern = try? Regex("\(escaped)([^ \\t\(character)](?:[^\(character)]*[^ \\t\(character)])?)\(escaped)") else { return }
        // Work from the end so earlier offsets stay valid while markers go.
        let plain = String(text.characters)
        let matches = plain.matches(of: pattern).reversed()
        for match in matches {
            let start = plain.distance(from: plain.startIndex, to: match.range.lowerBound)
            let length = plain.distance(from: match.range.lowerBound, to: match.range.upperBound)
            let lower = text.characters.index(text.startIndex, offsetBy: start)
            let upper = text.characters.index(lower, offsetBy: length)
            let innerLower = text.characters.index(lower, offsetBy: marker.count)
            let innerUpper = text.characters.index(upper, offsetBy: -marker.count)
            style(&text[innerLower..<innerUpper])
            text.removeSubrange(innerUpper..<upper)
            text.removeSubrange(lower..<innerLower)
        }
    }
}

/// Collapsible sections, as iPhone Notes folds everything under a heading.
enum MarkdownSections {
    /// The blocks left once the given headings are folded. A heading hides
    /// what follows it up to the next heading of the same or a higher level.
    static func visible(_ blocks: [MarkdownBlock], collapsed: Set<Int>) -> [(offset: Int, element: MarkdownBlock)] {
        var result: [(offset: Int, element: MarkdownBlock)] = []
        var hiddenBelow: Int?
        for (index, block) in blocks.enumerated() {
            if case let .heading(level, _) = block {
                if let limit = hiddenBelow, level > limit { continue }
                hiddenBelow = collapsed.contains(index) ? level : nil
                result.append((index, block))
            } else if hiddenBelow == nil {
                result.append((index, block))
            }
        }
        return result
    }

    /// Whether folding this heading would hide anything.
    static func hasContent(_ blocks: [MarkdownBlock], heading index: Int) -> Bool {
        guard blocks.indices.contains(index), case let .heading(level, _) = blocks[index] else { return false }
        guard blocks.indices.contains(index + 1) else { return false }
        if case let .heading(next, _) = blocks[index + 1] { return next > level }
        return true
    }
}
