import CryptoKit
import DeviceCheck
import Foundation

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
    private let relayURLKey = "state.relay-url"

    func registerIfSupported(apnsToken: Data, model: AppModel) async {
        guard DCAppAttestService.shared.isSupported, let session = model.session else { return }
        do {
            let relayURL = try resolvedRelayURL(serverURL: session.serverURL)
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
        } catch {
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

    private func resolvedRelayURL(serverURL: URL) throws -> URL {
        if let configured = defaults.string(forKey: relayURLKey), let url = URL(string: configured) {
            return url
        }
        guard var components = URLComponents(url: serverURL, resolvingAgainstBaseURL: false), let host = components.host else {
            throw PushRegistrationError.invalidRelayURL
        }
        if host.hasPrefix("state.") {
            components.host = "relay." + host.dropFirst("state.".count)
        } else {
            components.host = "relay." + host
        }
        guard let url = components.url else { throw PushRegistrationError.invalidRelayURL }
        defaults.set(url.absoluteString, forKey: relayURLKey)
        return url
    }

    private static var environment: String {
        #if DEBUG
        "sandbox"
        #else
        "production"
        #endif
    }
}
