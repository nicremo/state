import SwiftUI

/// The first thing State shows. It redraws the app icon as a vector so the
/// launch image, this screen and the home screen icon read as one object, and
/// it is the only dark moment in the app: a lit mark on its own ground, the
/// same way the icon is lit.
struct SplashView: View {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var mark: Double = 0
    @State private var wordmark: Double = 0

    var body: some View {
        ZStack {
            StateTheme.markBackdrop
                .ignoresSafeArea()

            RadialGradient(
                colors: [
                    Color(red: 0.99, green: 0.97, blue: 0.90).opacity(0.15),
                    Color.clear,
                ],
                center: .center,
                startRadius: 0,
                endRadius: 280
            )
            .ignoresSafeArea()

            VStack(spacing: StateTheme.Space.section) {
                StateMark(size: 112, progress: mark)

                VStack(spacing: StateTheme.Space.snug) {
                    Text(verbatim: "State")
                        .font(.largeTitle.weight(.semibold))
                        .tracking(1.5)
                        .foregroundStyle(.white)
                    Text("Memory for your coding agents")
                        .font(.footnote)
                        .foregroundStyle(.white.opacity(0.55))
                }
                .opacity(wordmark)
            }
        }
        .task {
            guard !reduceMotion else {
                mark = 1
                wordmark = 1
                return
            }
            withAnimation(.smooth(duration: 0.55)) { mark = 1 }
            try? await Task.sleep(for: .milliseconds(240))
            withAnimation(.smooth(duration: 0.4)) { wordmark = 1 }
        }
    }
}
