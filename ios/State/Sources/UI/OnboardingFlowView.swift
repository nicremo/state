import SwiftUI
import UserNotifications

/// The three screens between the launch mark and the connection form. They
/// answer what State is, where the pieces live, and why notifications matter,
/// because none of that is guessable from a field asking for a server URL.
struct OnboardingFlowView: View {
    let onFinish: () -> Void

    @State private var page = 0
    @State private var isRequestingNotifications = false
    @State private var notificationsHandled = false

    private let lastPage = 2

    var body: some View {
        VStack(spacing: 0) {
            header

            TabView(selection: $page) {
                purpose.tag(0)
                topology.tag(1)
                notifications.tag(2)
            }
            .tabViewStyle(.page(indexDisplayMode: .never))
            .animation(StateTheme.contentChange, value: page)

            footer
        }
        .background(StateTheme.warmBackground.ignoresSafeArea())
    }

    // MARK: Chrome

    private var header: some View {
        HStack {
            HStack(spacing: StateTheme.Space.snug) {
                ForEach(0...lastPage, id: \.self) { index in
                    Capsule()
                        .fill(index == page ? StateTheme.accent : Color.secondary.opacity(0.25))
                        .frame(width: index == page ? 20 : 6, height: 6)
                }
            }
            .animation(StateTheme.stateChange, value: page)
            .accessibilityHidden(true)

            Spacer()

            Button("Skip") { onFinish() }
                .font(.subheadline)
                .foregroundStyle(.secondary)
        }
        .padding(.horizontal, StateTheme.Space.section)
        .padding(.top, StateTheme.Space.block)
    }

    @ViewBuilder
    private var footer: some View {
        VStack(spacing: StateTheme.Space.group) {
            if page < lastPage {
                Button {
                    page += 1
                } label: {
                    Text("Continue")
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, StateTheme.Space.snug)
                }
                .buttonStyle(.borderedProminent)
            } else {
                Button {
                    requestNotifications()
                } label: {
                    HStack(spacing: StateTheme.Space.inner) {
                        if isRequestingNotifications {
                            ProgressView()
                                .controlSize(.small)
                                .tint(.white)
                        }
                        Text(notificationsHandled ? "Continue" : "Allow notifications")
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, StateTheme.Space.snug)
                }
                .buttonStyle(.borderedProminent)
                .disabled(isRequestingNotifications)

                Button("Not now") { onFinish() }
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .opacity(notificationsHandled ? 0 : 1)
            }
        }
        .animation(StateTheme.stateChange, value: page)
        .padding(.horizontal, StateTheme.Space.section)
        .padding(.bottom, StateTheme.Space.section)
    }

    // MARK: Pages

    private var purpose: some View {
        OnboardingPage(
            title: String(localized: "Your agents remember. You stay in control."),
            subtitle: String(localized: "Codex, Claude Code and OpenCode write reminders into State. You see every one of them on your iPhone, with the exact wording that caused it.")
        ) {
            VStack(alignment: .leading, spacing: StateTheme.Space.block) {
                OnboardingPoint(
                    systemImage: "text.bubble",
                    title: String(localized: "Nothing gets lost"),
                    detail: String(localized: "A reminder survives the session it was created in.")
                )
                OnboardingPoint(
                    systemImage: "list.bullet.rectangle.portrait",
                    title: String(localized: "Every change is on the record"),
                    detail: String(localized: "Who changed what, when, and on whose instruction.")
                )
                OnboardingPoint(
                    systemImage: "lock.shield",
                    title: String(localized: "Your server, your data"),
                    detail: String(localized: "State talks to a server you run. Nothing goes anywhere else.")
                )
            }
        }
    }

    private var topology: some View {
        OnboardingPage(
            title: String(localized: "Three pieces, one memory"),
            subtitle: String(localized: "State needs a server of your own. Your agents and this iPhone both talk to it.")
        ) {
            VStack(spacing: 0) {
                OnboardingNode(
                    systemImage: "terminal",
                    title: String(localized: "Your agents"),
                    detail: String(localized: "On your Mac or Windows machine, paired with one command.")
                )
                OnboardingConnector()
                OnboardingNode(
                    systemImage: "server.rack",
                    title: String(localized: "Your State server"),
                    detail: String(localized: "One binary on a machine you control. It keeps the audit chain.")
                )
                OnboardingConnector()
                OnboardingNode(
                    systemImage: "iphone",
                    title: String(localized: "This iPhone"),
                    detail: String(localized: "Reads offline, queues your changes, notifies you when something is due.")
                )
            }
        }
    }

    private var notifications: some View {
        OnboardingPage(
            title: String(localized: "Let State reach you"),
            subtitle: String(localized: "A reminder that cannot interrupt you is only a list entry. State schedules its alerts on this iPhone and marks them time sensitive, so they get through Focus modes.")
        ) {
            VStack(alignment: .leading, spacing: StateTheme.Space.block) {
                OnboardingPoint(
                    systemImage: "bell.badge",
                    title: String(localized: "Alerts are scheduled locally"),
                    detail: String(localized: "They fire even while your server is unreachable.")
                )
                OnboardingPoint(
                    systemImage: "lock.shield",
                    title: String(localized: "Remote push stays encrypted"),
                    detail: String(localized: "The shared relay never sees your reminder text.")
                )
            }
        }
    }

    private func requestNotifications() {
        guard !notificationsHandled else {
            onFinish()
            return
        }
        isRequestingNotifications = true
        Task {
            _ = try? await UNUserNotificationCenter.current()
                .requestAuthorization(options: NotificationCoordinator.authorizationOptions)
            isRequestingNotifications = false
            notificationsHandled = true
            onFinish()
        }
    }
}

