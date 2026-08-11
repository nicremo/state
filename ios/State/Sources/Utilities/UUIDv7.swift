import Foundation

enum UUIDv7 {
    static func generate(
        now: Date = Date(),
        randomBytes suppliedRandomBytes: [UInt8]? = nil
    ) -> UUID {
        let milliseconds = UInt64(max(0, now.timeIntervalSince1970 * 1_000))
        var randomBytes = suppliedRandomBytes ?? secureRandomBytes(count: 10)
        if randomBytes.count < 10 {
            randomBytes.append(contentsOf: secureRandomBytes(count: 10 - randomBytes.count))
        }
        var bytes = [UInt8](repeating: 0, count: 16)
        bytes[0] = UInt8((milliseconds >> 40) & 0xff)
        bytes[1] = UInt8((milliseconds >> 32) & 0xff)
        bytes[2] = UInt8((milliseconds >> 24) & 0xff)
        bytes[3] = UInt8((milliseconds >> 16) & 0xff)
        bytes[4] = UInt8((milliseconds >> 8) & 0xff)
        bytes[5] = UInt8(milliseconds & 0xff)
        bytes[6] = 0x70 | (randomBytes[0] & 0x0f)
        bytes[7] = randomBytes[1]
        bytes[8] = 0x80 | (randomBytes[2] & 0x3f)
        for index in 9..<16 {
            bytes[index] = randomBytes[index - 6]
        }
        let value = bytes.map { String(format: "%02x", $0) }.joined()
        let formatted = "\(value.prefix(8))-\(value.dropFirst(8).prefix(4))-\(value.dropFirst(12).prefix(4))-\(value.dropFirst(16).prefix(4))-\(value.dropFirst(20))"
        return UUID(uuidString: formatted) ?? UUID()
    }

    static func timestamp(_ identifier: UUID) -> UInt64? {
        let compact = identifier.uuidString.replacingOccurrences(of: "-", with: "")
        guard compact.count == 32 else { return nil }
        return UInt64(compact.prefix(12), radix: 16)
    }

    private static func secureRandomBytes(count: Int) -> [UInt8] {
        var bytes = [UInt8](repeating: 0, count: count)
        let status = SecRandomCopyBytes(kSecRandomDefault, count, &bytes)
        precondition(status == errSecSuccess, "Secure random generation failed")
        return bytes
    }
}
