import SwiftUI

/// Renders a reminder description or comment block by block, so the sections
/// an agent writes (Objective, Procedure, Acceptance criteria) stay readable
/// on a phone. Headings carry more space above than below; list markers hang
/// outside the text so wrapped lines align.
struct MarkdownView: View {
    let blocks: [MarkdownBlock]

    init(_ source: String) {
        blocks = MarkdownBlocks.parse(source)
    }

    init(blocks: [MarkdownBlock]) {
        self.blocks = blocks
    }

    var body: some View {
        VStack(alignment: .leading, spacing: StateTheme.Space.snug) {
            ForEach(Array(blocks.enumerated()), id: \.offset) { index, block in
                view(for: block)
                    .padding(.top, topSpacing(for: block, at: index))
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    @ViewBuilder
    private func view(for block: MarkdownBlock) -> some View {
        switch block {
        case let .heading(_, text):
            Text(inlineMarkdown: text)
                .font(.subheadline.weight(.semibold))
                .foregroundStyle(StateTheme.graphite)
                .accessibilityAddTraits(.isHeader)
        case let .paragraph(text):
            Text(inlineMarkdown: text)
                .font(.callout)
                .foregroundStyle(StateTheme.graphite.opacity(0.86))
        case let .bullet(text):
            listItem(marker: "•", text: text)
        case let .numbered(marker, text):
            listItem(marker: marker, text: text)
        case let .code(text):
            Text(verbatim: text)
                .font(.footnote.monospaced())
                .foregroundStyle(StateTheme.graphite)
                .padding(StateTheme.Space.inner)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(StateTheme.accentSoft, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
        }
    }

    private func listItem(marker: String, text: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: StateTheme.Space.inner) {
            Text(verbatim: marker)
                .font(.callout.monospacedDigit())
                .foregroundStyle(.secondary)
                .frame(minWidth: 16, alignment: .trailing)
            Text(inlineMarkdown: text)
                .font(.callout)
                .foregroundStyle(StateTheme.graphite.opacity(0.86))
        }
    }

    private func topSpacing(for block: MarkdownBlock, at index: Int) -> CGFloat {
        guard index > 0, case .heading = block else { return 0 }
        return StateTheme.Space.group
    }
}

/// A long description starts folded to its first blocks, so the occurrences
/// and runs below stay within reach. Nothing is hidden without a way back.
struct CollapsibleMarkdown: View {
    let source: String
    var foldedBlockCount = 6
    @State private var expanded = false

    var body: some View {
        let blocks = MarkdownBlocks.parse(source)
        let folds = blocks.count > foldedBlockCount + 1
        VStack(alignment: .leading, spacing: StateTheme.Space.group) {
            MarkdownView(blocks: folds && !expanded ? Array(blocks.prefix(foldedBlockCount)) : blocks)
            if folds {
                Button {
                    withAnimation(StateTheme.contentChange) { expanded.toggle() }
                } label: {
                    Label(
                        expanded ? String(localized: "Show less") : String(localized: "Show all"),
                        systemImage: expanded ? "chevron.up" : "chevron.down"
                    )
                    .font(.subheadline.weight(.medium))
                }
                .buttonStyle(.plain)
                .foregroundStyle(StateTheme.accent)
            }
        }
    }
}

extension Text {
    /// Inline Markdown only: emphasis, links and `code`. Block structure is
    /// handled by `MarkdownBlocks`.
    init(inlineMarkdown: String) {
        let options = AttributedString.MarkdownParsingOptions(
            interpretedSyntax: .inlineOnlyPreservingWhitespace
        )
        if let attributed = try? AttributedString(markdown: inlineMarkdown, options: options) {
            self.init(attributed)
        } else {
            self.init(verbatim: inlineMarkdown)
        }
    }
}
