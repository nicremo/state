import CryptoKit
import Foundation
import Security

/// Trust is scoped to one origin and the exact certificate scanned in person.
/// Other hosts, certificate changes and HTTP redirects fail closed.
public final class LocalServerTrust: NSObject, URLSessionDelegate, URLSessionTaskDelegate, Sendable {
    private let serverURL: URL
    private let fingerprint: String

    public init(serverURL: URL, fingerprint: String) throws {
        guard serverURL.scheme == "https", serverURL.host != nil,
              serverURL.user == nil, serverURL.password == nil,
              Self.isValidFingerprint(fingerprint) else {
            throw URLError(.badURL)
        }
        self.serverURL = serverURL
        self.fingerprint = fingerprint.lowercased()
    }

    public static func isValidFingerprint(_ value: String) -> Bool {
        value.count == 64 && value.utf8.allSatisfy {
            (48...57).contains($0) || (65...70).contains($0) || (97...102).contains($0)
        }
    }

    public static func session(serverURL: URL, fingerprint: String?) throws -> URLSession {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = 15
        configuration.timeoutIntervalForResource = 30
        guard let fingerprint else { return URLSession(configuration: configuration) }
        return URLSession(configuration: configuration, delegate: try LocalServerTrust(serverURL: serverURL, fingerprint: fingerprint), delegateQueue: nil)
    }

    public func urlSession(
        _ session: URLSession,
        didReceive challenge: URLAuthenticationChallenge,
        completionHandler: @escaping @Sendable (URLSession.AuthChallengeDisposition, URLCredential?) -> Void
    ) {
        let space = challenge.protectionSpace
        guard space.authenticationMethod == NSURLAuthenticationMethodServerTrust,
              space.host.lowercased() == serverURL.host?.lowercased(),
              space.port == (serverURL.port ?? 443),
              let trust = space.serverTrust,
              let chain = SecTrustCopyCertificateChain(trust) as? [SecCertificate],
              let certificate = chain.first else {
            completionHandler(.cancelAuthenticationChallenge, nil)
            return
        }
        let data = SecCertificateCopyData(certificate) as Data
        let actual = SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
        guard actual == fingerprint else {
            completionHandler(.cancelAuthenticationChallenge, nil)
            return
        }
        // The pin establishes identity. Basic X.509 validation still checks
        // validity and signature, without relying on a changeable Mac hostname.
        SecTrustSetPolicies(trust, SecPolicyCreateBasicX509())
        SecTrustSetAnchorCertificates(trust, [certificate] as CFArray)
        SecTrustSetAnchorCertificatesOnly(trust, true)
        guard SecTrustEvaluateWithError(trust, nil) else {
            completionHandler(.cancelAuthenticationChallenge, nil)
            return
        }
        completionHandler(.useCredential, URLCredential(trust: trust))
    }

    public func urlSession(
        _ session: URLSession,
        task: URLSessionTask,
        willPerformHTTPRedirection response: HTTPURLResponse,
        newRequest request: URLRequest,
        completionHandler: @escaping @Sendable (URLRequest?) -> Void
    ) {
        // The local API has no redirects. Reject them before credentials move.
        completionHandler(nil)
    }
}
