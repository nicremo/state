import SwiftUI

/// The four sections of State. Agenda holds today's and the planned
/// reminders with a switch between them; Activity sits behind a button in
/// Agenda's toolbar.
enum StateTab: Hashable {
    case agenda
    case notes
    case agent
    case settings

    /// The DEBUG launch argument `-stateInitialTab` still names the old
    /// sections; they land in the section that holds them now.
    static func launch(from name: String?) -> StateTab {
        switch name {
        case "notes": .notes
        case "agent": .agent
        case "settings": .settings
        default: .agenda
        }
    }
}

struct MainTabView: View {
    @Bindable var model: AppModel
    @State private var selection: StateTab = MainTabView.launchTab
    @State private var agendaMode: ReminderCollectionMode = MainTabView.launchMode
    @State private var opensNotificationSettings = false

    private static var launchTab: StateTab {
        #if DEBUG
        StateTab.launch(from: StateLaunch.initialTab)
        #else
        .agenda
        #endif
    }

    private static var launchMode: ReminderCollectionMode {
        #if DEBUG
        ReminderCollectionMode.launch(from: StateLaunch.initialTab)
        #else
        .today
        #endif
    }

    var body: some View {
        TabView(selection: $selection) {
            ReminderCollectionView(model: model, mode: .today, modeSelection: $agendaMode)
                .tabItem { Label(String(localized: "Agenda"), systemImage: "calendar.day.timeline.left") }
                .badge(model.conflicts.count)
                .tag(StateTab.agenda)

            NotesCollectionView(model: model)
                .tabItem { Label(String(localized: "Notes"), systemImage: "note.text") }
                .tag(StateTab.notes)

            AgentSessionsView(model: model)
                .tabItem { Label(String(localized: "Agent"), systemImage: "terminal") }
                .badge(model.agentSessions.filter { $0.status == .needsApproval }.count)
                .tag(StateTab.agent)

            SettingsView(model: model, opensNotificationSettings: $opensNotificationSettings)
                .tabItem { Label(String(localized: "Settings"), systemImage: "gearshape") }
                .tag(StateTab.settings)
        }
        .onReceive(NotificationCenter.default.publisher(for: .stateOpenNotificationSettings)) { _ in
            selection = .settings
            opensNotificationSettings = true
        }
        .onChange(of: model.requestedTab) { _, tab in
            guard let tab else { return }
            selection = tab
            model.requestedTab = nil
        }
    }
}

enum ReminderCollectionMode: Hashable {
    case today
    case planned

    static func launch(from name: String?) -> ReminderCollectionMode {
        name == "planned" ? .planned : .today
    }

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
    /// When set, the list shows a switch between Today and Planned and an
    /// Activity button; the Agenda tab and the split layout pass it.
    var modeSelection: Binding<ReminderCollectionMode>?

    @State private var showsActivity = false

