import SwiftUI

enum StateTab: Hashable {
    case today
    case planned
    case activity
    case settings
}

struct MainTabView: View {
    @Bindable var model: AppModel
    @State private var selection: StateTab = .today
    @State private var opensNotificationSettings = false

    var body: some View {
        TabView(selection: $selection) {
            ReminderCollectionView(model: model, mode: .today)
                .tabItem { Label(String(localized: "Today"), systemImage: "sun.max.fill") }
                .tag(StateTab.today)

            ReminderCollectionView(model: model, mode: .planned)
                .tabItem { Label(String(localized: "Planned"), systemImage: "calendar") }
                .tag(StateTab.planned)

            ActivityView(model: model)
                .tabItem { Label(String(localized: "Activity"), systemImage: "clock.arrow.circlepath") }
                .badge(model.conflicts.count)
                .tag(StateTab.activity)

            SettingsView(model: model, opensNotificationSettings: $opensNotificationSettings)
                .tabItem { Label(String(localized: "Settings"), systemImage: "gearshape") }
                .tag(StateTab.settings)
        }
        .onReceive(NotificationCenter.default.publisher(for: .stateOpenNotificationSettings)) { _ in
            selection = .settings
            opensNotificationSettings = true
        }
    }
}

enum ReminderCollectionMode {
    case today
    case planned

    var title: LocalizedStringKey {
        switch self {
        case .today: "Today"
        case .planned: "Planned"
        }
    }
}

struct ReminderCollectionView: View {
    @Bindable var model: AppModel
    let mode: ReminderCollectionMode
    /// When set, the list drives an external selection instead of pushing onto
    /// its own navigation stack. The split layout uses this for its detail
    /// column; the iPhone passes nothing and keeps the navigation stack.
    var selection: Binding<String?>?
    /// The split layout owns one editor sheet for the whole window, so the Mac
    /// menu command and the toolbar button open the same sheet.
    var editorPresentation: Binding<Bool>?

    @State private var search = ""
    @State private var ownEditorPresentation = false
    @State private var path: [String] = []
    @State private var createdReminderID: String?

    var body: some View {
        if let selection {
            list(selection: selection)
        } else {
            NavigationStack(path: $path) {
                list(selection: nil)
                    .navigationDestination(for: String.self) { reminderID in
                        ReminderDetailView(model: model, reminderID: reminderID)
                    }
            }
        }
    }

    /// The list itself. The navigation variant and the selection variant share
    /// it; only the row wrapper and the surrounding stack differ.
    @ViewBuilder
    private func list(selection: Binding<String?>?) -> some View {
        Group {
            if filteredReminders.isEmpty {
                emptyState
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(StateTheme.warmBackground.ignoresSafeArea())
            } else {
                List(selection: selection) {
                    ForEach(filteredReminders) { reminder in
                        row(reminder, selection: selection)
                    }
                }
                .stateListStyle()
                .stateBackground()
                .animation(StateTheme.contentChange, value: filteredReminders.map(\.id))
                .refreshable { await model.synchronize() }
            }
        }
        .navigationTitle(mode.title)
        .searchable(text: $search, prompt: String(localized: "Search reminders"))
        .toolbar {
            ToolbarItem(placement: .stateLeading) {
                if model.isSyncing {
                    ProgressView()
                        .controlSize(.small)
                        .transition(.opacity)
                        .accessibilityLabel(String(localized: "Synchronizing"))
                }
            }
            ToolbarItem(placement: .primaryAction) {
                Button {
                    editorIsPresented.wrappedValue = true
                } label: {
                    Label(String(localized: "New reminder"), systemImage: "plus")
                }
            }
        }
        .animation(StateTheme.stateChange, value: model.isSyncing)
        .sheet(
            isPresented: editorIsPresented,
            onDismiss: revealCreatedReminder,
            content: {
                ReminderEditorView(model: model) { draft in
                    createdReminderID = await model.createReminder(draft)
                }
            }
        )
    }

