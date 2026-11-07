import SwiftUI

/// The Agent tab: every session a runner works on for the owner, the active
/// ones first. On the iPhone a row opens the conversation; the split layout
/// passes a selection and shows it in its detail column.
struct AgentSessionsView: View {
    @Bindable var model: AppModel
    var selection: Binding<String?>?
    @State private var path: [String] = []
    @State private var startsTask = false

    var body: some View {
        Group {
            if let selection {
                list(selection: selection)
            } else {
                NavigationStack(path: $path) {
                    list(selection: nil)
                        .navigationDestination(for: String.self) { sessionID in
                            AgentSessionDetailView(model: model, sessionID: sessionID)
                        }
                }
            }
        }
        .task { await model.loadAgentSessions() }
        .task(id: model.agentSessions.contains { $0.status == .working || $0.status == .needsApproval }) {
            // While a round runs, the list follows it without a manual refresh.
            while !Task.isCancelled, model.agentSessions.contains(where: { $0.status == .working || $0.status == .needsApproval }) {
                try? await Task.sleep(for: .seconds(5))
                guard !Task.isCancelled else { return }
                await model.loadAgentSessions()
            }
        }
        .sheet(isPresented: $startsTask) {
            StartAgentSessionSheet(model: model, reminder: nil)
        }
        .onAppear(perform: openRequestedSession)
        .onChange(of: model.agentSessionToOpen) { _, _ in openRequestedSession() }
    }

    private func openRequestedSession() {
        guard let id = model.agentSessionToOpen else { return }
        model.agentSessionToOpen = nil
        if let selection {
            selection.wrappedValue = id
        } else {
            path = [id]
        }
    }

    @ViewBuilder
    private func list(selection: Binding<String?>?) -> some View {
        let active = model.agentSessions.filter { !$0.closed }
        let closed = model.agentSessions.filter(\.closed)
        Group {
            if model.agentSessions.isEmpty {
                ContentUnavailableView {
                    Label(String(localized: "No agent sessions yet"), systemImage: "terminal")
                } description: {
                    Text("Tap + to give an agent a task. It works on your Mac; its answer appears here and you can reply.")
                }
                .accessibilityIdentifier("agent-empty")
            } else {
                List(selection: selection) {
                    if !active.isEmpty {
                        Section(String(localized: "Active")) {
                            ForEach(active) { row($0, selection: selection) }
                        }
                    }
                    if !closed.isEmpty {
                        Section(String(localized: "Ended")) {
                            ForEach(closed) { row($0, selection: selection) }
                        }
                    }
                }
                .stateListStyle()
            }
        }
        .stateBackground()
        .navigationTitle(String(localized: "Agent"))
        .refreshable { await model.loadAgentSessions() }
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button {
                    startsTask = true
                } label: {
                    Label(String(localized: "New task"), systemImage: "plus")
                }
                .disabled(model.isDemo)
                .accessibilityIdentifier("agent-new-task")
            }
        }
    }

    @ViewBuilder
    private func row(_ session: AgentSession, selection: Binding<String?>?) -> some View {
        if let selection {
            Button {
                selection.wrappedValue = session.id
            } label: {
                AgentSessionRow(session: session)
            }
            .buttonStyle(.plain)
            .tag(session.id)
        } else {
            NavigationLink(value: session.id) {
                AgentSessionRow(session: session)
            }
        }
    }
}

struct AgentSessionRow: View {
    let session: AgentSession

    var body: some View {
        VStack(alignment: .leading, spacing: StateTheme.Space.snug) {
            HStack(alignment: .firstTextBaseline) {
                Text(session.title)
                    .font(.body.weight(.semibold))
                    .foregroundStyle(StateTheme.graphite)
                    .lineLimit(2)
                Spacer(minLength: StateTheme.Space.inner)
                if let date = session.lastActivityAt {
                    Text(date, format: .relative(presentation: .named))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            HStack(spacing: StateTheme.Space.inner) {
                // Ended sessions sit in their own section; the pill would
                // only repeat its title.
                if !session.closed {
                    AgentStatusPill(status: session.status)
                }
                Text([session.agentName, session.projectName].compactMap { $0 }.joined(separator: " · "))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            if let answer = session.lastAnswer {
                Text(AgentText.preview(answer))
                    .font(.subheadline)
                    .foregroundStyle(StateTheme.graphite.opacity(0.75))
                    .lineLimit(2)
            }
        }
        .padding(.vertical, StateTheme.Space.tight)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("agent-session-row")
    }
}

struct AgentStatusPill: View {
    let status: AgentSessionStatus

    var body: some View {
        Label(title, systemImage: symbol)
            .labelStyle(.titleAndIcon)
            .font(.caption.weight(.semibold))
            .foregroundStyle(tint)
            .padding(.horizontal, StateTheme.Space.inner)
            .padding(.vertical, StateTheme.Space.hairline)
            .background(tint.opacity(0.12), in: Capsule())
            .accessibilityIdentifier("agent-status-\(status.rawValue)")
    }

    private var title: String {
        switch status {
        case .working: String(localized: "Working")
        case .needsApproval: String(localized: "Needs approval")
        case .waiting: String(localized: "Waiting for you")
        case .failed: String(localized: "Failed")
        case .closed: String(localized: "Ended")
        }
    }

    private var symbol: String {
        switch status {
        case .working: "hourglass"
        case .needsApproval: "hand.raised"
        case .waiting: "bubble.left"
        case .failed: "exclamationmark.triangle"
        case .closed: "checkmark"
        }
    }

    private var tint: Color {
        switch status {
        case .working: StateTheme.accent
        case .needsApproval, .failed: .orange
        case .waiting: .green
        case .closed: .secondary
        }
    }
}

/// Helpers for agent text.
enum AgentText {
    /// A one-paragraph preview of a Markdown answer for list rows.
    static func preview(_ markdown: String) -> String {
        markdown
            .split(separator: "\n")
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
            .map { line in
                var text = line
                while let first = text.first, "#>-*".contains(first) {
                    text.removeFirst()
                }
                return text.replacingOccurrences(of: "**", with: "").trimmingCharacters(in: .whitespaces)
            }
            .filter { !$0.isEmpty }
            .joined(separator: " ")
    }

    /// A plain preview for reminder rows: Markdown headings such as
    /// "## Objective" are structure, not content, and are left out.
    static func rowPreview(_ markdown: String) -> String {
        let body = markdown
            .split(separator: "\n")
            .filter { !$0.trimmingCharacters(in: .whitespaces).hasPrefix("#") }
            .joined(separator: "\n")
        return preview(body).replacingOccurrences(of: "`", with: "")
    }

    /// What the owner's rights setting lets the agent do, from the policy's
    /// capabilities; the runner uses the same rule.
    static func rights(_ capabilities: [String]) -> String {
        let full: Set<String> = ["run_tests", "network_access", "write_state", "deploy", "message_external", "destructive"]
        if capabilities.contains(where: full.contains) {
            return String(localized: "May edit files and run commands without asking.")
        }
        if capabilities.contains("edit_repository") {
            return String(localized: "May edit files in the project, no commands.")
        }
        return String(localized: "May only read the project.")
    }
}
