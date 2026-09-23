import CryptoKit
import DeviceCheck
import Foundation
import Network

/// Observable result of the last push registration attempt. The settings
/// screen shows why notifications do or do not travel through a relay.
enum PushRelayStatus: Equatable, Sendable {
    case unknown
    case registered
    case noRelay
    case unavailable
    case failed(String)
}

enum PushRegistrationError: Error, LocalizedError {
    case unavailable
    case invalidRelayURL
    case invalidResponse
    case rejected(Int)

    var errorDescription: String? {
        switch self {
        case .unavailable: "App Attest is unavailable on this device."
        case .invalidRelayURL: "The push relay URL is invalid."
        case .invalidResponse: "The push relay returned an invalid response."
        case let .rejected(status): "The push relay rejected registration with status \(status)."
        }
    }
}

private struct RelayChallenge: Codable {
    let challenge: String
    let expiresAt: Date
}

private struct RelayRegistrationResponse: Codable {
    let routeID: String
    let authorization: String
}

private struct RelayAttestation: Codable {
    let keyID: String
    let object: String
    let challenge: String
    let assertion: String
}

private struct RelayRegistrationRequest: Codable {
    let apnsToken: String
    let environment: String
    let attestation: RelayAttestation
}

private struct RegistrationClientData: Codable {
    let apnsTokenHash: String
    let challenge: String
    let environment: String
}

