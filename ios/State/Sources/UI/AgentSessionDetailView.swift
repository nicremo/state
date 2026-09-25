import SwiftUI
#if canImport(UIKit)
import UIKit
#elseif canImport(AppKit)
import AppKit
#endif

/// One session as a conversation: the owner's messages, the agent's answers
/// as Markdown, what the agent is doing right now, and a field to answer
/// once it waits. The menu opens the session in a terminal on the Mac.
struct AgentSessionDetailView: View {
    @Bindable var model: AppModel
    let sessionID: String
    @State private var draft = ""
    @State private var sending = false
    @State private var confirmsClose = false
    @FocusState private var composerFocused: Bool

    private var session: AgentSession? { model.agentSessions.first { $0.id == sessionID } }

    var body: some View {
        Group {
            if let session {
                conversation(session)
            } else {
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .stateBackground()
        .navigationTitle(session?.title ?? String(localized: "Agent"))
        .stateInlineNavigationTitle()
        .toolbar { toolbar }
        .task(id: sessionID) {
            await model.refreshAgentSession(id: sessionID)
            // Follow a running round until the agent answers.
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(5))
                guard !Task.isCancelled else { return }
                if let status = session?.status, status == .working || status == .needsApproval {
                    await model.refreshAgentSession(id: sessionID)
                }
            }
        }
        .confirmationDialog(String(localized: "End this session?"), isPresented: $confirmsClose) {
            Button(String(localized: "End session"), role: .destructive) {
                if let session { Task { await model.closeAgentSession(session) } }
            }
            Button(String(localized: "Cancel"), role: .cancel) {}
        } message: {
            Text("The agent keeps its work. You can open the session on the Mac later, but not answer here any more.")
        }
        .accessibilityIdentifier("agent-session-detail")
    }

