import SwiftUI

/// The first thing State shows. The launch screen and this view stand on the
/// icon's own ground, so the icon the owner tapped seems to open rather than
/// hand over to a second drawing. Nothing but the mark: it has one job.
struct SplashView: View {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var arrived = false

    var body: some View {
        ZStack {
            SplashGround()
                .ignoresSafeArea()

            StateMark(size: 116)
                .scaleEffect(arrived || reduceMotion ? 1 : 0.94)
                .opacity(arrived || reduceMotion ? 1 : 0)
        }
        .task {
            guard !reduceMotion else { return }
            withAnimation(.smooth(duration: 0.5)) { arrived = true }
        }
    }
}

/// Exactly the launch screen color, so the hand over from the system launch
/// image to this view is invisible.
struct SplashGround: View {
    var body: some View {
        StateTheme.mist
    }
}
