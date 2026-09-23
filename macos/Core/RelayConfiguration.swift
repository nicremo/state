import Foundation

/// The optional public push relay of the mac server. Empty means local-only
/// delivery: the iPhone then receives notifications only in the home network,
/// so the relay stays optional for everyone who does not run a VPS.
public enum DesktopRelay {
    public static let defaultsKey = "state.desktop.relay-url"

    /// Trims the input and returns the address, or nil when the input is empty
    /// or is not an absolute HTTPS URL without user information, query or
    /// fragment.
    public static func normalized(_ value: String) -> String? {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, let components = URLComponents(string: trimmed) else { return nil }
        guard components.scheme?.lowercased() == "https",
              let host = components.host, !host.isEmpty,
              components.user == nil, components.password == nil,
              components.query == nil, components.fragment == nil,
              components.url != nil
        else { return nil }
        return trimmed
    }

    /// Empty input is valid and clears the relay. Any other input has to pass
    /// `normalized`.
    public static func isValid(_ value: String) -> Bool {
        value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || normalized(value) != nil
    }

    /// Reads the stored relay. An unusable stored value counts as absent, so a
    /// hand-edited preference cannot break the server start.
    public static func stored(in defaults: UserDefaults) -> String {
        guard let value = defaults.string(forKey: defaultsKey) else { return "" }
        return normalized(value) ?? ""
    }

    /// Writes the relay address, or removes the preference when the input is
    /// empty. Invalid input is rejected without touching the stored value.
    @discardableResult
    public static func store(_ value: String, in defaults: UserDefaults) -> Bool {
        guard isValid(value) else { return false }
        if let address = normalized(value) {
            defaults.set(address, forKey: defaultsKey)
        } else {
            defaults.removeObject(forKey: defaultsKey)
        }
        return true
    }

    /// Arguments for the desktop server. A missing or unusable relay is left
    /// out, which keeps the server in local-only mode.
    public static func launchArguments(dataDirectory: String, host: String, relayURL: String) -> [String] {
        var arguments = ["desktop", "--data", dataDirectory, "--host", host]
        if let address = normalized(relayURL) {
            arguments += ["--relay-url", address]
        }
        return arguments
    }
}
