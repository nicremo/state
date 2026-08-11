import Foundation

struct ServerSession: Codable, Sendable {
    let serverURL: URL
    let actor: Actor
}

@MainActor
final class SessionRepository {
    private let defaults: UserDefaults
    private let profileKey = "state.server-session"
    private let credentialAccount = "server-credential"

    init(defaults: UserDefaults = UserDefaults(suiteName: "group.com.fabincrm.state") ?? .standard) {
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

    func clear() throws {
        try SharedKeychain.delete(account: credentialAccount)
        defaults.removeObject(forKey: profileKey)
    }
}

struct PairingPayload: Sendable {
    let serverURL: URL
    let bootstrapToken: String?
    let pairingCode: String?

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
        self.serverURL = serverURL
        bootstrapToken = bootstrap
        pairingCode = code
    }
}