    /// A row either pushes onto the navigation stack or carries the selection
    /// the split layout reads, and either way it looks and behaves the same.
    @ViewBuilder
    private func row(_ reminder: Reminder, selection: Binding<String?>?) -> some View {
        let content = ReminderRow(
            reminder: reminder,
            latestEvent: model.activity.first { $0.reminderID == reminder.id }
        )
        Group {
            if selection == nil {
                NavigationLink(value: reminder.id) { content }
            } else {
                content.tag(reminder.id)
            }
        }
        .listRowInsets(
            EdgeInsets(
                top: StateTheme.Space.group,
                leading: StateTheme.Space.block,
                bottom: StateTheme.Space.group,
                trailing: StateTheme.Space.block
            )
        )
        .swipeActions(edge: .trailing) {
            Button(role: .destructive) {
                Task { await model.archiveReminder(reminder, archived: true) }
            } label: {
                Label(String(localized: "Archive"), systemImage: "archivebox")
            }
        }
    }

    private var editorIsPresented: Binding<Bool> {
        editorPresentation ?? $ownEditorPresentation
    }

    @ViewBuilder
    private var emptyState: some View {
        if search.isEmpty {
            ContentUnavailableView {
                Label(String(localized: "Nothing pending"), systemImage: "checkmark.circle")
            } description: {
                Text("Reminders you or your agents create appear here.")
            } actions: {
                Button {
                    editorIsPresented.wrappedValue = true
                } label: {
                    Text("New reminder")
                }
                .buttonStyle(.borderedProminent)
            }
        } else {
            ContentUnavailableView.search(text: search)
        }
    }

    /// Opening the new reminder proves it exists. Without it a reminder that
    /// belongs to the other tab, an undated one for example, looks exactly like
    /// nothing happened.
    private func revealCreatedReminder() {
        guard let createdReminderID else { return }
        if let selection {
            selection.wrappedValue = createdReminderID
        } else {
            path = [createdReminderID]
        }
        self.createdReminderID = nil
    }

    private var filteredReminders: [Reminder] {
        let today = Date().formatted(.iso8601.year().month().day())
        return model.reminders.filter { reminder in
            let belongs: Bool
            switch mode {
            case .today:
                belongs = reminder.schedule?.localDate ?? "9999-12-31" <= today
            case .planned:
                belongs = reminder.schedule == nil || (reminder.schedule?.localDate ?? today) > today
            }
            let matches = search.isEmpty
                || reminder.title.localizedStandardContains(search)
                || reminder.description?.localizedStandardContains(search) == true
            return belongs && matches
        }
    }
}

struct ReminderRow: View {
    let reminder: Reminder
    let latestEvent: AuditEvent?

    var body: some View {
        VStack(alignment: .leading, spacing: StateTheme.Space.snug) {
            Text(reminder.title)
                .font(.headline)
                .foregroundStyle(StateTheme.graphite)
                .lineLimit(2)

            if let description = reminder.description, !description.isEmpty {
                Text(description)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }

            HStack(spacing: StateTheme.Space.inner) {
                if let schedule = reminder.schedule {
                    MetaLabel(
                        text: StateDateFormatter.rowLabel(for: schedule),
                        systemImage: isOverdue ? "exclamationmark.circle" : "calendar",
                        tint: isOverdue ? .orange : .secondary
                    )
                }
                if let actor = latestEvent?.actor {
                    OriginBadge(actor: actor)
                }
                Spacer(minLength: StateTheme.Space.tight)
                Text(verbatim: "r\(reminder.revision)")
                    .font(.caption2.monospacedDigit())
                    .foregroundStyle(.tertiary)
            }
            .padding(.top, StateTheme.Space.hairline)
        }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("reminder-\(reminder.id)")
    }

    /// A due date in the past that nobody has acted on deserves a warmer color
    /// than the same date after completion.
    private var isOverdue: Bool {
        guard
            reminder.status == .active,
            let schedule = reminder.schedule,
            let date = StateDateFormatter.date(from: schedule)
        else {
            return false
        }
        return date < Date()
    }
}
