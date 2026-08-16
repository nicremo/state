import SwiftUI

struct ActivityView: View {
    @Bindable var model: AppModel
    @State private var showsConflicts = false

    var body: some View {
        NavigationStack {
            Group {
                if model.activity.isEmpty {
                    ContentUnavailableView {
                        Label(String(localized: "No activity yet"), systemImage: "clock.arrow.circlepath")
                    } description: {
                        Text("Every human, device, agent and system change will appear here.")
                    }
                } else {
                    List {
                        if !model.conflicts.isEmpty {
                            Section {
                                Button {
                                    showsConflicts = true
                                } label: {
                                    HStack(spacing: StateTheme.Space.group) {
                                        Image(systemName: "exclamationmark.triangle.fill")
                                            .font(.body)
                                            .foregroundStyle(.orange)
                                        VStack(alignment: .leading, spacing: StateTheme.Space.hairline) {
                                            Text(
                                                String.localizedStringWithFormat(
                                                    String(localized: "Resolve conflicts (%lld)"),
                                                    Int64(model.conflicts.count)
                                                )
                                            )
                                            .font(.subheadline.weight(.semibold))
                                            .foregroundStyle(StateTheme.graphite)
                                            Text("The server and this iPhone changed the same reminder.")
                                                .font(.caption)
                                                .foregroundStyle(.secondary)
                                        }
                                        Spacer(minLength: StateTheme.Space.tight)
                                        Image(systemName: "chevron.right")
                                            .font(.caption.weight(.semibold))
                                            .foregroundStyle(.tertiary)
                                    }
                                }
                                .buttonStyle(.plain)
                            }
                        }
                        Section {
                            ForEach(model.activity) { event in
                                NavigationLink {
                                    ReminderDetailView(model: model, reminderID: event.reminderID)
                                } label: {
                                    AuditEventRow(event: event, title: title(for: event))
                                }
                                .listRowInsets(
                                    EdgeInsets(
                                        top: StateTheme.Space.group,
                                        leading: StateTheme.Space.block,
                                        bottom: StateTheme.Space.group,
                                        trailing: StateTheme.Space.block
                                    )
                                )
                            }
                        }
                    }
                    .listStyle(.insetGrouped)
                    .stateBackground()
                    .animation(StateTheme.contentChange, value: model.activity.map(\.id))
                    .refreshable { await model.synchronize() }
                }
            }
            .navigationTitle("Activity")
            .sheet(isPresented: $showsConflicts) {
                ConflictResolutionView(model: model)
            }
        }
    }

    /// The activity feed spans every reminder, so the row needs to say which
    /// one it is about before it says what happened.
    private func title(for event: AuditEvent) -> String? {
        model.reminders.first { $0.id == event.reminderID }?.title
    }
}

struct ConflictResolutionView: View {
    @Bindable var model: AppModel
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List(model.conflicts) { conflict in
                VStack(alignment: .leading, spacing: StateTheme.Space.group) {
                    VStack(alignment: .leading, spacing: StateTheme.Space.tight) {
                        Text(title(for: conflict))
                            .font(.headline)
                            .foregroundStyle(StateTheme.graphite)
                        Text(
                            String(
                                format: String(localized: "Changed fields: %@"),
                                conflict.fields.joined(separator: ", ")
                            )
                        )
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        Text(conflict.createdAt, format: .dateTime.day().month().hour().minute())
                            .font(.caption2)
                            .foregroundStyle(.tertiary)
                    }
                    HStack(spacing: StateTheme.Space.inner) {
                        Button("Keep my version") {
                            Task { await model.resolveConflict(conflict, keepLocal: true) }
                        }
                        .buttonStyle(.borderedProminent)
                        Button("Use server version") {
                            Task { await model.resolveConflict(conflict, keepLocal: false) }
                        }
                        .buttonStyle(.bordered)
                    }
                    .controlSize(.small)
                }
                .padding(.vertical, StateTheme.Space.snug)
            }
            .animation(StateTheme.contentChange, value: model.conflicts.map(\.id))
            .overlay {
                if model.conflicts.isEmpty {
                    ContentUnavailableView("All resolved", systemImage: "checkmark.circle.fill")
                }
            }
            .navigationTitle("Conflicts")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                }
            }
        }
    }

    private func title(for conflict: StoredConflict) -> String {
        (try? StateJSON.decoder.decode(Reminder.self, from: conflict.localSnapshot).title)
            ?? String(localized: "Reminder conflict")
    }
}
