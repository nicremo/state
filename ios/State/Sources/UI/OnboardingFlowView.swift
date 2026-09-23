import SwiftUI

/// One welcome screen between the launch mark and the connection. It says what
/// State is in three lines and gets out of the way; notification permission is
/// asked later, after a server is connected and a reminder can actually fire.
struct OnboardingFlowView: View {
    let onFinish: () -> Void

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var revealed = false

    private let points: [(symbol: String, title: String, detail: String)] = [
        (
            "checklist",
            String(localized: "Nothing gets lost"),
            String(localized: "A reminder survives the session it was created in.")
        ),
        (
            "clock.arrow.circlepath",
            String(localized: "Every change is on the record"),
            String(localized: "Who changed what, when, and on whose instruction.")
        ),
        (
            "lock",
            String(localized: "Your server, your data"),
            String(localized: "State only talks to a server you run yourself.")
        ),
    ]

    var body: some View {
        #if os(macOS)
        // A Mac window is large; the welcome stays one centered column with
        // its action directly under the points.
        ScrollView {
            VStack(spacing: StateTheme.Space.stage) {
                header
                pointList
                continueButton
                    .frame(width: 320)
                    .padding(.top, StateTheme.Space.block)
            }
            .frame(maxWidth: 440)
            .padding(StateTheme.Space.stage)
            .frame(maxWidth: .infinity, minHeight: 520)
        }
        .defaultScrollAnchor(.center)
        .background(StateTheme.ground.ignoresSafeArea())
        .onAppear { revealed = true }
        #else
        VStack(spacing: 0) {
            ScrollView {
                VStack(spacing: StateTheme.Space.stage) {
                    header
                        .padding(.top, 56)
                    pointList
                }
                .frame(maxWidth: 440)
                .frame(maxWidth: .infinity)
                .padding(.horizontal, StateTheme.Space.stage)
                .padding(.bottom, StateTheme.Space.section)
            }
            .scrollBounceBehavior(.basedOnSize)

            continueButton
                .frame(maxWidth: 440)
                .padding(.horizontal, StateTheme.Space.stage)
                .padding(.bottom, StateTheme.Space.section)
        }
        .background(StateTheme.ground.ignoresSafeArea())
        .onAppear { revealed = true }
        #endif
    }

    private var header: some View {
        VStack(spacing: StateTheme.Space.section) {
            StateMark(size: 88)
            Text("Welcome to State")
                .font(.largeTitle.weight(.bold))
                .multilineTextAlignment(.center)
                .foregroundStyle(StateTheme.graphite)
        }
    }

    private var pointList: some View {
        VStack(alignment: .leading, spacing: StateTheme.Space.section) {
            ForEach(Array(points.enumerated()), id: \.offset) { index, point in
                WelcomePoint(symbol: point.symbol, title: point.title, detail: point.detail)
                    .opacity(revealed ? 1 : 0)
                    .offset(y: revealed ? 0 : 10)
                    .animation(
                        reduceMotion ? nil : .smooth(duration: 0.5).delay(0.12 + Double(index) * 0.08),
                        value: revealed
                    )
            }
        }
        .padding(.horizontal, StateTheme.Space.tight)
    }

    private var continueButton: some View {
        Button {
            onFinish()
        } label: {
            Text("Continue")
        }
        .buttonStyle(.statePrimary)
        .keyboardShortcut(.defaultAction)
        .accessibilityIdentifier("welcome-continue")
    }
}

private struct WelcomePoint: View {
    let symbol: String
    let title: String
    let detail: String

    var body: some View {
        HStack(alignment: .top, spacing: StateTheme.Space.block) {
            Image(systemName: symbol)
                .font(.title2)
                .foregroundStyle(StateTheme.accent)
                .frame(width: 36, alignment: .center)
                .accessibilityHidden(true)

            VStack(alignment: .leading, spacing: StateTheme.Space.hairline) {
                Text(title)
                    .font(.headline)
                    .foregroundStyle(StateTheme.graphite)
                Text(detail)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .accessibilityElement(children: .combine)
    }
}
