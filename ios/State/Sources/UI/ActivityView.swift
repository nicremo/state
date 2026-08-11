import SwiftUI

struct ActivityView: View {
    @Bindable var model: AppModel
    @State private var showsConflicts = false

    var body: some View {
        NavigationStack {
            Group {
                if model.activity.isEmpty {
                    ContentUnavailableView(
                        String(localized: "No activity yet"),
                        systemImage: "clock.arrow.circlepath",
                        description: Text("Every human, device, agent and system change will appear here.")
                    )
                } else {
                    List {
                        if !model.conflicts.isEmpty {
                            Section {
                                Button {
                                    showsConflicts = true
                                } label: {
                                    Label(
                                        String(localized: "Resolve \(model.conflicts.count) conflicts"),
                                        systemImage: "exclamationmark.triangle.fill"
                                    )
                                    .foregroundStyle(.orange)
                                }
                            }
                        }
                        Section {
                            ForEach(model.activity) { event in
                                NavigationLink {
                                    ReminderDetailView(model: model, reminderID: event.reminderID)
                                } label: {
                                    AuditEventRow(event: event)
                                }
                            }
                        }
                    }
                    .listStyle(.insetGrouped)
                    .refreshable { await model.synchronize() }
                }
            }
            .navigationTitle("Activity")
            .sheet(isPresented: $showsConflicts) {
                ConflictResolutionView(model: model)
            }
        }
    }
}

struct ConflictResolutionView: View {
    @Bindable var model: AppModel
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List(model.conflicts) { conflict in
                VStack(alignment: .leading, spacing: 12) {
                    Text(title(for: conflict))
                        .font(.headline)
                    Text("Changed fields: \(conflict.fields.joined(separator: ", "))")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Text(conflict.createdAt, format: .dateTime.day().month().year().hour().minute())
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    HStack {
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
                .padding(.vertical, 6)
            }
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
