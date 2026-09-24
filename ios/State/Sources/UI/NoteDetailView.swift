import SwiftUI

/// One note: read it as rendered Markdown, tap a checklist item to tick it,
/// or switch to the editor. Typing is saved shortly after it pauses, when the
/// app goes to the background and when the editor closes, as in Apple Notes.
struct NoteDetailView: View {
    @Bindable var model: AppModel
    /// Called once a new note exists, so the split layout can select it.
    var onCreate: ((String) -> Void)?
    /// Called when the note leaves this screen: archived, or discarded empty.
    var onClose: (() -> Void)?

    @State private var noteID: String?
    @State private var isEditing: Bool
    @State private var titleDraft = ""
    @State private var documentDraft = ""
    /// The note as the editor found it, or as it was last saved from here.
    @State private var editBase: Note?
    @State private var isSaving = false
    @State private var selection: TextSelection?
    @FocusState private var focus: Field?
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase

    private enum Field { case title, body }

    init(model: AppModel, noteID: String?, onCreate: ((String) -> Void)? = nil, onClose: (() -> Void)? = nil) {
        self.model = model
        self.onCreate = onCreate
        self.onClose = onClose
        _noteID = State(initialValue: noteID)
        _isEditing = State(initialValue: noteID == nil)
    }

    var body: some View {
        Group {
            if isEditing {
                editor
            } else if let note = currentNote {
                reader(note)
            } else {
                ContentUnavailableView(String(localized: "Select a note"), systemImage: "note.text")
            }
        }
        .background(StateTheme.ground.ignoresSafeArea())
        // The note shows its title in large type; the bar stays quiet.
        .navigationTitle("")
        .stateInlineNavigationTitle()
        .toolbar { toolbar }
        .task {
            // Focus only takes once the push transition has settled.
            guard isEditing, noteID == nil else { return }
            try? await Task.sleep(for: .milliseconds(450))
            focus = .body
        }
        .task(id: documentDraft + "\u{0}" + titleDraft) {
            guard isEditing else { return }
            try? await Task.sleep(for: .seconds(2))
            guard !Task.isCancelled else { return }
            await save(reveal: false)
        }
        .onChange(of: scenePhase) { _, phase in
            if phase != .active, isEditing {
                Task { await save(reveal: false) }
            }
        }
        .onChange(of: titleDraft) { _, title in
            let clamped = NoteText.clampTitle(title)
            if clamped != title { titleDraft = clamped }
        }
        .onDisappear {
            if isEditing {
                Task { await save(reveal: false) }
            }
        }
    }

    // MARK: Reading