/// One onboarding screen: a leading statement, a supporting sentence, and the
/// content that proves it.
private struct OnboardingPage<Content: View>: View {
    let title: String
    let subtitle: String
    @ViewBuilder let content: Content

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: StateTheme.Space.section) {
                VStack(alignment: .leading, spacing: StateTheme.Space.group) {
                    Text(title)
                        .font(.title.bold())
                        .foregroundStyle(StateTheme.graphite)
                    Text(subtitle)
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }
                content
            }
            .frame(maxWidth: 560, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, StateTheme.Space.section)
            .padding(.top, StateTheme.Space.stage)
            .padding(.bottom, StateTheme.Space.section)
        }
    }
}

private struct OnboardingPoint: View {
    let systemImage: String
    let title: String
    let detail: String

    var body: some View {
        HStack(alignment: .top, spacing: StateTheme.Space.group) {
            Image(systemName: systemImage)
                .font(.system(size: 15, weight: .medium))
                .foregroundStyle(StateTheme.accent)
                .frame(width: 32, height: 32)
                .background(StateTheme.accentSoft, in: RoundedRectangle(cornerRadius: 9, style: .continuous))

            VStack(alignment: .leading, spacing: StateTheme.Space.hairline) {
                Text(title)
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(StateTheme.graphite)
                Text(detail)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .accessibilityElement(children: .combine)
    }
}

private struct OnboardingNode: View {
    let systemImage: String
    let title: String
    let detail: String

    var body: some View {
        HStack(alignment: .top, spacing: StateTheme.Space.group) {
            Image(systemName: systemImage)
                .font(.system(size: 16, weight: .medium))
                .foregroundStyle(StateTheme.accent)
                .frame(width: 38, height: 38)
                .background(StateTheme.accentSoft, in: Circle())

            VStack(alignment: .leading, spacing: StateTheme.Space.hairline) {
                Text(title)
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(StateTheme.graphite)
                Text(detail)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .padding(.top, StateTheme.Space.snug)

            Spacer(minLength: 0)
        }
        .accessibilityElement(children: .combine)
    }
}

private struct OnboardingConnector: View {
    var body: some View {
        HStack {
            Rectangle()
                .fill(Color.secondary.opacity(0.25))
                .frame(width: 1, height: 22)
                .padding(.leading, 19)
            Spacer()
        }
        .accessibilityHidden(true)
    }
}
