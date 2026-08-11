import SwiftUI

struct MainTabView: View {
    @Bindable var model: AppModel

    var body: some View {
        TabView {
            ReminderCollectionView(model: model, mode: .today)
                .tabItem { Label(String(localized: "Today"), systemImage: "sun.max.fill") }

            ReminderCollectionView(model: model, mode: .planned)
                .tabItem { Label(String(localized: "Planned"), systemImage: "calendar") }

            ActivityView(model: model)
                .tabItem { Label(String(localized: "Activity"), systemImage: "clock.arrow.circlepath") }

            SettingsView(model: model)
                .tabItem { Label(String(localized: "Settings"), systemImage: "gearshape") }
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
    @State private var search = ""
    @State private var showsEditor = false

    var body: some View {
        NavigationStack {
            Group {
                if filteredReminders.isEmpty {
                    ContentUnavailableView(
                        search.isEmpty ? String(localized: "Nothing pending") : String(localized: "No results"),
                        systemImage: search.isEmpty ? "checkmark.circle" : "magnifyingglass",
                        description: Text(search.isEmpty
                            ? String(localized: "Agent and app reminders appear here.")
                            : String(localized: "Try a different search term."))
                    )
                } else {
                    List(filteredReminders) { reminder in
                        NavigationLink {
                            ReminderDetailView(model: model, reminderID: reminder.id)
                        } label: {
                            ReminderRow(
                                reminder: reminder,
                                latestEvent: model.activity.first { $0.reminderID == reminder.id }
                            )
                        }
                        .swipeActions(edge: .trailing) {
                            Button(role: .destructive) {
                                Task { await model.archiveReminder(reminder, archived: true) }
                            } label: {
                                Label(String(localized: "Archive"), systemImage: "archivebox")
                            }
                        }
                    }
                    .listStyle(.insetGrouped)
                    .refreshable { await model.synchronize() }
                }
            }
            .navigationTitle(mode.title)
            .searchable(text: $search, prompt: String(localized: "Search reminders"))
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    if model.isSyncing {
                        ProgressView()
                            .accessibilityLabel(String(localized: "Synchronizing"))
                    }
                }
                ToolbarItem(placement: .primaryAction) {
                    Button {
                        showsEditor = true
                    } label: {
                        Label(String(localized: "New reminder"), systemImage: "plus")
                    }
                }
            }
            .sheet(isPresented: $showsEditor) {
                ReminderEditorView { draft in
                    await model.createReminder(draft)
                }
            }
        }
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
        VStack(alignment: .leading, spacing: 8) {
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

            HStack(spacing: 8) {
                if let schedule = reminder.schedule {
                    Label(StateDateFormatter.label(for: schedule), systemImage: "calendar")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                if let actor = latestEvent?.actor {
                    OriginBadge(actor: actor)
                }
                Spacer(minLength: 0)
                Text(verbatim: "r\(reminder.revision)")
                    .font(.caption2.monospaced())
                    .foregroundStyle(.tertiary)
            }
        }
        .padding(.vertical, 5)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("reminder-\(reminder.id)")
    }
}
