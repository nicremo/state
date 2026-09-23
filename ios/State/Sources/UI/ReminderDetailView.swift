import SwiftUI

struct ReminderDetailView: View {
    @Bindable var model: AppModel
    let reminderID: String
    @State private var detail: ReminderDetail?
    @State private var comment = ""
    @State private var showsEditor = false
    @State private var confirmsArchive = false
    @State private var completionFeedback = 0

    var body: some View {
        Group {
            if let detail {
                List {
                    header(detail.reminder)

                    if !detail.occurrences.isEmpty {
                        Section("Occurrences") {
                            ForEach(detail.occurrences) { occurrence in
                                OccurrenceRow(occurrence: occurrence) {
                                    completionFeedback += 1
                                    Task {
                                        await model.completeOccurrence(id: occurrence.id)
                                        await reload()
                                    }
                                } onSnooze: { until in
                                    Task {
                                        await model.snoozeOccurrence(id: occurrence.id, until: until)
                                        await reload()
                                    }
                                }
                            }
                        }
                    }

                    if detail.reminder.executionPolicyID != nil || !detail.runs.isEmpty {
                        Section("Agent runs") {
                            if let policyID = detail.reminder.executionPolicyID {
                                HStack {
                                    PolicyBadge(policy: model.policies.first { $0.id == policyID })
                                    Spacer()
                                }
                            }
                            if detail.runs.isEmpty {
                                Text("No runs yet")
                                    .foregroundStyle(.secondary)
                            }
                            ForEach(detail.runs) { run in
                                NavigationLink {
                                    RunDetailView(model: model, reminderID: reminderID, run: run)
                                } label: {
                                    AgentRunRow(run: run)
                                }
                            }
                            if model.session?.actor.kind == .owner, let policyID = detail.reminder.executionPolicyID {
                                Button {
                                    Task {
                                        await model.triggerManualRun(reminderID: reminderID, policyID: policyID)
                                        await reload()
                                    }
                                } label: {
                                    Label("Run now", systemImage: "play.fill")
                                }
                                .disabled(model.isDemo)
                            }
                        }
                    }

                    commentSection(detail)

                    Section("Complete history") {
                        ForEach(detail.history.reversed()) { event in
                            AuditEventRow(event: event)
                                .listRowInsets(
                                    EdgeInsets(
                                        top: StateTheme.Space.inner,
                                        leading: StateTheme.Space.block,
                                        bottom: StateTheme.Space.inner,
                                        trailing: StateTheme.Space.block
                                    )
                                )
                        }
                    }
                }
                .listStyle(.insetGrouped)
                .stateBackground()
                .animation(StateTheme.contentChange, value: detail.occurrences.map(\.status))
                .refreshable {
                    await model.synchronize()
                    await reload()
                }
            } else {
                ProgressView()
                    .controlSize(.large)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(StateTheme.warmBackground)
            }
        }
        .sensoryFeedback(.success, trigger: completionFeedback)
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
                ReminderEditorView(model: model, reminder: reminder) { draft in
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

    @ViewBuilder
    private func header(_ reminder: Reminder) -> some View {
        Section {
            VStack(alignment: .leading, spacing: StateTheme.Space.inner) {
                Text(reminder.title)
                    .font(.title2.bold())
                    .foregroundStyle(StateTheme.graphite)

                if let description = reminder.description, !description.isEmpty {
                    Text(markdown: description)
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }

                HStack(spacing: StateTheme.Space.inner) {
                    if let schedule = reminder.schedule {
                        MetaLabel(
                            text: StateDateFormatter.label(for: schedule),
                            systemImage: "calendar"
                        )
                    } else {
                        MetaLabel(
                            text: String(localized: "No date"),
                            systemImage: "calendar.badge.minus"
                        )
                    }
                    if let recurrence = reminder.recurrence {
                        MetaLabel(text: label(for: recurrence), systemImage: "repeat")
                    }
                    Spacer(minLength: StateTheme.Space.tight)
                    Text(verbatim: "r\(reminder.revision)")
                        .font(.caption2.monospacedDigit())
                        .foregroundStyle(.tertiary)
                        .accessibilityLabel(
                            String.localizedStringWithFormat(
                                String(localized: "Revision %lld"),
                                reminder.revision
                            )
                        )
                }
                .padding(.top, StateTheme.Space.hairline)
            }
            .padding(.vertical, StateTheme.Space.snug)
        }
    }

    @ViewBuilder
    private func commentSection(_ detail: ReminderDetail) -> some View {
        Section("Comments") {
            if detail.comments.isEmpty {
                Text("No comments yet")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            ForEach(detail.comments) { item in
                VStack(alignment: .leading, spacing: StateTheme.Space.group) {
                    Text(markdown: item.body)
                        .font(.callout)
                    HStack(spacing: StateTheme.Space.inner) {
                        OriginBadge(actor: item.actor)
                        Spacer(minLength: StateTheme.Space.tight)
                        Text(item.createdAt, format: .dateTime.day().month().hour().minute())
                            .font(.caption2)
                            .foregroundStyle(.tertiary)
                    }
                }
                .padding(.vertical, StateTheme.Space.tight)
            }
            HStack(alignment: .bottom, spacing: StateTheme.Space.inner) {
                TextField("Add context", text: $comment, axis: .vertical)
                    .font(.callout)
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
                        .symbolRenderingMode(.hierarchical)
                }
                .buttonStyle(.plain)
                .foregroundStyle(canSend ? StateTheme.accent : Color.secondary)
                .disabled(!canSend)
                .animation(StateTheme.stateChange, value: canSend)
                .accessibilityLabel("Add comment")
            }
        }
    }

    private var canSend: Bool {
        !comment.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private func label(for recurrence: RecurrenceRule) -> String {
        switch recurrence.frequency {
        case .daily: String(localized: "Daily")
        case .weekly: String(localized: "Weekly")
        case .monthly: String(localized: "Monthly")
        case .yearly: String(localized: "Yearly")
        }
    }

    private func reload() async {
        detail = await model.reminderDetail(id: reminderID)
    }
}

/// One generated due date. Completing is the primary act, so it sits on a
/// checkbox at the leading edge with a full touch target of its own; snoozing
/// is a choice between times and therefore a menu, not a second button
/// competing for the same row.
private struct OccurrenceRow: View {
    let occurrence: Occurrence
    let onComplete: () -> Void
    let onSnooze: (Date) -> Void

    var body: some View {
        HStack(spacing: StateTheme.Space.group) {
            Button(action: onComplete) {
                Image(systemName: statusIcon)
                    .font(.title3)
                    .symbolRenderingMode(.hierarchical)
                    .foregroundStyle(statusColor)
                    .frame(width: 44, height: 44)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .disabled(occurrence.status == .completed)
            .accessibilityLabel(String(localized: "Mark as complete"))

            VStack(alignment: .leading, spacing: StateTheme.Space.hairline) {
                Text(dateLabel)
                    .font(.subheadline)
                    .foregroundStyle(StateTheme.graphite)
                Text(statusLabel)
                    .font(.caption)
                    .foregroundStyle(statusColor)
            }

            Spacer(minLength: StateTheme.Space.inner)

            if occurrence.status != .completed {
                Menu {
                    Button("10 minutes") { onSnooze(Date().addingTimeInterval(10 * 60)) }
                    Button("1 hour") { onSnooze(Date().addingTimeInterval(60 * 60)) }
                    Button("Tomorrow morning") { onSnooze(Self.tomorrowMorning()) }
                } label: {
                    Image(systemName: "clock.arrow.circlepath")
                        .font(.body)
                        .foregroundStyle(StateTheme.accent)
                        .frame(width: 44, height: 44)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel(String(localized: "Snooze"))
            }
        }
        .padding(.vertical, -StateTheme.Space.snug)
    }

    private static func tomorrowMorning() -> Date {
        let calendar = Calendar.current
        let tomorrow = calendar.date(byAdding: .day, value: 1, to: Date()) ?? Date()
        return calendar.date(bySettingHour: 9, minute: 0, second: 0, of: tomorrow)
            ?? Date().addingTimeInterval(12 * 60 * 60)
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
        case .snoozed: "clock.badge.fill"
        }
    }

    private var statusLabel: String {
        switch occurrence.status {
        case .pending:
            String(localized: "Pending")
        case .completed:
            String(localized: "Completed")
        case .snoozed:
            if let until = occurrence.snoozedUntil {
                String(
                    format: String(localized: "Snoozed until %@"),
                    until.formatted(date: .omitted, time: .shortened)
                )
            } else {
                String(localized: "Snoozed")
            }
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

/// One entry of the audit chain. In the activity feed the reminder title leads,
/// because the reader is scanning across reminders. Inside a reminder the
/// action leads, because the subject is already known.
struct AuditEventRow: View {
    let event: AuditEvent
    var title: String?

    var body: some View {
        HStack(alignment: .top, spacing: StateTheme.Space.group) {
            marker

            VStack(alignment: .leading, spacing: StateTheme.Space.snug) {
                HStack(alignment: .firstTextBaseline, spacing: StateTheme.Space.inner) {
                    Text(title ?? actionLabel)
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(StateTheme.graphite)
                        .lineLimit(2)
                    Spacer(minLength: StateTheme.Space.tight)
                    Text(event.serverTime, format: .dateTime.day().month().hour().minute())
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                        .layoutPriority(1)
                }

                HStack(spacing: StateTheme.Space.inner) {
                    if title != nil {
                        Text(actionLabel)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    OriginBadge(actor: event.actor)
                }

                if let excerpt = event.sourceExcerpt, !excerpt.isEmpty {
                    Text(verbatim: "“\(excerpt)”")
                        .font(.caption)
                        .italic()
                        .foregroundStyle(.secondary)
                        .lineLimit(3)
                }

                if !event.changedFields.isEmpty {
                    Text(event.changedFields.joined(separator: " · "))
                        .font(.caption2.monospaced())
                        .foregroundStyle(.tertiary)
                        .lineLimit(1)
                }
            }
        }
        .accessibilityElement(children: .combine)
    }

    /// Names the kind of change at a glance, so the feed is scannable without
    /// reading a single label.
    private var marker: some View {
        Image(systemName: symbol)
            .font(.system(size: 12, weight: .semibold))
            .foregroundStyle(StateTheme.accent)
            .frame(width: 26, height: 26)
            .background(StateTheme.accentSoft, in: Circle())
    }

    private var symbol: String {
        switch event.action {
        case "reminder.created": "plus"
        case "reminder.updated": "pencil"
        case "reminder.archived": "archivebox"
        case "reminder.restored": "arrow.uturn.backward"
        case "comment.added": "text.bubble"
        case "occurrence.completed": "checkmark"
        case "occurrence.snoozed": "clock"
        case "conflict.resolved": "arrow.triangle.merge"
        default: "circle"
        }
    }

    private var actionLabel: String {
        switch event.action {
        case "reminder.created": String(localized: "Reminder created")
        case "reminder.updated": String(localized: "Reminder changed")
        case "reminder.archived": String(localized: "Reminder archived")
        case "reminder.restored": String(localized: "Reminder restored")
        case "comment.added": String(localized: "Comment added")
        case "occurrence.completed": String(localized: "Occurrence completed")
        case "occurrence.snoozed": String(localized: "Occurrence snoozed")
        case "conflict.resolved": String(localized: "Conflict resolved")
        case "run.planned": String(localized: "Run planned")
        case "run.eligible": String(localized: "Run eligible")
        case "run.claimed": String(localized: "Run claimed")
        case "run.started": String(localized: "Run started")
        case "run.progress": String(localized: "Run progress")
        case "run.approval_requested": String(localized: "Approval requested")
        case "run.approved": String(localized: "Run approved")
        case "run.declined": String(localized: "Run declined")
        case "run.succeeded": String(localized: "Run succeeded")
        case "run.failed": String(localized: "Run failed")
        case "run.cancelled": String(localized: "Run cancelled")
        case "run.expired": String(localized: "Run expired")
        case "run.requeued": String(localized: "Run requeued")
        case "policy.created": String(localized: "Policy created")
        case "policy.updated": String(localized: "Policy changed")
        case "policy.enabled": String(localized: "Policy enabled")
        case "policy.disabled": String(localized: "Policy disabled")
        case "project.created": String(localized: "Project created")
        case "project.updated": String(localized: "Project changed")
        case "runner.registered": String(localized: "Runner registered")
        case "runner.updated": String(localized: "Runner changed")
        default: event.action
        }
    }
}

extension Text {
    init(markdown: String) {
        if let attributed = try? AttributedString(markdown: markdown) {
            self.init(attributed)
        } else {
            self.init(verbatim: markdown)
        }
    }
}
