import CryptoKit
import XCTest
@testable import State

final class PushEnvelopeTests: XCTestCase {
    func testEnvelopeRoundTripUsesRouteAsAuthenticatedData() throws {
        let recipient = Curve25519.KeyAgreement.PrivateKey()
        let plaintext = Data("secret reminder".utf8)
        let envelope = try PushEnvelope.seal(
            plaintext,
            recipientPublicKey: recipient.publicKey.rawRepresentation,
            routeID: "route-a"
        )

        XCTAssertEqual(
            try envelope.open(recipientPrivateKey: recipient.rawRepresentation, routeID: "route-a"),
            plaintext
        )
        XCTAssertThrowsError(
            try envelope.open(recipientPrivateKey: recipient.rawRepresentation, routeID: "route-b")
        )
    }
}
