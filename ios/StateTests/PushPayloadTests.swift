import Foundation
import XCTest
@testable import State

final class PushPayloadTests: XCTestCase {
    /// Decodes with the extension's plain snake-case decoder, which has no
    /// ID/URL acronym fixup. The fixture mirrors NotifyRunFinished in
    /// internal/push/service.go.
    func testDecodesRunFinishedPayload() throws {
        let json = Data(
            """
            {
              "kind": "run_finished",
              "run_id": "01989f00-0000-7000-8000-000000000060",
              "reminder_id": "01989f00-0000-7000-8000-000000000010",
              "occurrence_id": "01989f00-0000-7000-8000-000000000020",
              "status": "needs_approval",
              "title": "Fix the failing tests",
              "finished_at": "2026-08-12T09:02:00Z"
            }
            """.utf8
        )
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase

        let probe = try decoder.decode(PushKindProbe.self, from: json)
        let payload = try decoder.decode(RunPushPayload.self, from: json)

        XCTAssertEqual(probe.kind, "run_finished")
        XCTAssertEqual(payload.kind, "run_finished")
        XCTAssertEqual(payload.runId, "01989f00-0000-7000-8000-000000000060")
        XCTAssertEqual(payload.reminderId, "01989f00-0000-7000-8000-000000000010")
        XCTAssertEqual(payload.occurrenceId, "01989f00-0000-7000-8000-000000000020")
        XCTAssertEqual(payload.status, "needs_approval")
        XCTAssertEqual(payload.title, "Fix the failing tests")
        XCTAssertEqual(payload.finishedAt, "2026-08-12T09:02:00Z")
    }

    /// Manual runs carry no occurrence, so the key is absent on the wire.
    func testDecodesRunFinishedPayloadWithoutOccurrence() throws {
        let json = Data(
            """
            {
              "kind": "run_finished",
              "run_id": "run-1",
              "reminder_id": "reminder-1",
              "status": "succeeded",
              "title": "Nightly maintenance",
              "finished_at": "2026-08-12T09:02:00Z"
            }
            """.utf8
        )
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase

        let payload = try decoder.decode(RunPushPayload.self, from: json)

        XCTAssertNil(payload.occurrenceId)
        XCTAssertEqual(payload.status, "succeeded")
    }

    /// The reminder payload predates the kind dispatch and keeps its shape.
    func testDecodesReminderPayload() throws {
        let json = Data(
            """
            {
              "kind": "reminder",
              "reminder_id": "reminder-1",
              "occurrence_id": "occurrence-1",
              "title": "Water the plants",
              "description": "Also check the cuttings",
              "notify_at": "2026-08-12T09:00:00Z",
              "revision": 3
            }
            """.utf8
        )
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase

        let probe = try decoder.decode(PushKindProbe.self, from: json)
        let payload = try decoder.decode(ReminderPushPayload.self, from: json)

        XCTAssertEqual(probe.kind, "reminder")
        XCTAssertEqual(payload.reminderId, "reminder-1")
        XCTAssertEqual(payload.occurrenceId, "occurrence-1")
        XCTAssertEqual(payload.title, "Water the plants")
        XCTAssertEqual(payload.description, "Also check the cuttings")
        XCTAssertEqual(payload.revision, 3)
    }

    func testRunStatusTextIsLocalized() {
        XCTAssertEqual(RunPushStatusText.localized("succeeded", language: "en"), "succeeded")
        XCTAssertEqual(RunPushStatusText.localized("failed", language: "en"), "failed")
        XCTAssertEqual(RunPushStatusText.localized("needs_approval", language: "en"), "needs approval")
        XCTAssertEqual(RunPushStatusText.localized("succeeded", language: "de"), "erfolgreich")
        XCTAssertEqual(RunPushStatusText.localized("failed", language: "de"), "fehlgeschlagen")
        XCTAssertEqual(RunPushStatusText.localized("needs_approval", language: "de"), "wartet auf Freigabe")
        // Other languages fall back to English, unknown status passes through.
        XCTAssertEqual(RunPushStatusText.localized("succeeded", language: "fr"), "succeeded")
        XCTAssertEqual(RunPushStatusText.localized("bogus", language: "de"), "bogus")
    }
}
