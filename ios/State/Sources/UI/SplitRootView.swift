import SwiftUI

/// The three column layout the iPad and the Mac use. The sidebar picks the
/// section, the content column shows the matching list, and the detail column
/// shows the reminder the owner selected. The iPhone keeps `MainTabView`.
struct SplitRootView: View {
    @Bindable var model: AppModel

    @State private var section: StateTab? = .today
    @State private var selectedReminderID: String?
    @State private var selectedNoteID: String?
    @State private var opensNotificationSettings = false
    @State private var opensEditor = false
    @State private var columnVisibility: NavigationSplitViewVisibility = .all

    var body: some View {
        NavigationSplitView(columnVisibility: $columnVisibility) {
            List(selection: $section) {
                Label(String(localized: "Today"), systemImage: "sun.max.fill")
                    .tag(StateTab.today)
                    .accessibilityIdentifier("sidebar-today")
                Label(String(localized: "Planned"), systemImage: "calendar")
                    .tag(StateTab.planned)
                    .accessibilityIdentifier("sidebar-planned")
                Label(String(localized: "Notes"), systemImage: "note.text")
                    .tag(StateTab.notes)
                    .accessibilityIdentifier("sidebar-notes")
                Label(String(localized: "Activity"), systemImage: "clock.arrow.circlepath")
                    .badge(model.conflicts.count)
                    .tag(StateTab.activity)
                    .accessibilityIdentifier("sidebar-activity")
                Label(String(localized: "Settings"), systemImage: "gearshape")
                    .tag(StateTab.settings)
                    .accessibilityIdentifier("sidebar-settings")
            }
            .navigationTitle("State")
            .navigationSplitViewColumnWidth(min: 180, ideal: 220)
        } content: {
            switch section ?? .today {
            case .today:
                ReminderCollectionView(
                    model: model,
                    mode: .today,
                    selection: $selectedReminderID,
                    editorPresentation: $opensEditor
                )
            case .planned:
                ReminderCollectionView(
                    model: model,
                    mode: .planned,
                    selection: $selectedReminderID,
                    editorPresentation: $opensEditor
                )
            case .notes:
                NotesCollectionView(model: model, selection: $selectedNoteID)
            case .activity:
                ActivityView(model: model)
            case .settings:
                SettingsView(model: model, opensNotificationSettings: $opensNotificationSettings)
            }
        } detail: {
            detail
        }
        .onChange(of: section) { _, _ in
            selectedReminderID = nil
            selectedNoteID = nil
        }
        .onReceive(NotificationCenter.default.publisher(for: .stateOpenNotificationSettings)) { _ in
            section = .settings
            opensNotificationSettings = true
        }
        .onReceive(NotificationCenter.default.publisher(for: .stateCreateReminder)) { _ in
            createReminder()
        }
        .onReceive(NotificationCenter.default.publisher(for: .stateSynchronizeNow)) { _ in
            Task { await model.synchronize() }
        }
    }

    @ViewBuilder
    private var detail: some View {
        if section == .notes {
            noteDetail
        } else if let selectedReminderID, section == .today || section == .planned {
            NavigationStack {
                ReminderDetailView(model: model, reminderID: selectedReminderID)
            }
            .id(selectedReminderID)
            .accessibilityIdentifier("split-detail")
        } else {
            ContentUnavailableView(
                String(localized: "Select a reminder"),
                systemImage: "checklist"
            )
            .accessibilityIdentifier("split-detail-placeholder")
        }
    }

    @ViewBuilder
    private var noteDetail: some View {
        if let selectedNoteID {
            NavigationStack {
                NoteDetailView(
                    model: model,
                    noteID: selectedNoteID == NoteRoute.newSelection ? nil : selectedNoteID
                ) { created in
                    self.selectedNoteID = created
                }
            }
            .id(selectedNoteID)
        } else {
            ContentUnavailableView(String(localized: "Select a note"), systemImage: "note.text")
        }
    }

    /// The editor lives inside the reminder list, so the menu command has to
    /// make sure such a list is on screen before the sheet can open.
    private func createReminder() {
        if section != .today && section != .planned {
            section = .today
        }
        opensEditor = true
    }
}
