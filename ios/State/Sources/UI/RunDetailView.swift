import SwiftUI

extension AgentRunStatus {
    var label: String {
        switch self {
        case .planned: String(localized: "Planned")
        case .eligible: String(localized: "Eligible")
        case .claimed: String(localized: "Claimed")
        case .running: String(localized: "Running")
        case .succeeded: String(localized: "Succeeded")
        case .failed: String(localized: "Failed")
        case .cancelled: String(localized: "Cancelled")
        case .needsApproval: String(localized: "Needs approval")
        case .expired: String(localized: "Expired")
        }
    }

    var icon: String {
        switch self {
        case .planned: "calendar.badge.clock"
        case .eligible: "clock"
        case .claimed: "arrow.down.circle"
        case .running: "play.circle.fill"
        case .succeeded: "checkmark.circle.fill"
        case .failed: "xmark.octagon.fill"
        case .cancelled: "slash.circle"
        case .needsApproval: "hand.raised.fill"
        case .expired: "hourglass"
        }
    }

    var color: Color {
        switch self {
        case .planned, .eligible, .cancelled, .expired: .secondary
        case .claimed, .running: StateTheme.accent
        case .succeeded: .green
        case .failed: .red
        case .needsApproval: .orange
        }
    }

    /// Statuses in which the owner may still cancel the run.
    var isActive: Bool {
        switch self {
        case .planned, .eligible, .claimed, .running: true
        default: false
        }
    }
}

/// Compact badge naming the execution policy a reminder runs under. Mirrors
/// the OriginBadge capsule.
struct PolicyBadge: View {
    let policy: ExecutionPolicy?

    var body: some View {
        Label(policy?.name ?? String(localized: "Policy"), systemImage: "cpu")
            .font(.caption.weight(.semibold))
            .foregroundStyle(StateTheme.accent)
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
            .background(StateTheme.accent.opacity(0.12), in: Capsule())
            .accessibilityLabel(String(format: String(localized: "Policy: %@"), policy?.name ?? "–"))
    }
}

struct AgentRunRow: View {
    let run: AgentRun

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Label(run.status.label, systemImage: run.status.icon)
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(run.status.color)
                Spacer()
                Text(run.adapter)
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
            }
            HStack {
                if let requestedAt = run.requestedAt {
                    Text(requestedAt, format: .dateTime.day().month().year().hour().minute())
                }
                if let finishedAt = run.finishedAt {
                    Text("→ \(finishedAt, format: .dateTime.hour().minute())")
                }
            }
            .font(.caption)
            .foregroundStyle(.secondary)
            if let failureCode = run.failureCode, !failureCode.isEmpty {
                Text(failureCode)
                    .font(.caption.monospaced())
                    .foregroundStyle(.red)
            }
            if run.status == .needsApproval, let capability = run.approvalCapability {
                Text(
                    String.localizedStringWithFormat(
                        String(localized: "Waits for approval: %@"),
                        CapabilityCatalog.label(for: capability)
                    )
                )
                    .font(.caption)
                    .foregroundStyle(.orange)
            }
        }
        .padding(.vertical, 4)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("run-\(run.id)")
    }
}

struct RunDetailView: View {
    @Bindable var model: AppModel
    let reminderID: String
    @State private var run: AgentRun
    @State private var events: [AuditEvent] = []
    @State private var confirmsDecline = false
    @State private var confirmsCancel = false
    @State private var isActing = false

    private var isOwner: Bool {
        model.session?.actor.kind == .owner
    }

    init(model: AppModel, reminderID: String, run: AgentRun) {
        self.model = model
        self.reminderID = reminderID
        _run = State(initialValue: run)
    }

