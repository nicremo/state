import Foundation

/// Every address the app sends the owner to. Keeping them in one place means a
/// moved document is a single change rather than a hunt through views.
enum StateLinks {
    static let repository = URL(string: "https://github.com/Nicremo/state")!
    /// The full setup guide. Linked from the connection screen and from the top
    /// of Settings, because a self hosted tool is unusable without it.
    static let documentation = URL(string: "https://github.com/Nicremo/state/blob/main/DOCUMENTATION.md")!
    static let privacy = URL(string: "https://github.com/Nicremo/state/blob/main/PRIVACY.md")!
}