private actor RelayClient {
    private let baseURL: URL
    private let session: URLSession

    init(baseURL: URL, session: URLSession = .shared) throws {
        guard baseURL.scheme?.lowercased() == "https", baseURL.host != nil else {
            throw PushRegistrationError.invalidRelayURL
        }
        self.baseURL = baseURL
        self.session = session
    }

    func challenge() async throws -> RelayChallenge {
        let data = try await request(path: "/v1/attest/challenges", method: "POST")
        return try StateJSON.decoder.decode(RelayChallenge.self, from: data)
    }

    func register(_ input: RelayRegistrationRequest) async throws -> RelayRegistrationResponse {
        let body = try StateJSON.encoder.encode(input)
        let data = try await request(path: "/v1/routes", method: "POST", body: body)
        return try StateJSON.decoder.decode(RelayRegistrationResponse.self, from: data)
    }

    func update(routeID: String, capability: String, apnsToken: String) async throws {
        let body = try JSONSerialization.data(withJSONObject: ["apns_token": apnsToken], options: [.sortedKeys])
        _ = try await request(
            path: "/v1/routes/\(routeID)",
            method: "PATCH",
            body: body,
            authorization: capability
        )
    }

    private func request(
        path: String,
        method: String,
        body: Data? = nil,
        authorization: String? = nil
    ) async throws -> Data {
        guard let url = URL(string: path, relativeTo: baseURL)?.absoluteURL else {
            throw PushRegistrationError.invalidRelayURL
        }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.httpBody = body
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if body != nil {
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        if let authorization {
            request.setValue("Bearer \(authorization)", forHTTPHeaderField: "Authorization")
        }
        let (data, response) = try await session.data(for: request)
        guard let response = response as? HTTPURLResponse else {
            throw PushRegistrationError.invalidResponse
        }
        guard (200..<300).contains(response.statusCode) else {
            throw PushRegistrationError.rejected(response.statusCode)
        }
        return data
    }
}

@MainActor
final class PushRegistrationService {
    private let defaults = UserDefaults(suiteName: "group.com.fabincrm.state") ?? .standard
    private let routeCapabilityAccount = "relay-route-capability"
    private let routeIDKey = "state.relay-route-id"
    /// Builds before this change cached a derived relay globally. The relay now
    /// belongs to the server session, so the old value is deleted on sight.
    nonisolated static let legacyRelayURLKey = "state.relay-url"

    func registerIfSupported(apnsToken: Data, model: AppModel) async {
        guard DCAppAttestService.shared.isSupported else {
            model.pushStatus = .unavailable
            return
        }
        guard let session = model.session else { return }
        Self.removeLegacyCachedRelay(in: defaults)
        guard let relayURL = Self.relayURL(for: session.serverURL, configured: session.relayURL) else {
            model.pushStatus = .noRelay
            return
        }
        do {
            let client = try RelayClient(baseURL: relayURL)
            let token = apnsToken.map { String(format: "%02x", $0) }.joined()
            let privateKey = try pushPrivateKey()

            if
                let routeID = defaults.string(forKey: routeIDKey),
                let capabilityData = try SharedKeychain.get(account: routeCapabilityAccount),
                let capability = String(data: capabilityData, encoding: .utf8)
            {
                do {
                    try await client.update(routeID: routeID, capability: capability, apnsToken: token)
                    try await model.registerPushRoute(
                        relayURL: relayURL,
                        routeID: routeID,
                        authorization: capability,
                        publicKey: privateKey.publicKey.rawRepresentation
                    )
                    model.pushStatus = .registered
                    return
                } catch PushRegistrationError.rejected(401), PushRegistrationError.rejected(404) {
                    defaults.removeObject(forKey: routeIDKey)
                    try SharedKeychain.delete(account: routeCapabilityAccount)
                }
            }

            let challenge = try await client.challenge()
            let attestKeyID = try await DCAppAttestService.shared.generateKey()
            let attestationHash = Data(SHA256.hash(data: Data(challenge.challenge.utf8)))
            let attestationObject = try await DCAppAttestService.shared.attestKey(
                attestKeyID,
                clientDataHash: attestationHash
            )
            let environment = Self.environment
            let tokenHash = SHA256.hash(data: Data(token.utf8)).map { String(format: "%02x", $0) }.joined()
            let clientData = RegistrationClientData(
                apnsTokenHash: tokenHash,
                challenge: challenge.challenge,
                environment: environment
            )
            let clientDataHash = Data(SHA256.hash(data: try StateJSON.encoder.encode(clientData)))
            let assertion = try await DCAppAttestService.shared.generateAssertion(
                attestKeyID,
                clientDataHash: clientDataHash
            )
            let response = try await client.register(
                RelayRegistrationRequest(
                    apnsToken: token,
                    environment: environment,
                    attestation: RelayAttestation(
                        keyID: attestKeyID,
                        object: attestationObject.base64EncodedString(),
                        challenge: challenge.challenge,
                        assertion: assertion.base64EncodedString()
                    )
                )
            )
            try SharedKeychain.set(Data(response.authorization.utf8), account: routeCapabilityAccount)
            defaults.set(response.routeID, forKey: routeIDKey)
            try await model.registerPushRoute(
                relayURL: relayURL,
                routeID: response.routeID,
                authorization: response.authorization,
                publicKey: privateKey.publicKey.rawRepresentation
            )
            model.pushStatus = .registered
        } catch {
            model.pushStatus = .failed(error.localizedDescription)
            model.presentedError = error.localizedDescription
        }
    }

    private func pushPrivateKey() throws -> Curve25519.KeyAgreement.PrivateKey {
        if let stored = try SharedKeychain.get(account: SharedKeychain.pushPrivateKeyAccount) {
            return try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: stored)
        }
        let key = Curve25519.KeyAgreement.PrivateKey()
        try SharedKeychain.set(key.rawRepresentation, account: SharedKeychain.pushPrivateKeyAccount)
        return key
    }

    /// Deletes the relay address that older builds cached globally.
    nonisolated static func removeLegacyCachedRelay(in defaults: UserDefaults) {
        defaults.removeObject(forKey: legacyRelayURLKey)
    }

    /// A relay has to be an absolute HTTPS address without credentials.
    nonisolated static func isUsableRelay(_ url: URL) -> Bool {
        url.scheme?.lowercased() == "https" && url.host?.isEmpty == false && url.user == nil && url.password == nil
    }

    /// Decides where pushes go. The relay of the session wins; otherwise the
    /// address is derived from a public server host. Local and private hosts
    /// get no relay, because `relay.<name>.local` and `relay.192.168.1.20` do
    /// not exist and a failed registration would look like a broken relay.
    nonisolated static func relayURL(for serverURL: URL, configured: URL?) -> URL? {
        if let configured {
            return isUsableRelay(configured) ? configured : nil
        }
        guard
            let components = URLComponents(url: serverURL, resolvingAgainstBaseURL: false),
            let host = components.host,
            isPublicHost(host)
        else {
            return nil
        }
        var relay = components
        relay.user = nil
        relay.password = nil
        relay.query = nil
        relay.fragment = nil
        relay.host = host.hasPrefix("state.") ? "relay." + host.dropFirst("state.".count) : "relay." + host
        if relay.path == "/" { relay.path = "" }
        guard let url = relay.url, isUsableRelay(url) else { return nil }
        return url
    }

    nonisolated private static func isPublicHost(_ host: String) -> Bool {
        let lowercased = host.lowercased()
        if lowercased.hasSuffix(".local") || lowercased == "local" { return false }
        if IPv4Address(host) != nil || IPv6Address(host) != nil { return false }
        // A single label such as "localhost" or a Bonjour name never resolves
        // outside the own network.
        return lowercased.contains(".")
    }

    private static var environment: String {
        #if DEBUG
        "sandbox"
        #else
        "production"
        #endif
    }
}