    private func conversation(_ session: AgentSession) -> some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: StateTheme.Space.section) {
                    header(session)
                    ForEach(Array(session.turns.enumerated()), id: \.element.id) { index, turn in
                        turnView(turn, isFirst: index == 0, session: session)
                    }
                    Color.clear.frame(height: 1).id("bottom")
                }
                .padding(StateTheme.Space.section)
            }
            .onChange(of: session.turns.map(\.status)) { _, _ in
                withAnimation(StateTheme.contentChange) { proxy.scrollTo("bottom", anchor: .bottom) }
            }
            .onAppear { proxy.scrollTo("bottom", anchor: .bottom) }
            .refreshable { await model.refreshAgentSession(id: sessionID) }
            .safeAreaInset(edge: .bottom, spacing: 0) { composer(session) }
        }
    }

    private func header(_ session: AgentSession) -> some View {
        HStack(spacing: StateTheme.Space.inner) {
            AgentStatusPill(status: session.status)
            Text([session.agentName, session.projectName].compactMap { $0 }.joined(separator: " · "))
                .font(.caption)
                .foregroundStyle(.secondary)
            Spacer(minLength: 0)
        }
    }

    @ViewBuilder
    private func turnView(_ turn: AgentRun, isFirst: Bool, session: AgentSession) -> some View {
        if turn.turnKind == .openTerminal {
            Label(turn.isFinished ? (turn.status == .succeeded ? String(localized: "Opened in a terminal on the Mac") : String(localized: "Could not open the terminal")) : String(localized: "Opening on the Mac"), systemImage: "terminal")
                .font(.caption)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity)
                .accessibilityIdentifier("agent-open-turn")
        } else {
            VStack(alignment: .leading, spacing: StateTheme.Space.block) {
                if let prompt = turn.prompt, !prompt.isEmpty {
                    ownerMessage(prompt)
                } else if isFirst {
                    Text("Started on the reminder's task")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, alignment: .trailing)
                }
                agentPart(turn, session: session)
            }
        }
    }

    private func ownerMessage(_ text: String) -> some View {
        HStack {
            Spacer(minLength: 48)
            Text(text)
                .font(.body)
                .foregroundStyle(StateTheme.graphite)
                .textSelection(.enabled)
                .padding(StateTheme.Space.group)
                .background(StateTheme.accentSoft, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
                .accessibilityIdentifier("agent-owner-message")
        }
    }

    @ViewBuilder
    private func agentPart(_ turn: AgentRun, session: AgentSession) -> some View {
        switch turn.status {
        case .needsApproval:
            approvalCard(turn)
        case .eligible, .planned:
            progressLine(String(localized: "Waiting for your Mac to pick up this round"), since: turn.requestedAt ?? turn.createdAt)
        case .claimed, .running:
            progressLine(String(localized: "\(session.agentName) is working"), since: turn.startedAt ?? turn.claimedAt ?? turn.createdAt)
        case .succeeded, .failed, .cancelled, .expired:
            answerCard(turn, session: session)
        }
    }

    private func progressLine(_ text: String, since start: Date) -> some View {
        HStack(spacing: StateTheme.Space.inner) {
            ProgressView().controlSize(.small)
            Text(text)
                .font(.subheadline)
                .foregroundStyle(StateTheme.graphite)
            Spacer(minLength: 0)
            Text(start, style: .timer)
                .font(.caption.monospacedDigit())
                .foregroundStyle(.secondary)
        }
        .padding(StateTheme.Space.group)
        .background(StateTheme.accentSoft.opacity(0.6), in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("agent-working")
    }

    private func answerCard(_ turn: AgentRun, session: AgentSession) -> some View {
        VStack(alignment: .leading, spacing: StateTheme.Space.inner) {
            HStack {
                Label(session.agentName, systemImage: "terminal")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                Spacer()
                if let finished = turn.finishedAt {
                    Text(finished, format: .dateTime.hour().minute())
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            if let text = turn.resultText, !text.isEmpty {
                MarkdownView(text, style: .document)
                    .textSelection(.enabled)
            }
            switch turn.status {
            case .failed, .expired:
                Label(failureText(turn), systemImage: "exclamationmark.triangle")
                    .font(.footnote)
                    .foregroundStyle(.orange)
            case .cancelled:
                Label(String(localized: "Round cancelled"), systemImage: "xmark.circle")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            default:
                EmptyView()
            }
        }
        .padding(StateTheme.Space.group)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(StateTheme.ground, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 14, style: .continuous).stroke(StateTheme.graphite.opacity(0.08)))
        .accessibilityIdentifier("agent-answer")
    }

    private func failureText(_ turn: AgentRun) -> String {
        if turn.failureCode == "adapter_unavailable" {
            return String(localized: "The agent could not start on the Mac. Check that it is installed and the project is set up for the runner.")
        }
        if turn.status == .expired {
            return String(localized: "No Mac picked up this round in time.")
        }
        if (turn.resultText ?? "").isEmpty, let summary = turn.resultSummary, !summary.isEmpty {
            return summary
        }
        return String(localized: "The round ended with an error.")
    }

    private func approvalCard(_ turn: AgentRun) -> some View {
        VStack(alignment: .leading, spacing: StateTheme.Space.inner) {
            Label(String(localized: "The agent asks for a permission"), systemImage: "hand.raised")
                .font(.subheadline.weight(.semibold))
            if let capability = turn.approvalCapability {
                Text(capability)
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
            }
            HStack(spacing: StateTheme.Space.inner) {
                Button(String(localized: "Allow")) {
                    Task {
                        await model.approveRun(turn, approved: true)
                        await model.refreshAgentSession(id: sessionID)
                    }
                }
                .buttonStyle(.statePill)
                Button(String(localized: "Decline"), role: .destructive) {
                    Task {
                        await model.approveRun(turn, approved: false)
                        await model.refreshAgentSession(id: sessionID)
                    }
                }
                .buttonStyle(.plain)
                .foregroundStyle(.orange)
            }
        }
        .padding(StateTheme.Space.group)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.orange.opacity(0.1), in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .accessibilityIdentifier("agent-approval")
    }

    @ViewBuilder
    private func composer(_ session: AgentSession) -> some View {
        VStack(spacing: StateTheme.Space.tight) {
            Divider()
            if session.closed {
                Text("This session has ended.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .padding(StateTheme.Space.group)
            } else {
                if !session.canSend {
                    Text("You can answer as soon as the agent has finished its round.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .padding(.top, StateTheme.Space.tight)
                }
                HStack(alignment: .bottom, spacing: StateTheme.Space.inner) {
                    TextField(String(localized: "Answer the agent"), text: $draft, axis: .vertical)
                        .lineLimit(1...6)
                        .textFieldStyle(.plain)
                        .focused($composerFocused)
                        .padding(StateTheme.Space.inner)
                        .background(StateTheme.accentSoft.opacity(0.6), in: RoundedRectangle(cornerRadius: 18, style: .continuous))
                        .accessibilityIdentifier("agent-composer")
                    Button {
                        Task { await send(session) }
                    } label: {
                        Image(systemName: sending ? "hourglass" : "arrow.up.circle.fill")
                            .font(.title)
                            .foregroundStyle(canSubmit(session) ? StateTheme.accent : Color.secondary)
                            .frame(width: StateControlMetrics.tapTarget, height: StateControlMetrics.tapTarget)
                    }
                    .buttonStyle(.plain)
                    .disabled(!canSubmit(session))
                    .accessibilityLabel(String(localized: "Send"))
                    .accessibilityIdentifier("agent-send")
                }
                .padding(.horizontal, StateTheme.Space.block)
                .padding(.bottom, StateTheme.Space.inner)
            }
        }
        .background(.bar)
    }

    private func canSubmit(_ session: AgentSession) -> Bool {
        session.canSend && !sending && !draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private func send(_ session: AgentSession) async {
        sending = true
        if await model.sendAgentMessage(draft, to: session) {
            draft = ""
            composerFocused = false
        }
        sending = false
    }

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        if let session {
            ToolbarItem(placement: .primaryAction) {
                Menu {
                    Button {
                        Task { await model.openAgentSessionOnMac(session) }
                    } label: {
                        Label(String(localized: "Open on the Mac"), systemImage: "macbook")
                    }
                    .disabled(!session.canOpenOnMac)
                    if let command = session.resumeCommand {
                        Button {
                            copy(command)
                        } label: {
                            Label(String(localized: "Copy command"), systemImage: "doc.on.doc")
                        }
                    }
                    if session.activeTurn != nil {
                        Button(role: .destructive) {
                            Task { await model.cancelAgentTurn(in: session) }
                        } label: {
                            Label(String(localized: "Cancel round"), systemImage: "stop.circle")
                        }
                    }
                    if !session.closed {
                        Button(role: .destructive) {
                            confirmsClose = true
                        } label: {
                            Label(String(localized: "End session"), systemImage: "checkmark.circle")
                        }
                        .disabled(session.activeTurn != nil)
                    }
                } label: {
                    Label(String(localized: "Session actions"), systemImage: "ellipsis.circle")
                }
                .accessibilityIdentifier("agent-session-menu")
            }
        }
    }

    private func copy(_ text: String) {
        #if canImport(UIKit)
        UIPasteboard.general.string = text
        #elseif canImport(AppKit)
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        #endif
    }
}
