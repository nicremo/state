import SwiftUI

/// Picks the layout by available width: tabs on a compact iPhone, the three
/// column split on a regular iPad and on every Mac.
struct AdaptiveRootView: View {
    @Bindable var model: AppModel

    #if os(iOS)
    @Environment(\.horizontalSizeClass) private var horizontalSizeClass
    #endif

    var body: some View {
        #if os(macOS)
        SplitRootView(model: model)
        #else
        if horizontalSizeClass == .regular {
            SplitRootView(model: model)
        } else {
            MainTabView(model: model)
        }
        #endif
    }
}
