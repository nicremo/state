import Foundation
import Security

enum SharedKeychain {
    static let accessGroup = "$(AppIdentifierPrefix)com.fabincrm.state.shared"
    static let pushPrivateKeyAccount = "push-x25519-private-key"

    static func set(_ value: Data, account: String, accessGroup: String? = nil) throws {
        let group = accessGroup ?? resolvedAccessGroup
        var query = baseQuery(account: account, accessGroup: group)
        SecItemDelete(query as CFDictionary)
        query[kSecValueData as String] = value
        query[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let status = SecItemAdd(query as CFDictionary, nil)
        guard status == errSecSuccess else {
            throw KeychainError.status(status)
        }
    }

    static func get(account: String, accessGroup: String? = nil) throws -> Data? {
        var query = baseQuery(account: account, accessGroup: accessGroup ?? resolvedAccessGroup)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound {
            return nil
        }
        guard status == errSecSuccess, let data = result as? Data else {
            throw KeychainError.status(status)
        }
        return data
    }

    static func delete(account: String, accessGroup: String? = nil) throws {
        let status = SecItemDelete(baseQuery(account: account, accessGroup: accessGroup ?? resolvedAccessGroup) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainError.status(status)
        }
    }

    private static var resolvedAccessGroup: String? {
        guard let prefix = Bundle.main.object(forInfoDictionaryKey: "AppIdentifierPrefix") as? String else {
            return nil
        }
        return prefix + "com.fabincrm.state.shared"
    }

    private static func baseQuery(account: String, accessGroup: String?) -> [String: Any] {
        var query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: "com.fabincrm.state",
            kSecAttrAccount as String: account,
        ]
        if let accessGroup {
            query[kSecAttrAccessGroup as String] = accessGroup
        }
        return query
    }
}

enum KeychainError: Error {
    case status(OSStatus)
}
