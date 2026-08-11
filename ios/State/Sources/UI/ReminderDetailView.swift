import SwiftUI

struct ReminderDetailView: View {
    @Bindable var model: AppModel
    let reminderID: String
    @State private var detail: ReminderDetail?
    @State private var comment = ""
    @State private var showsEditor = false
    @State private var confirmsArchive = false

    var body: some View {
        Group {
            if let detail {
                List {
                    Section {
                        VStack(alignment: .leading, spacing: 12) {
                            Text(detail.reminder.title)
                                .font(.title2.bold())
                                .foregroundStyle(StateTheme.graphite)
                            if let description = detail.reminder.description, !description.isEmpty {
                                Text(markdown: description)
                                    .foregroundStyle(.secondary)
                            }
                            HStack {
                                if let schedule = detail.reminder.schedule {
                                    Label(StateDateFormatter.label(for: schedule), systemImage: "calendar")
                                } else {
                                    Label(String(localized: "No date"), systemImage: "calendar.badge.minus")
                                }
                                Spacer()
                                Text("Revision \(detail.reminder.revision)")
                                    .font(.caption.monospaced())
                                    .foregroundStyle(.tertiary)
                            }
                            .font(.subheadline)
                        }
                        .padding(.vertical, 8)
                    }

                    if !detail.occurrences.isEmpty {
                        Section("Occurrences") {
                            ForEach(detail.occurrences) { occurrence in
                                OccurrenceRow(occurrence: occurrence) {
                                    Task {
                                        await model.completeOccurrence(id: occurrence.id)
                                        await reload()
                                    }
                                } onSnooze: {
                                    Task {
                                        await model.snoozeOccurrence(
                                            id: occurrence.id,
                                            until: Date().addingTimeInterval(10 * 60)
                                        )
                                        await reload()
                                    }
                                }
                            }
                        }
                    }

                    Section("Comments") {
                        if detail.comments.isEmpty {
                            Text("No comments yet")
                                .foregroundStyle(.secondary)
                        }
                        ForEach(detail.comments) { item in
                            VStack(alignment: .leading, spacing: 7) {
                                Text(markdown: item.body)
                                HStack {
                                    OriginBadge(actor: item.actor)
                                    Spacer()
                                    Text(item.createdAt, format: .dateTime.day().month().year().hour().minute())
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                }
                            }
                            .padding(.vertical, 4)
                        }
                        HStack(alignment: .bottom) {
                            TextField("Add context", text: $comment, axis: .vertical)
                                .lineLimit(1...5)
                            Button {
                                let body = comment.trimmingCharacters(in: .whitespacesAndNewlines)
                                guard !body.isEmpty else { return }
                                comment = ""
                                Task {
                                    await model.addComment(reminderID: reminderID, body: body)
                                    await reload()
                                }
                            } label: {
                                Image(systemName: "arrow.up.circle.fill")
                                    .font(.title2)
                            }
                            .disabled(comment.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                            .accessibilityLabel("Add comment")
                        }
                    }

                    Section("Complete history") {
                        ForEach(detail.history.reversed()) { event in
                            AuditEventRow(event: event)
                        }
                    }
                }
                .listStyle(.insetGrouped)
                .refreshable {
                    await model.synchronize()
                    await reload()
                }
            } else {
                ProgressView()
            }
        }
        .navigationTitle("Details")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItemGroup(placement: .primaryAction) {
                Button {
                    showsEditor = true
                } label: {
                    Label("Edit", systemImage: "pencil")
                }
                Button {
                    confirmsArchive = true
                } label: {
                    Label("Archive", systemImage: "archivebox")
                }
            }
        }
        .task(id: model.activity.count) { await reload() }
        .sheet(isPresented: $showsEditor) {
            if let reminder = detail?.reminder {
                ReminderEditorView(reminder: reminder) { draft in
                    await model.updateReminder(reminder, draft: draft)
                    await reload()
                }
            }
        }
        .confirmationDialog("Archive this reminder?", isPresented: $confirmsArchive) {
            Button("Archive", role: .destructive) {
                guard let reminder = detail?.reminder else { return }
                Task { await model.archiveReminder(reminder, archived: true) }
            }
            Button("Cancel", role: .cancel) {}
        }
    }

    private func reload() async {
        detail = await model.reminderDetail(id: reminderID)
    }
}

private struct OccurrenceRow: View {
    let occurrence: Occurrence
    let onComplete: () -> Void
    let onSnooze: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Label(dateLabel, systemImage: statusIcon)
                Spacer()
                Text(statusLabel)
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(statusColor)
            }
            if occurrence.status != .completed {
                HStack {
                    Button("Complete", action: onComplete)
                        .buttonStyle(.borderedProminent)
                    Button("Snooze 10 minutes", action: onSnooze)
                        .buttonStyle(.bordered)
                }
                .controlSize(.small)
            }
        }
        .padding(.vertical, 4)
    }

    private var dateLabel: String {
        let schedule = Schedule(
            localDate: occurrence.localDate,
            localTime: occurrence.localTime,
            timeZone: occurrence.timeZone,
            mode: occurrence.timeZoneMode,
            prewarningMinutes: occurrence.prewarningMinutes
        )
        return StateDateFormatter.label(for: schedule)
    }

    private var statusIcon: String {
        switch occurrence.status {
        case .pending: "circle"
        case .completed: "checkmark.circle.fill"
        case .snoozed: "clock.badge"
        }
    }

    private var statusLabel: String {
        switch occurrence.status {
        case .pending: String(localized: "Pending")
        case .completed: String(localized: "Completed")
        case .snoozed: String(localized: "Snoozed")
        }
    }

    private var statusColor: Color {
        switch occurrence.status {
        case .pending: .secondary
        case .completed: .green
        case .snoozed: .orange
        }
    }
}

struct AuditEventRow: View {
    let event: AuditEvent

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            VStack(spacing: 0) {
                Circle()
                    .fill(StateTheme.accent)
                    .frame(width: 9, height: 9)
                Rectangle()
                    .fill(Color.secondary.opacity(0.25))
                    .frame(width: 1, height: 62)
            }
            VStack(alignment: .leading, spacing: 6) {
                Text(actionLabel)
                    .font(.subheadline.weight(.semibold))
                HStack {
                    OriginBadge(actor: event.actor)
                    Text(event.serverTime, format: .dateTime.day().month().year().hour().minute())
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                if !event.changedFields.isEmpty {
                    Text(event.changedFields.joined(separator: ", "))
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)
                }
                if let excerpt = event.sourceExcerpt, !excerpt.isEmpty {
                    Text("“\(excerpt)”")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(3)
                }
            }
        }
        .accessibilityElement(children: .combine)
    }

    private var actionLabel: String {
        switch event.action {
        case "reminder.created": String(localized: "Reminder created")
        case "reminder.updated": String(localized: "Reminder changed")
        case "reminder.archived": String(localized: "Reminder archived")
        case "reminder.restored": String(localized: "Reminder restored")
        case "comment.created": String(localized: "Comment added")
        case "occurrence.completed": String(localized: "Occurrence completed")
        case "occurrence.snoozed": String(localized: "Occurrence snoozed")
        case "conflict.resolved": String(localized: "Conflict resolved")
        default: event.action
        }
    }
}

private extension Text {
    init(markdown: String) {
        if let attributed = try? AttributedString(markdown: markdown) {
            self.init(attributed)
        } else {
            self.init(verbatim: markdown)
        }
    }
}
