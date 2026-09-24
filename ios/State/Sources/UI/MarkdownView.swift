import SwiftUI

/// Renders a reminder description or comment block by block, so the sections
/// an agent writes (Objective, Procedure, Acceptance criteria) stay readable
/// on a phone. Headings carry more space above than below; list markers hang
/// outside the text so wrapped lines align.
struct MarkdownView: View {
    /// `compact` fits a reminder description under its title; `document` is a
    /// note read on its own, with body-sized text and real heading sizes.
    enum Style {
        case compact
        case document
    }

    let blocks: [MarkdownBlock]
    var style: Style = .compact
    /// When set, checklist items are buttons that report their source line.
    var onToggleTask: ((Int) -> Void)?

    init(_ source: String, style: Style = .compact, onToggleTask: ((Int) -> Void)? = nil) {
        blocks = MarkdownBlocks.parse(source)
        self.style = style
        self.onToggleTask = onToggleTask
    }

    init(blocks: [MarkdownBlock], style: Style = .compact, onToggleTask: ((Int) -> Void)? = nil) {
        self.blocks = blocks
        self.style = style
        self.onToggleTask = onToggleTask
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

    private var textFont: Font { style == .document ? .body : .callout }

    private func headingFont(level: Int) -> Font {
        switch (style, level) {
        case (.document, 1): .title3.weight(.semibold)
        case (.document, 2): .headline
        case (.document, _): .subheadline.weight(.semibold)
        case (.compact, _): .subheadline.weight(.semibold)
        }
    }

    @ViewBuilder
    private func view(for block: MarkdownBlock) -> some View {
        switch block {
        case let .heading(level, text):
            Text(inlineMarkdown: text)
                .font(headingFont(level: level))
                .foregroundStyle(StateTheme.graphite)
                .accessibilityAddTraits(.isHeader)
        case let .paragraph(text):
            Text(inlineMarkdown: text)
                .font(textFont)
                .foregroundStyle(StateTheme.graphite.opacity(0.86))
        case let .bullet(text):
            listItem(marker: "•", text: text)
        case let .numbered(marker, text):
            listItem(marker: marker, text: text)
        case let .task(checked, text, line):
            taskItem(checked: checked, text: text, line: line)
        case .divider:
            Rectangle()
                .fill(StateTheme.graphite.opacity(0.12))
                .frame(height: 1)
                .padding(.vertical, StateTheme.Space.inner)
                .accessibilityHidden(true)
        case let .code(text):
            Text(verbatim: text)
                .font(.footnote.monospaced())
                .foregroundStyle(StateTheme.graphite)
                .padding(StateTheme.Space.inner)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(StateTheme.accentSoft, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
        }
    }

    @ViewBuilder
    private func taskItem(checked: Bool, text: String, line: Int) -> some View {
        let label = HStack(alignment: .firstTextBaseline, spacing: StateTheme.Space.inner) {
            Image(systemName: checked ? "checkmark.circle.fill" : "circle")
                .font(textFont)
                .foregroundStyle(checked ? StateTheme.accent : Color.secondary)
                .frame(minWidth: 16)
                .accessibilityHidden(true)
            Text(inlineMarkdown: text)
                .font(textFont)
                .foregroundStyle(checked ? Color.secondary : StateTheme.graphite.opacity(0.86))
                .strikethrough(checked, color: .secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        if let onToggleTask {
            Button {
                onToggleTask(line)
            } label: {
                label
                    .frame(minHeight: StateControlMetrics.tapTarget)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(Text(inlineMarkdown: text))
            .accessibilityValue(checked ? String(localized: "Checked") : String(localized: "Not checked"))
            .accessibilityAddTraits(.isToggle)
        } else {
            label
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(Text(inlineMarkdown: text))
                .accessibilityValue(checked ? String(localized: "Checked") : String(localized: "Not checked"))
        }
    }

    private func listItem(marker: String, text: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: StateTheme.Space.inner) {
            Text(verbatim: marker)
                .font(textFont.monospacedDigit())
                .foregroundStyle(.secondary)
                .frame(minWidth: 16, alignment: .trailing)
            Text(inlineMarkdown: text)
                .font(textFont)
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
