import XCTest
@testable import State

final class UUIDv7Tests: XCTestCase {
    func testGeneratedIdentifierHasVersionSevenAndExpectedTimestamp() throws {
        let date = Date(timeIntervalSince1970: 1_786_474_800.123)
        let identifier = UUIDv7.generate(now: date, randomBytes: Array(0..<10))

        XCTAssertEqual(identifier.uuidString.split(separator: "-")[2].first, "7")
        XCTAssertEqual(UUIDv7.timestamp(identifier), 1_786_474_800_123)
    }

    func testIdentifiersSortByCreationTime() throws {
        let earlier = UUIDv7.generate(now: Date(timeIntervalSince1970: 100), randomBytes: Array(repeating: 1, count: 10))
        let later = UUIDv7.generate(now: Date(timeIntervalSince1970: 101), randomBytes: Array(repeating: 0, count: 10))

        XCTAssertLessThan(earlier.uuidString, later.uuidString)
    }
}
