import SwiftUI

enum NoteRoute: Hashable {
    case note(String)
    case new

    /// The split layout keeps a plain string selection; this value stands for
    /// "a note that does not exist yet".
    static let newSelection = "new-note"
}

/// One flat list of notes, newest change first. No folders, no tags: search
/// is the only way to narrow it, as the product spec asks.
struct NotesCollectionView: View {
    @Bindable var model: AppModel
    /// Set by the split layout, which shows the note in its detail column.
    var selection: Binding<String?>?

    @State private var search = ""
    @State private var path: [NoteRoute] = []
    @State private var showsArchive = false

    var body: some View {
        if let selection {
            list(selection: selection)
        } else {
            NavigationStack(path: $path) {
                list(selection: nil)
                    .navigationDestination(for: NoteRoute.self) { route in
                        switch route {
                        case let .note(identifier):
                            NoteDetailView(model: model, noteID: identifier)
                        case .new:
                            NoteDetailView(model: model, noteID: nil)
                        }
                    }
            }
        }
    }

    @ViewBuilder
    private func list(selection: Binding<String?>?) -> some View {
        Group {
            if visibleNotes.isEmpty {
                emptyState
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(StateTheme.ground.ignoresSafeArea())
            } else {
                List(selection: selection) {
                    ForEach(visibleNotes) { note in
                        row(note, selection: selection)
                    }
                }
                .stateListStyle()
                .stateBackground()
                .animation(StateTheme.contentChange, value: visibleNotes.map(\.id))
                .refreshable { await model.synchronize() }
            }
        }
        .navigationTitle(showsArchive ? String(localized: "Archived notes") : String(localized: "Notes"))
        .searchable(text: $search, prompt: String(localized: "Search notes"))
        .toolbar {
            ToolbarItem(placement: .stateLeading) {
                Button {
                    withAnimation(StateTheme.stateChange) { showsArchive.toggle() }
                } label: {
                    Label(
                        showsArchive ? String(localized: "Show all notes") : String(localized: "Archived notes"),
                        systemImage: showsArchive ? "note.text" : "archivebox"
                    )
                }
            }
            ToolbarItem(placement: .primaryAction) {
                Button(action: newNote) {
                    Label(String(localized: "New note"), systemImage: "square.and.pencil")
                }
                .disabled(showsArchive)
            }
        }
        #if DEBUG
        .task(id: model.notes.first?.id) {
            guard selection == nil, path.isEmpty else { return }
            switch StateLaunch.initialNote {
            case "first": if let first = model.notes.first { path = [.note(first.id)] }
            case "new": path = [.new]
            default: break
            }
        }
        #endif
    }

    @ViewBuilder
    private func row(_ note: Note, selection: Binding<String?>?) -> some View {
        if let selection {
            Button {
                selection.wrappedValue = note.id
            } label: {
                NoteRow(note: note)
            }
            .buttonStyle(.plain)
            .contentShape(Rectangle())
            .listRowInsets(Self.rowInsets)
            .listRowBackground(selection.wrappedValue == note.id ? StateTheme.accentSoft : Color.clear)
            .swipeActions(edge: .trailing) { archiveAction(note) }
            .tag(note.id)
        } else {
            NavigationLink(value: NoteRoute.note(note.id)) {
                NoteRow(note: note)
            }
            .listRowInsets(Self.rowInsets)
            .swipeActions(edge: .trailing) { archiveAction(note) }
        }
    }

    private func archiveAction(_ note: Note) -> some View {
        Button(role: note.archived ? nil : .destructive) {
            Task { await model.setNoteArchived(id: note.id, archived: !note.archived) }
        } label: {
            if note.archived {
                Label(String(localized: "Restore"), systemImage: "arrow.uturn.backward")
            } else {
                Label(String(localized: "Archive"), systemImage: "archivebox")
            }
        }
    }

    @ViewBuilder
    private var emptyState: some View {
        if !search.isEmpty {
            ContentUnavailableView.search(text: search)
        } else if showsArchive {
            ContentUnavailableView(String(localized: "Nothing archived"), systemImage: "archivebox")
        } else {
            ContentUnavailableView {
                Label(String(localized: "No notes yet"), systemImage: "note.text")
            } description: {
                Text("Notes you or your agents write appear here.")
            } actions: {
                Button(action: newNote) {
                    Text("New note")
                }
                .buttonStyle(.statePill)
            }
        }
    }

    private func newNote() {
        if let selection {
            selection.wrappedValue = NoteRoute.newSelection
        } else {
            path = [.new]
        }
    }

    private var visibleNotes: [Note] {
        let source = showsArchive ? model.archivedNotes : model.notes
        let query = search.trimmingCharacters(in: .whitespaces)
        return query.isEmpty ? source : source.filter { $0.matches(query) }
    }

    private static let rowInsets = EdgeInsets(
        top: StateTheme.Space.group,
        leading: StateTheme.Space.block,
        bottom: StateTheme.Space.group,
        trailing: StateTheme.Space.block
    )
}

/// Title, a two line summary and when it last changed. Nothing else competes
/// for attention in the list.
struct NoteRow: View {
    let note: Note

    var body: some View {
        VStack(alignment: .leading, spacing: StateTheme.Space.snug) {
            Text(note.title.isEmpty ? String(localized: "New note") : note.title)
                .font(.headline)
                .foregroundStyle(StateTheme.graphite)
                .lineLimit(1)
            if !note.summary.isEmpty {
                Text(note.summary)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
            Text(note.updatedAt, format: .relative(presentation: .named))
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("note-\(note.id)")
    }
}
