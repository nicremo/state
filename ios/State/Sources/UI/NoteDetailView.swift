import SwiftUI

/// One note: read it as rendered Markdown, tap a checklist item to tick it,
/// or switch to the editor. Leaving the editor saves, as in Apple Notes.
struct NoteDetailView: View {
    @Bindable var model: AppModel
    /// Called once a new note exists, so the split layout can select it.
    var onCreate: ((String) -> Void)?

    @State private var noteID: String?
    @State private var stored: Note?
    @State private var isEditing: Bool
    @State private var titleDraft = ""
    @State private var documentDraft = ""
    @State private var selection: TextSelection?
    @FocusState private var focus: Field?
    @Environment(\.dismiss) private var dismiss

    private enum Field { case title, body }

    init(model: AppModel, noteID: String?, onCreate: ((String) -> Void)? = nil) {
        self.model = model
        self.onCreate = onCreate
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
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .background(StateTheme.ground.ignoresSafeArea())
        // The note shows its title in large type; the bar stays quiet.
        .navigationTitle("")
        .stateInlineNavigationTitle()
        .toolbar { toolbar }
        .task(id: model.notes) { await reload() }
        .onAppear {
            if isEditing {
                beginEditing()
            }
        }
        .onDisappear {
            if isEditing {
                let title = titleDraft
                let document = documentDraft
                Task { await save(title: title, document: document, reveal: false) }
            }
        }
    }

    // MARK: Reading

    private func reader(_ note: Note) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: StateTheme.Space.group) {
                Text(note.title.isEmpty ? String(localized: "New note") : note.title)
                    .font(.title2.bold())
                    .foregroundStyle(StateTheme.graphite)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityAddTraits(.isHeader)

                HStack(spacing: StateTheme.Space.inner) {
                    Text(note.updatedAt, format: .relative(presentation: .named))
                    if note.archived {
                        Label(String(localized: "Archived"), systemImage: "archivebox")
                            .labelStyle(.titleAndIcon)
                    }
                }
                .font(.caption)
                .foregroundStyle(.tertiary)

                MarkdownView(Self.body(of: note)) { line in
                    let document = NoteEditing.toggleTask(atLine: line, in: note.document)
                    Task { await model.updateNote(id: note.id, title: nil, document: document) }
                }
                .padding(.top, StateTheme.Space.tight)
            }
            .padding(.horizontal, StateTheme.Space.section)
            .padding(.vertical, StateTheme.Space.block)
            .frame(maxWidth: 720, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .onTapGesture(count: 2) { beginEditing() }
    }

    /// A derived title is the document's first line, so the reader shows it
    /// once, as the title. The line is blanked rather than removed, which keeps
    /// every checklist item on its original line number.
    static func body(of note: Note) -> String {
        guard note.titleSource == Note.derivedSource else { return note.document }
        var lines = note.document.components(separatedBy: "\n")
        if let first = lines.firstIndex(where: { !$0.trimmingCharacters(in: .whitespaces).isEmpty }) {
            lines[first] = ""
        }
        return lines.joined(separator: "\n")
    }

    // MARK: Editing

    private var editor: some View {
        VStack(alignment: .leading, spacing: 0) {
            TextField(
                String(localized: "Title"),
                text: $titleDraft,
                prompt: Text(titlePlaceholder)
            )
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
                // The text view insets its text by about five points; this
                // lines the first character up with the title above.
                .padding(.horizontal, StateTheme.Space.section - 5)
                .overlay(alignment: .topLeading) {
                    if documentDraft.isEmpty {
                        Text("Start writing")
                            .font(.body)
                            .foregroundStyle(.tertiary)
                            .padding(.horizontal, StateTheme.Space.section)
                            .padding(.top, 8)
                            .allowsHitTesting(false)
                    }
                }
        }
        .frame(maxWidth: 760)
        .frame(maxWidth: .infinity)
        .safeAreaInset(edge: .bottom) { formatBar }
    }

    /// Without a written title the first line becomes the title, and the
    /// placeholder says so by showing it.
    private var titlePlaceholder: String {
        let derived = NoteText.title(documentDraft)
        return derived.isEmpty ? String(localized: "Title") : derived
    }

    private var formatBar: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: StateTheme.Space.tight) {
                formatButton(String(localized: "Heading"), systemImage: "textformat.size") { prefix("## ") }
                formatButton(String(localized: "Bold"), systemImage: "bold") { wrap("**") }
                formatButton(String(localized: "Italic"), systemImage: "italic") { wrap("*") }
                formatButton(String(localized: "List"), systemImage: "list.bullet") { prefix("- ") }
                formatButton(String(localized: "Numbered list"), systemImage: "list.number") { prefix("1. ") }
                formatButton(String(localized: "Checklist"), systemImage: "checklist") { prefix("- [ ] ") }
                formatButton(String(localized: "Divider"), systemImage: "minus") { divider() }
            }
            .padding(.horizontal, StateTheme.Space.block)
            .padding(.vertical, StateTheme.Space.inner)
        }
        .background(.bar)
        .opacity(focus == .body ? 1 : 0.55)
        .animation(StateTheme.stateChange, value: focus)
    }

    private func formatButton(_ title: String, systemImage: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: systemImage)
                .font(.body.weight(.medium))
                .frame(width: 40, height: 36)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .foregroundStyle(StateTheme.accent)
        .accessibilityLabel(title)
        .help(title)
    }

    private var currentRange: Range<String.Index> {
        if case let .selection(range)? = selection?.indices,
           range.lowerBound >= documentDraft.startIndex,
           range.upperBound <= documentDraft.endIndex {
            return range
        }
        return documentDraft.endIndex..<documentDraft.endIndex
    }

    private func prefix(_ marker: String) {
        let (text, cursor) = NoteEditing.insertLinePrefix(marker, in: documentDraft, at: currentRange)
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
                    let title = titleDraft
                    let document = documentDraft
                    isEditing = false
                    focus = nil
                    Task { await save(title: title, document: document, reveal: true) }
                }
                .fontWeight(.semibold)
            } else if let note = currentNote {
                Button {
                    beginEditing()
                } label: {
                    Label(String(localized: "Edit"), systemImage: "pencil")
                }
                Button {
                    Task {
                        await model.setNoteArchived(id: note.id, archived: !note.archived)
                        if !note.archived { dismiss() }
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
        noteID.flatMap { model.note(id: $0) } ?? stored
    }

    private func reload() async {
        guard let noteID else { return }
        stored = await model.storedNote(id: noteID)
    }

    private func beginEditing() {
        if let note = currentNote {
            titleDraft = note.titleSource == Note.userSource ? note.title : ""
            documentDraft = note.document
        }
        isEditing = true
        focus = currentNote == nil ? .body : nil
    }

    /// `reveal` selects a newly created note. Saving on the way out must not,
    /// or it would steal the selection the owner just made elsewhere.
    private func save(title: String, document: String, reveal: Bool) async {
        if let noteID {
            await model.updateNote(id: noteID, title: title, document: document)
            return
        }
        let isEmpty = title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && document.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        guard !isEmpty, let created = await model.createNote(title: title, document: document) else { return }
        noteID = created
        if reveal {
            onCreate?(created)
        }
    }
}
