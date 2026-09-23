import Foundation
import Testing
@testable import StateServerCore

struct LogFileTests {
    private func temporaryDirectory() throws -> URL {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
        return url
    }

    @Test func appendsAndRotates() throws {
        let directory = try temporaryDirectory()
        let log = LogFile(directory: directory, name: "server.log", maxBytes: 10, keep: 2)
        try log.append(Data("12345678\n".utf8))
        try log.append(Data("abcdefgh\n".utf8))
        try log.append(Data("ABCDEFGH\n".utf8))
        let current = try String(contentsOf: log.url, encoding: .utf8)
        let first = try String(contentsOf: directory.appendingPathComponent("server.log.1"), encoding: .utf8)
        let second = try String(contentsOf: directory.appendingPathComponent("server.log.2"), encoding: .utf8)
        #expect(current == "ABCDEFGH\n")
        #expect(first == "abcdefgh\n")
        #expect(second == "12345678\n")
    }

    @Test func createsTheFileWithOwnerOnlyPermissions() throws {
        let directory = try temporaryDirectory()
        let log = LogFile(directory: directory)
        try log.append(Data("x\n".utf8))
        let attributes = try FileManager.default.attributesOfItem(atPath: log.url.path)
        #expect((attributes[.posixPermissions] as? NSNumber)?.intValue == 0o600)
    }
}
