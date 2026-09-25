import CryptoKit
import Foundation

/// Where photos and recordings live on this device. A file waits in
/// Application Support until its upload is confirmed, then moves to the
/// cache under its hash, where downloads of other devices' media land too.
enum NoteMediaFiles {
    struct Stored: Sendable {
        let fileName: String
        let sha256: String
        let byteSize: Int64
    }

    static var pendingDirectory: URL {
        directory(.applicationSupportDirectory)
    }

    static var cacheDirectory: URL {
        directory(.cachesDirectory)
    }

    private static func directory(_ base: FileManager.SearchPathDirectory) -> URL {
        let root = FileManager.default.urls(for: base, in: .userDomainMask)[0]
            .appendingPathComponent("NoteMedia", isDirectory: true)
        try? FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        return root
    }

    /// Keeps new media for upload. Files are protected until first unlock,
    /// so a queued upload can still go out in the background.
    static func save(_ data: Data, fileExtension: String) throws -> Stored {
        let name = UUIDv7.generate().uuidString.lowercased() + "." + fileExtension
        let url = pendingDirectory.appendingPathComponent(name)
        #if os(iOS)
        try data.write(to: url, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
        #else
        try data.write(to: url, options: [.atomic])
        #endif
        return Stored(fileName: name, sha256: sha256(data), byteSize: Int64(data.count))
    }

    static func pendingData(_ fileName: String) -> Data? {
        guard !fileName.contains("/") else { return nil }
        return try? Data(contentsOf: pendingDirectory.appendingPathComponent(fileName))
    }

    /// The bytes of a queued upload: from the queue, or from the cache when
    /// an earlier run moved the file there before it could mark the upload.
    static func uploadData(_ upload: NoteUpload) -> Data? {
        pendingData(upload.fileName) ?? cachedData(sha256: upload.sha256)
    }

    static func removePending(_ fileNames: [String]) {
        for name in fileNames where !name.contains("/") {
            try? FileManager.default.removeItem(at: pendingDirectory.appendingPathComponent(name))
        }
    }

    /// After a confirmed upload the file moves to the cache under its hash.
    static func keepUploaded(_ upload: NoteUpload) {
        let source = pendingDirectory.appendingPathComponent(upload.fileName)
        let target = cachedURL(sha256: upload.sha256)
        if FileManager.default.fileExists(atPath: target.path) {
            try? FileManager.default.removeItem(at: source)
        } else {
            try? FileManager.default.moveItem(at: source, to: target)
        }
    }

    static func cachedURL(sha256: String) -> URL {
        cacheDirectory.appendingPathComponent(sha256)
    }

    static func cachedData(sha256: String) -> Data? {
        guard sha256.count == 64, sha256.allSatisfy(\.isHexDigit) else { return nil }
        return try? Data(contentsOf: cachedURL(sha256: sha256))
    }

    /// Stores downloaded media, but only when it is what the note says.
    static func cache(_ data: Data, sha256 expected: String) -> Bool {
        guard sha256(data) == expected else { return false }
        try? data.write(to: cachedURL(sha256: expected), options: [.atomic])
        return true
    }

    static func sha256(_ data: Data) -> String {
        SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }
}
