import Foundation

struct ServerSession: Codable, Sendable {
    let serverURL: URL
    let actor: Actor
    var certificateFingerprint: String? = nil
    /// The public push relay of this server, if it has one. It belongs to the
    /// session so a server switch cannot inherit the address of an earlier
    /// connection.
    var relayURL: URL? = nil
}

@MainActor
final class SessionRepository {
    private let defaults: UserDefaults
    private let profileKey = "state.server-session"
    private let credentialAccount = "server-credential"

    init(defaults: UserDefaults = Platform.sharedDefaults) {
        self.defaults = defaults
    }

    func load() throws -> (ServerSession, String)? {
        guard
            let data = defaults.data(forKey: profileKey),
            let tokenData = try SharedKeychain.get(account: credentialAccount),
            let token = String(data: tokenData, encoding: .utf8)
        else {
            return nil
        }
        return (try StateJSON.decoder.decode(ServerSession.self, from: data), token)
    }

    func save(session: ServerSession, token: String) throws {
        try SharedKeychain.set(Data(token.utf8), account: credentialAccount)
        defaults.set(try StateJSON.encoder.encode(session), forKey: profileKey)
    }

    /// Stores a new relay address with the current session. The credential
    /// stays in Keychain, so only the profile is rewritten.
    @discardableResult
    func updateRelayURL(_ relayURL: URL?) throws -> ServerSession? {
        guard let (session, token) = try load() else { return nil }
        var updated = session
        updated.relayURL = relayURL
        try save(session: updated, token: token)
        return updated
    }

    func clear() throws {
        try SharedKeychain.delete(account: credentialAccount)
        defaults.removeObject(forKey: profileKey)
    }
}

struct PairingPayload: Sendable {
    let serverURL: URL
    let bootstrapToken: String?
    let pairingCode: String?
    let certificateFingerprint: String?
    /// Relay address the server advertises in its QR code. A QR code that
    /// carries an unusable relay is rejected as a whole, because pairing
    /// without push is better than registering at an address nobody chose.
    let relayURL: URL?

    init?(value: String) {
        guard
            let components = URLComponents(string: value),
            components.scheme == "state",
            components.host == "pair",
            let serverValue = components.queryItems?.first(where: { $0.name == "server" })?.value,
            let serverURL = URL(string: serverValue)
        else {
            return nil
        }
        let bootstrap = components.queryItems?.first(where: { $0.name == "bootstrap" })?.value
        let code = components.queryItems?.first(where: { $0.name == "code" })?.value
        guard bootstrap != nil || code != nil else { return nil }
        let fingerprint = components.queryItems?.first(where: { $0.name == "fingerprint" })?.value
        if let fingerprint {
            guard serverURL.scheme == "https", LocalServerTrust.isValidFingerprint(fingerprint) else { return nil }
        }
        guard serverURL.user == nil, serverURL.password == nil else { return nil }
        if let relayValue = components.queryItems?.first(where: { $0.name == "relay" })?.value {
            guard let relay = URL(string: relayValue), PushRegistrationService.isUsableRelay(relay) else { return nil }
            relayURL = relay
        } else {
            relayURL = nil
        }
        certificateFingerprint = fingerprint
        self.serverURL = serverURL
        bootstrapToken = bootstrap
        pairingCode = code
    }
}
