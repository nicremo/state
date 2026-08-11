import SwiftUI

struct StateRootView: View {
    @Bindable var model: AppModel
    @Environment(\.scenePhase) private var scenePhase
    @State private var notificationCoordinator = NotificationCoordinator()
    @State private var pushRegistrationService = PushRegistrationService()

    var body: some View {
        Group {
            if model.session == nil {
                OnboardingView(model: model)
            } else {
                MainTabView(model: model)
            }
        }
        .tint(StateTheme.accent)
        .background(StateTheme.warmBackground.ignoresSafeArea())
        .task {
            await model.start()
            if model.session != nil {
                await notificationCoordinator.activate(model: model)
            }
        }
        .onChange(of: model.session?.actor.id) { _, actorID in
            guard actorID != nil else { return }
            Task { await notificationCoordinator.activate(model: model) }
        }
        .onChange(of: scenePhase) { _, phase in
            guard phase == .active, model.session != nil else { return }
            Task {
                await model.synchronize()
                await notificationCoordinator.refresh(model: model)
            }
        }
        .onReceive(NotificationCenter.default.publisher(for: .stateRemoteSync)) { _ in
            Task {
                await model.synchronize()
                await notificationCoordinator.refresh(model: model)
            }
        }
        .onReceive(NotificationCenter.default.publisher(for: .stateAPNSToken)) { notification in
            guard let token = notification.object as? Data else { return }
            Task { await pushRegistrationService.registerIfSupported(apnsToken: token, model: model) }
        }
        .onReceive(NotificationCenter.default.publisher(for: .stateNotificationAction)) { notification in
            guard let occurrenceID = notification.userInfo?["occurrence_id"] as? String, !occurrenceID.isEmpty else { return }
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
}
