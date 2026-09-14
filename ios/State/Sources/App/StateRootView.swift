import SwiftUI

struct StateRootView: View {
    @Bindable var model: AppModel
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @AppStorage("state.onboarding.completed") private var completedOnboarding = false
    @State private var showsSplash = !StateLaunch.isUITesting

    var body: some View {
        ZStack {
            stage
                .tint(StateTheme.accent)
                .background(StateTheme.warmBackground.ignoresSafeArea())
                .modifier(StateLifecycle(model: model))

            if showsSplash {
                SplashView()
                    .transition(.opacity)
                    .zIndex(1)
            }
        }
        .task {
            guard showsSplash else { return }
            try? await Task.sleep(for: .milliseconds(reduceMotion ? 400 : 1_150))
            withAnimation(.easeOut(duration: 0.45)) { showsSplash = false }
        }
        .alert(
            String(localized: "State could not complete the action"),
            isPresented: Binding(
                get: { model.presentedError != nil },
                set: { if !$0 { model.presentedError = nil } }
            )
        ) {
            Button(String(localized: "OK"), role: .cancel) {
                model.presentedError = nil
            }
        } message: {
            Text(model.presentedError ?? "")
        }
    }

    /// Introduction once, then the connection form until a credential exists,
    /// then the app itself.
    @ViewBuilder
    private var stage: some View {
        if showsIntroduction {
            OnboardingFlowView {
                withAnimation(StateTheme.contentChange) { completedOnboarding = true }
            }
            .transition(.opacity)
        } else if model.session == nil {
            ConnectView(model: model)
                .transition(.opacity)
        } else {
            MainTabView(model: model)
                .transition(.opacity)
        }
    }

    private var showsIntroduction: Bool {
        !completedOnboarding && !StateLaunch.isUITesting
    }
}

/// Everything the app has to do because time passes: schedule notifications,
/// resynchronize on foreground, register for push, act on a notification the
/// owner tapped. Kept out of the view body so the body stays readable.
private struct StateLifecycle: ViewModifier {
    @Bindable var model: AppModel
    @Environment(\.scenePhase) private var scenePhase
    @State private var notificationCoordinator = NotificationCoordinator()
    @State private var pushRegistrationService = PushRegistrationService()

    func body(content: Content) -> some View {
        content
            .task {
                await model.start()
                if model.session != nil, !model.isDemo {
                    await notificationCoordinator.activate(model: model)
                }
            }
            .task(id: scenePhase) {
                guard scenePhase == .active else { return }
                while !Task.isCancelled {
                    do { try await Task.sleep(for: .seconds(15)) } catch { return }
                    guard !Task.isCancelled else { return }
                    if model.session?.certificateFingerprint != nil, !model.isDemo {
                        await resynchronize()
                    }
                }
            }
            .onChange(of: model.session?.actor.id) { _, actorID in
                guard actorID != nil, !model.isDemo else { return }
                Task { await notificationCoordinator.activate(model: model) }
            }
            .onChange(of: scenePhase) { _, phase in
                guard phase == .active, model.session != nil, !model.isDemo else { return }
                Task { await resynchronize() }
            }
            .onReceive(NotificationCenter.default.publisher(for: .stateRemoteSync)) { _ in
                Task { await resynchronize() }
            }
            .onReceive(NotificationCenter.default.publisher(for: .stateAPNSToken)) { notification in
                guard !model.isDemo, let token = notification.object as? Data else { return }
                Task { await pushRegistrationService.registerIfSupported(apnsToken: token, model: model) }
            }
            .onReceive(NotificationCenter.default.publisher(for: .stateNotificationAction)) { notification in
                handle(notification)
            }
    }

    private func resynchronize() async {
        await model.synchronize()
        await notificationCoordinator.refresh(model: model)
    }

    private func handle(_ notification: Notification) {
        guard
            let occurrenceID = notification.userInfo?["occurrence_id"] as? String,
            !occurrenceID.isEmpty
        else {
            return
        }
        let action = notification.userInfo?["action"] as? String
        Task {
            switch action {
            case StateNotificationAction.complete:
                await model.completeOccurrence(id: occurrenceID)
            case StateNotificationAction.snoozeTenMinutes:
                await model.snoozeOccurrence(id: occurrenceID, until: Date().addingTimeInterval(10 * 60))
            case StateNotificationAction.snoozeOneHour:
                await model.snoozeOccurrence(id: occurrenceID, until: Date().addingTimeInterval(60 * 60))
            default:
                break
            }
            await notificationCoordinator.refresh(model: model)
        }
    }
}
