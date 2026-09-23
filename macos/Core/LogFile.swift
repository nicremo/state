import Foundation

/// A small size-rotated log file for the server's structured stderr. The
/// server never logs request bodies, credentials or pairing codes, so its
/// stderr is safe to keep; the file is still readable by the owner only.
public struct LogFile: Sendable {
    public let url: URL
    private let directory: URL
    private let name: String
    private let maxBytes: Int
    private let keep: Int

    public init(directory: URL, name: String = "server.log", maxBytes: Int = 5 * 1_048_576, keep: Int = 3) {
        self.directory = directory
        self.name = name
        self.maxBytes = maxBytes
        self.keep = keep
        self.url = directory.appendingPathComponent(name)
    }

    public func append(_ data: Data) throws {
        let manager = FileManager.default
        try manager.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let currentSize = (try? manager.attributesOfItem(atPath: url.path)[.size] as? NSNumber)?.intValue ?? 0
        if currentSize > 0, currentSize + data.count > maxBytes {
            try rotate()
        }
        if !manager.fileExists(atPath: url.path) {
            manager.createFile(atPath: url.path, contents: nil, attributes: [.posixPermissions: 0o600])
        }
        let handle = try FileHandle(forWritingTo: url)
        defer { try? handle.close() }
        try handle.seekToEnd()
        try handle.write(contentsOf: data)
    }

    private func rotate() throws {
        let manager = FileManager.default
        let oldest = directory.appendingPathComponent("\(name).\(keep)")
        if manager.fileExists(atPath: oldest.path) { try manager.removeItem(at: oldest) }
        if keep > 1 {
            for index in stride(from: keep - 1, through: 1, by: -1) {
                let source = directory.appendingPathComponent("\(name).\(index)")
                let target = directory.appendingPathComponent("\(name).\(index + 1)")
                if manager.fileExists(atPath: source.path) { try manager.moveItem(at: source, to: target) }
            }
        }
        try manager.moveItem(at: url, to: directory.appendingPathComponent("\(name).1"))
    }
}
