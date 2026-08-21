import Foundation
import XCTest
@testable import State

final class PolicyDraftValidationTests: XCTestCase {
    /// Mirrors the ValidProjectSlug pattern in internal/state/project.go.
    func testPolicyNameValidation() {
        XCTAssertTrue(PolicyDraft.isValidName("nightly-maintenance"))
        XCTAssertTrue(PolicyDraft.isValidName("ab"))
        XCTAssertTrue(PolicyDraft.isValidName("a1"))
        XCTAssertTrue(PolicyDraft.isValidName(String(repeating: "a", count: 64)))

        XCTAssertFalse(PolicyDraft.isValidName(""))
        XCTAssertFalse(PolicyDraft.isValidName("a"))
        XCTAssertFalse(PolicyDraft.isValidName(String(repeating: "a", count: 65)))
        XCTAssertFalse(PolicyDraft.isValidName("-leading"))
        XCTAssertFalse(PolicyDraft.isValidName("trailing-"))
        XCTAssertFalse(PolicyDraft.isValidName("Upper"))
        XCTAssertFalse(PolicyDraft.isValidName("with space"))
        XCTAssertFalse(PolicyDraft.isValidName("under_score"))
    }

    /// Mirrors the unattended allow-list in internal/state/execution_models.go.
    func testDisallowedCapabilitiesFollowMode() {
        var draft = PolicyDraft()
        draft.allowedCapabilities = ["read_repository", "deploy", "write_state"]

        draft.mode = .supervised
        XCTAssertEqual(draft.disallowedCapabilities, [])

        draft.mode = .unattendedLowRisk
        XCTAssertEqual(draft.disallowedCapabilities, ["deploy", "write_state"])

        draft.allowedCapabilities = ["read_repository", "run_tests", "edit_repository", "read_state_context"]
        XCTAssertEqual(draft.disallowedCapabilities, [])
    }

    func testIsValidRequiresNameProjectAdapterAndCompatibleCapabilities() {
        var draft = PolicyDraft()
        draft.name = "nightly-maintenance"
        draft.projectID = "01989f00-0000-7000-8000-000000000050"
        draft.adapter = "claude-code"
        draft.allowedCapabilities = ["run_tests"]
        XCTAssertTrue(draft.isValid)

        var noName = draft
        noName.name = "Not a slug"
        XCTAssertFalse(noName.isValid)

        var noProject = draft
        noProject.projectID = ""
        XCTAssertFalse(noProject.isValid)

        var badAdapter = draft
        badAdapter.adapter = "NOPE!!"
        XCTAssertFalse(badAdapter.isValid)

        var unattendedRestricted = draft
        unattendedRestricted.mode = .unattendedLowRisk
        unattendedRestricted.allowedCapabilities = ["deploy"]
        XCTAssertFalse(unattendedRestricted.isValid)
    }
}