    private func reader(_ note: Note) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: StateTheme.Space.group) {
                Text(note.title)
                    .font(.title2.bold())
                    .foregroundStyle(StateTheme.graphite)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityAddTraits(.isHeader)

                HStack(spacing: StateTheme.Space.inner) {
                    TimelineView(.periodic(from: .now, by: 30)) { _ in
                        Text(note.updatedAt, format: .relative(presentation: .named))
                    }
                    if note.archived {
                        Label(String(localized: "Archived"), systemImage: "archivebox")
                            .labelStyle(.titleAndIcon)
                    }
                }
                .font(.caption)
                .foregroundStyle(.secondary)

                if let error = model.noteSyncError(id: note.id) {
                    Label(String(localized: "Not synced: \(error)"), systemImage: "exclamationmark.icloud")
                        .font(.caption)
                        .foregroundStyle(.orange)
                }

                MarkdownView(Self.body(of: note), style: .document) { line in
                    Task { await model.toggleNoteTask(id: note.id, line: line) }
                }
                .padding(.top, StateTheme.Space.tight)
            }
            .padding(.horizontal, StateTheme.Space.section)
            .padding(.vertical, StateTheme.Space.block)
            .frame(maxWidth: 720, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    /// A derived title is the document's first line, so the reader shows it
    /// once, as the title. That line is blanked rather than removed, which
    /// keeps every checklist item on its source line. Nothing is blanked when
    /// the first line is structure (a fence, a divider) or when the title had
    /// to be shortened, because then the line holds more than the title.
    static func body(of note: Note) -> String {
        guard note.titleSource == Note.derivedSource else { return note.document }
        var lines = note.document.components(separatedBy: "\n")
        guard let first = lines.firstIndex(where: { !$0.trimmingCharacters(in: .whitespaces).isEmpty }) else {
            return note.document
        }
        let line = lines[first].trimmingCharacters(in: .whitespaces)
        let isStructure = line.hasPrefix("```") || line.hasPrefix("~~~") || ["---", "***", "___"].contains(line)
        guard !isStructure, NoteText.plainText(lines[first]) == note.title else { return note.document }
        lines[first] = ""
        return lines.joined(separator: "\n")
    }

    // MARK: Editing

    private var editor: some View {
        VStack(alignment: .leading, spacing: 0) {
            TextField(String(localized: "Title"), text: $titleDraft, prompt: Text(titlePlaceholder))
                .font(.title2.bold())
                .foregroundStyle(StateTheme.graphite)
                .textFieldStyle(.plain)
                .focused($focus, equals: .title)
                .submitLabel(.next)
                .onSubmit { focus = .body }
                .padding(.horizontal, StateTheme.Space.section)
                .padding(.top, StateTheme.Space.block)
                .padding(.bottom, StateTheme.Space.inner)

            TextEditor(text: $documentDraft, selection: $selection)
                .font(.body)
                .foregroundStyle(StateTheme.graphite)
                .scrollContentBackground(.hidden)
                .focused($focus, equals: .body)
                // The text view insets its text a few points; this lines the
                // first character up with the title above.
                .padding(.horizontal, StateTheme.Space.section - Self.textInset.width)
                .overlay(alignment: .topLeading) {
                    if documentDraft.isEmpty {
                        Text("Start writing")
                            .font(.body)
                            .foregroundStyle(.tertiary)
                            .padding(.horizontal, StateTheme.Space.section)
                            .padding(.top, Self.textInset.height)
                            .allowsHitTesting(false)
                    }
                }
        }
        .frame(maxWidth: 760)
        .frame(maxWidth: .infinity)
        .safeAreaInset(edge: .bottom) {
            if showsFormatBar {
                formatBar
            }
        }
    }

    #if os(macOS)
    private static let textInset = CGSize(width: 0, height: 0)
    private var showsFormatBar: Bool { true }
    #else
    private static let textInset = CGSize(width: 5, height: 8)
    /// On the phone the bar belongs to the keyboard: it shows while the text
    /// is being typed and gets out of the way otherwise.
    private var showsFormatBar: Bool { focus == .body }
    #endif

    /// Without a written title the first line becomes the title, and the
    /// placeholder says so by showing it.
    private var titlePlaceholder: String {
        let derived = NoteText.title(documentDraft)
        return derived.isEmpty ? String(localized: "Title") : derived
    }

    private var formatBar: some View {
        VStack(spacing: 0) {
            Rectangle()
                .fill(StateTheme.graphite.opacity(0.08))
                .frame(height: 1)
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 0) {
                    formatButton(String(localized: "Heading"), systemImage: "textformat.size") { prefix("## ") }
                    formatButton(String(localized: "Bold"), systemImage: "bold") { wrap("**") }
                    formatButton(String(localized: "Italic"), systemImage: "italic") { wrap("*") }
                    formatButton(String(localized: "List"), systemImage: "list.bullet") { prefix("- ") }
                    formatButton(String(localized: "Numbered list"), systemImage: "list.number") { prefix("1. ") }
                    formatButton(String(localized: "Checklist"), systemImage: "checklist") { prefix("- [ ] ") }
                    formatButton(String(localized: "Divider"), systemImage: "minus") { divider() }
                }
                .padding(.horizontal, StateTheme.Space.inner)
            }
        }
        .background(StateTheme.ground)
    }

    private func formatButton(_ title: String, systemImage: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: systemImage)
                .font(.body.weight(.medium))
                .frame(width: StateControlMetrics.formatButton, height: StateControlMetrics.formatButton)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .foregroundStyle(StateTheme.accent)
        .accessibilityLabel(title)
        .help(title)
    }

    /// The selection as a range of the current draft. A selection left over
    /// from different text is ignored rather than trusted, because its indices
    /// could split a character.
    private var currentRange: Range<String.Index> {
        if case let .selection(range)? = selection?.indices,
           let lower = String.Index(range.lowerBound, within: documentDraft),
           let upper = String.Index(range.upperBound, within: documentDraft),
           lower <= upper {
            return lower..<upper
        }
        return documentDraft.endIndex..<documentDraft.endIndex
    }

    private func prefix(_ marker: String) {
        let (text, cursor) = NoteEditing.toggleLinePrefix(marker, in: documentDraft, at: currentRange)
        apply(text, cursor: cursor)
    }

    private func wrap(_ marker: String) {
        let (text, cursor) = NoteEditing.wrap(marker, in: documentDraft, range: currentRange)
        apply(text, cursor: cursor)
    }

    private func divider() {
        let (text, cursor) = NoteEditing.insertDivider(in: documentDraft, at: currentRange)
        apply(text, cursor: cursor)
    }

    private func apply(_ text: String, cursor: String.Index) {
        documentDraft = text
        selection = TextSelection(insertionPoint: cursor)
        focus = .body
    }

    // MARK: Toolbar and persistence

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        ToolbarItemGroup(placement: .primaryAction) {
            if isEditing {
                Button(String(localized: "Done")) {
                    Task { await finishEditing() }
                }
                .fontWeight(.semibold)
                .keyboardShortcut(.return, modifiers: .command)
            } else if let note = currentNote {
                Button {
                    beginEditing()
                } label: {
                    Label(String(localized: "Edit"), systemImage: "pencil")
                }
                .keyboardShortcut("e", modifiers: .command)
                Button {
                    Task {
                        await model.setNoteArchived(id: note.id, archived: !note.archived)
                        if !note.archived { close() }
                    }
                } label: {
                    if note.archived {
                        Label(String(localized: "Restore"), systemImage: "arrow.uturn.backward")
                    } else {
                        Label(String(localized: "Archive"), systemImage: "archivebox")
                    }
                }
            }
        }
    }

    private var currentNote: Note? {
        noteID.flatMap { model.note(id: $0) }
    }

    private func beginEditing() {
        guard let note = currentNote else { return }
        titleDraft = note.titleSource == Note.userSource ? note.title : ""
        documentDraft = note.document
        editBase = note
        selection = nil
        isEditing = true
    }

    private func finishEditing() async {
        let wasArchived = currentNote?.archived ?? false
        await save(reveal: true)
        focus = nil
        if noteID == nil || currentNote == nil || (currentNote?.archived == true && !wasArchived) {
            // Nothing was written, or the emptied note was archived.
            isEditing = false
            close()
            return
        }
        isEditing = false
    }

    private func close() {
        if let onClose {
            onClose()
        } else {
            dismiss()
        }
    }

    /// Writes the drafts. New notes are created on the first save that has
    /// something to keep; later saves edit that note. Saves never overlap, so
    /// a slow first save cannot create the note twice.
    private func save(reveal: Bool) async {
        guard !isSaving else { return }
        isSaving = true
        defer { isSaving = false }
        let title = titleDraft
        let document = documentDraft

        guard let noteID else {
            guard let created = await model.createNote(title: title, document: document) else { return }
            self.noteID = created
            editBase = model.note(id: created)
            if reveal { onCreate?(created) }
            return
        }
        guard let base = editBase else { return }
        let unchanged = document == base.document
            && (title.isEmpty ? base.titleSource == Note.derivedSource : title == base.title && base.titleSource == Note.userSource)
        guard !unchanged else { return }
        switch await model.saveNote(id: noteID, title: title, document: document, base: base) {
        case let .saved(note):
            editBase = note
        case let .savedAsCopy(copyID):
            // The note changed elsewhere; keep editing the copy that holds
            // this text so further typing does not create more copies.
            self.noteID = copyID
            editBase = model.note(id: copyID)
            if reveal { onCreate?(copyID) }
        case .archivedEmpty:
            editBase = model.note(id: noteID)
        case .rejected:
            break
        }
    }
}
