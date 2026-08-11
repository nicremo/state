import CryptoKit
import Foundation

struct PushEnvelope: Codable, Sendable {
    let version: Int
    let ephemeralPublicKey: Data
    let nonce: Data
    let ciphertext: Data

    static func seal(
        _ plaintext: Data,
        recipientPublicKey: Data,
        routeID: String
    ) throws -> PushEnvelope {
        let recipient = try Curve25519.KeyAgreement.PublicKey(rawRepresentation: recipientPublicKey)
        let ephemeral = Curve25519.KeyAgreement.PrivateKey()
        let sharedSecret = try ephemeral.sharedSecretFromKeyAgreement(with: recipient)
        let key = deriveKey(
            sharedSecret: sharedSecret,
            ephemeralPublicKey: ephemeral.publicKey.rawRepresentation,
            recipientPublicKey: recipientPublicKey
        )
        let sealed = try AES.GCM.seal(plaintext, using: key, authenticating: authenticatedData(routeID: routeID))
        guard let nonceData = sealed.nonce.withUnsafeBytes({ Data($0) }) as Data? else {
            throw PushEnvelopeError.invalidEnvelope
        }
        return PushEnvelope(
            version: 1,
            ephemeralPublicKey: ephemeral.publicKey.rawRepresentation,
            nonce: nonceData,
            ciphertext: sealed.ciphertext + sealed.tag
        )
    }

    func open(recipientPrivateKey: Data, routeID: String) throws -> Data {
        guard version == 1, ephemeralPublicKey.count == 32, nonce.count == 12, ciphertext.count > 16 else {
            throw PushEnvelopeError.invalidEnvelope
        }
        let privateKey = try Curve25519.KeyAgreement.PrivateKey(rawRepresentation: recipientPrivateKey)
        let ephemeral = try Curve25519.KeyAgreement.PublicKey(rawRepresentation: ephemeralPublicKey)
        let sharedSecret = try privateKey.sharedSecretFromKeyAgreement(with: ephemeral)
        let key = Self.deriveKey(
            sharedSecret: sharedSecret,
            ephemeralPublicKey: ephemeralPublicKey,
            recipientPublicKey: privateKey.publicKey.rawRepresentation
        )
        let encrypted = ciphertext.dropLast(16)
        let tag = ciphertext.suffix(16)
        let sealedBox = try AES.GCM.SealedBox(
            nonce: AES.GCM.Nonce(data: nonce),
            ciphertext: encrypted,
            tag: tag
        )
        return try AES.GCM.open(sealedBox, using: key, authenticating: Self.authenticatedData(routeID: routeID))
    }

    private static func deriveKey(
        sharedSecret: SharedSecret,
        ephemeralPublicKey: Data,
        recipientPublicKey: Data
    ) -> SymmetricKey {
        let salt = Data(SHA256.hash(data: ephemeralPublicKey + recipientPublicKey))
        return sharedSecret.hkdfDerivedSymmetricKey(
            using: SHA256.self,
            salt: salt,
            sharedInfo: Data("com.fabincrm.state.push.v1".utf8),
            outputByteCount: 32
        )
    }

    private static func authenticatedData(routeID: String) -> Data {
        Data("state-push-v1\0".utf8) + Data(routeID.utf8)
    }
}

enum PushEnvelopeError: Error {
    case invalidEnvelope
}