    var body: some View {
        List {
            Section {
                VStack(alignment: .leading, spacing: 12) {
                    Label(run.status.label, systemImage: run.status.icon)
                        .font(.title3.bold())
                        .foregroundStyle(run.status.color)
                    Text(run.taskContract.objective)
                        .foregroundStyle(StateTheme.graphite)
                    HStack {
                        Text(run.adapter)
                            .font(.caption.monospaced())
                            .foregroundStyle(.secondary)
                        Text(run.taskContract.projectName)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                .padding(.vertical, 8)
            }

            Section("Times") {
                if let requestedAt = run.requestedAt {
                    LabeledContent("Requested") {
                        Text(requestedAt, format: .dateTime.day().month().year().hour().minute())
                    }
                }
                if let claimedAt = run.claimedAt {
                    LabeledContent("Claimed") {
                        Text(claimedAt, format: .dateTime.day().month().year().hour().minute())
                    }
                }
                if let startedAt = run.startedAt {
                    LabeledContent("Started") {
                        Text(startedAt, format: .dateTime.day().month().year().hour().minute())
                    }
                }
                if let finishedAt = run.finishedAt {
                    LabeledContent("Finished") {
                        Text(finishedAt, format: .dateTime.day().month().year().hour().minute())
                    }
                }
            }

            if run.resultSummary != nil || run.failureCode != nil || run.approvalCapability != nil {
                Section("Result") {
                    if let summary = run.resultSummary, !summary.isEmpty {
                        Text(summary)
                    }
                    if let failureCode = run.failureCode, !failureCode.isEmpty {
                        LabeledContent("Failure code", value: failureCode)
                            .font(.subheadline.monospaced())
                    }
                    if run.status == .needsApproval, let capability = run.approvalCapability {
                        Label(
                            String.localizedStringWithFormat(
                                String(localized: "The runner asks to use: %@"),
                                CapabilityCatalog.label(for: capability)
                            ),
                            systemImage: "hand.raised"
                        )
                        .foregroundStyle(.orange)
                    }
                }
            }

            if isOwner, run.status == .needsApproval || run.status.isActive {
                Section {
                    if run.status == .needsApproval {
                        Button {
                            act { await model.approveRun(run, approved: true) }
                        } label: {
                            Label("Approve", systemImage: "checkmark.circle")
                        }
                        Button(role: .destructive) {
                            confirmsDecline = true
                        } label: {
                            Label("Decline", systemImage: "xmark.circle")
                        }
                    }
                    if run.status.isActive {
                        Button(role: .destructive) {
                            confirmsCancel = true
                        } label: {
                            Label("Cancel run", systemImage: "stop.circle")
                        }
                    }
                }
                .disabled(isActing)
            }

            Section("Timeline") {
                if events.isEmpty {
                    Text("No events yet")
                        .foregroundStyle(.secondary)
                }
                ForEach(events.reversed()) { event in
                    AuditEventRow(event: event)
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Agent run")
        .navigationBarTitleDisplayMode(.inline)
        .task { await reload() }
        .refreshable { await reload() }
        .confirmationDialog("Decline this request?", isPresented: $confirmsDecline) {
            Button("Decline", role: .destructive) {
                act { await model.approveRun(run, approved: false) }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("The run stops without using the capability.")
        }
        .confirmationDialog("Cancel this run?", isPresented: $confirmsCancel) {
            Button("Cancel run", role: .destructive) {
                act { await model.cancelRun(run) }
            }
            Button("Back", role: .cancel) {}
        } message: {
            Text("The runner releases the claim and the run closes as cancelled.")
        }
    }

    private func act(_ mutation: @escaping () async -> Void) {
        isActing = true
        Task {
            await mutation()
            await reload()
            isActing = false
        }
    }

    private func reload() async {
        guard let detail = await model.reminderDetail(id: reminderID) else { return }
        if let fresh = detail.runs.first(where: { $0.id == run.id }) {
            run = fresh
        }
        // The server writes every run event in the contract's correlation
        // scope, so the timeline is a plain filter over the reminder history.
        events = detail.history.filter { $0.correlationID == run.taskContract.correlationID }
    }
}