    private var currentMode: ReminderCollectionMode { modeSelection?.wrappedValue ?? mode }

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
                    .background(StateTheme.ground.ignoresSafeArea())
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
        .navigationTitle(currentMode.title)
        .modifier(AgendaModeSwitch(modeSelection: modeSelection, inNavigationBar: selection == nil))
        .searchable(text: $search, prompt: String(localized: "Search reminders"))
        .toolbar {
            // Pull to refresh shows the synchronization; a spinner here left
            // an empty bar item behind whenever nothing was syncing.
            if modeSelection != nil {
                ToolbarItem(placement: .stateLeading) {
                    Button {
                        showsActivity = true
                    } label: {
                        Label(String(localized: "Activity"), systemImage: model.conflicts.isEmpty ? "clock.arrow.circlepath" : "exclamationmark.arrow.circlepath")
                    }
                    .accessibilityIdentifier("agenda-activity")
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
        .sheet(isPresented: $showsActivity) {
            ActivityView(model: model, onDone: { showsActivity = false })
                #if os(macOS)
                .frame(minWidth: 520, minHeight: 560)
                #endif
        }
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

    /// A row either pushes onto the navigation stack or sets the selection the
    /// split layout reads, and either way it looks and behaves the same. The
    /// selection branch carries a real button as well as its tag, because a tag
    /// alone does not make a Mac list row respond to a click.
    @ViewBuilder
    private func row(_ reminder: Reminder, selection: Binding<String?>?) -> some View {
        if let selection {
            Button {
                selection.wrappedValue = reminder.id
            } label: {
                rowContent(reminder)
            }
            .buttonStyle(.plain)
            .contentShape(Rectangle())
            .listRowInsets(Self.rowInsets)
            .listRowBackground(
                selection.wrappedValue == reminder.id ? StateTheme.accentSoft : Color.clear
            )
            .swipeActions(edge: .trailing) { archiveAction(reminder) }
            .tag(reminder.id)
        } else {
            NavigationLink(value: reminder.id) {
                rowContent(reminder)
            }
            .listRowInsets(Self.rowInsets)
            .swipeActions(edge: .trailing) { archiveAction(reminder) }
        }
    }

    private func rowContent(_ reminder: Reminder) -> some View {
        let summary = model.occurrenceSummaries[reminder.id]
        return ReminderRow(
            reminder: reminder,
            latestEvent: model.activity.first { $0.reminderID == reminder.id },
            summary: summary,
            onComplete: summary?.next == nil ? nil : {
                Task { await model.completeNextOccurrence(of: reminder) }
            }
        )
    }

    private func archiveAction(_ reminder: Reminder) -> some View {
        Button(role: .destructive) {
            Task { await model.archiveReminder(reminder, archived: true) }
        } label: {
            Label(String(localized: "Archive"), systemImage: "archivebox")
        }
    }

    private static let rowInsets = EdgeInsets(
        top: StateTheme.Space.group,
        leading: StateTheme.Space.block,
        bottom: StateTheme.Space.group,
        trailing: StateTheme.Space.block
    )

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
        let wanted: ReminderBucket = currentMode == .today ? .today : .planned
        return model.reminders.filter { reminder in
            let belongs = ReminderListing.bucket(
                for: reminder,
                summary: model.occurrenceSummaries[reminder.id],
                today: today
            ) == wanted
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
    var summary: OccurrenceSummary? = nil
    /// Nil when there is nothing to check off, such as an undated reminder.
    var onComplete: (() -> Void)? = nil

    @State private var checked = false

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: StateTheme.Space.group) {
            if let onComplete {
                Button {
                    withAnimation(.smooth(duration: 0.2)) { checked = true }
                    Task {
                        try? await Task.sleep(for: .milliseconds(350))
                        onComplete()
                        checked = false
                    }
                } label: {
                    Image(systemName: checked ? "checkmark.circle.fill" : "circle")
                        .font(.title3)
                        .foregroundStyle(checked ? StateTheme.accent : Color.secondary.opacity(0.6))
                        .contentTransition(.symbolEffect(.replace))
                        .frame(width: 28, height: 28)
                        // A 44 point target without moving the circle.
                        .padding(8)
                        .contentShape(Rectangle())
                        .padding(-8)
                }
                .buttonStyle(.plain)
                .accessibilityLabel(String(localized: "Complete reminder"))
            }

            VStack(alignment: .leading, spacing: StateTheme.Space.snug) {
                Text(reminder.title)
                    .font(.headline)
                    .foregroundStyle(checked ? .secondary : StateTheme.graphite)
                    .strikethrough(checked)
                    .lineLimit(2)

                if let description = reminder.description, case let preview = AgentText.rowPreview(description), !preview.isEmpty {
                    Text(preview)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }

                if dueSchedule != nil || originActor != nil {
                    HStack(spacing: StateTheme.Space.inner) {
                        if let dueSchedule {
                            MetaLabel(
                                text: StateDateFormatter.rowLabel(for: dueSchedule),
                                systemImage: reminder.recurrence == nil ? "calendar" : "repeat",
                                tint: isOverdue ? .orange : .secondary
                            )
                        }
                        if let actor = originActor {
                            OriginBadge(actor: actor)
                        }
                    }
                    .padding(.top, StateTheme.Space.hairline)
                }
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("reminder-\(reminder.id)")
    }

    private var dueSchedule: Schedule? {
        ReminderListing.dueSchedule(for: reminder, summary: summary)
    }

    /// Who last touched the reminder, shown only when it was not the owner:
    /// the owner's own changes need no badge on every row.
    private var originActor: Actor? {
        guard let actor = latestEvent?.actor, actor.kind != .owner else { return nil }
        return actor
    }

    /// A due date in the past that nobody has acted on deserves a warmer color.
    private var isOverdue: Bool {
        guard
            reminder.status == .active,
            let dueSchedule,
            let date = StateDateFormatter.date(from: dueSchedule)
        else {
            return false
        }
        return date < Date()
    }
}

/// The Today and Planned switch. On the iPhone it sits in the middle of an
/// inline navigation bar, above the search field, so title, switch and
/// search never overlap. The split layout's narrow column keeps it below the
/// bar, with a background that stops at the bar instead of running under it.
private struct AgendaModeSwitch: ViewModifier {
    let modeSelection: Binding<ReminderCollectionMode>?
    let inNavigationBar: Bool

    func body(content: Content) -> some View {
        if let modeSelection, inNavigationBar {
            content
                .stateInlineNavigationTitle()
                .toolbar {
                    ToolbarItem(placement: .principal) {
                        picker(modeSelection)
                            .frame(maxWidth: 240)
                    }
                }
        } else if let modeSelection {
            content.safeAreaInset(edge: .top, spacing: 0) {
                picker(modeSelection)
                    .padding(.horizontal, StateTheme.Space.block)
                    .padding(.vertical, StateTheme.Space.inner)
                    .background(.bar, ignoresSafeAreaEdges: [])
            }
        } else {
            content
        }
    }

    private func picker(_ selection: Binding<ReminderCollectionMode>) -> some View {
        Picker(String(localized: "Agenda"), selection: selection) {
            Text("Today").tag(ReminderCollectionMode.today)
            Text("Planned").tag(ReminderCollectionMode.planned)
        }
        .pickerStyle(.segmented)
        .labelsHidden()
        .accessibilityIdentifier("agenda-mode")
    }
}
