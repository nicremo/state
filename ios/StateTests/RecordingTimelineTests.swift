import XCTest
@testable import State

/// A recording made of several parts plays as one timeline, and the
/// sentence being spoken is found from the time.
final class RecordingTimelineTests: XCTestCase {
    private func part(_ ordinal: Int, durationMs: Int64?, text: String?, segments: [TranscriptSegment]?) -> NoteAttachment {
        var attachment = NoteAttachment(
            id: "a\(ordinal)", noteID: "n", ordinal: ordinal, kind: "audio", mimeType: "audio/mp4",
            byteSize: 100, sha256: String(repeating: "\(ordinal)", count: 64)
        )
        attachment.durationMs = durationMs
        attachment.derivedText = text
        attachment.segments = segments
        return attachment
    }

    private var twoParts: [NoteAttachment] {
        [
            part(1, durationMs: 5000, text: "C.", segments: [TranscriptSegment(startMs: 1000, endMs: 3000, text: "C.")]),
            part(0, durationMs: 10000, text: "A. B.", segments: [
                TranscriptSegment(startMs: 0, endMs: 4000, text: "A."),
                TranscriptSegment(startMs: 4000, endMs: 10000, text: "B."),
            ]),
        ]
    }

    func testPartsFormOneTimelineInTheirOrder() {
        let timeline = RecordingTimeline(attachments: twoParts)
        XCTAssertEqual(timeline.duration, 15)
        XCTAssertTrue(timeline.hasTimestamps)
        XCTAssertEqual(timeline.segments.map(\.text), ["A.", "B.", "C."])
        XCTAssertEqual(timeline.segments.map(\.start), [0, 4, 11])
        XCTAssertEqual(timeline.segments.map(\.end), [4, 10, 13])
        XCTAssertEqual(timeline.transcript, "A. B.\n\nC.")
    }

    func testTheSpokenSegmentFollowsTheTime() {
        let timeline = RecordingTimeline(attachments: twoParts)
        XCTAssertNil(timeline.segmentIndex(at: -1))
        XCTAssertEqual(timeline.segmentIndex(at: 0), 0)
        XCTAssertEqual(timeline.segmentIndex(at: 3.99), 0)
        XCTAssertEqual(timeline.segmentIndex(at: 4), 1)
        XCTAssertEqual(timeline.segmentIndex(at: 10.5), 1, "between two passages the last one stays marked")
        XCTAssertEqual(timeline.segmentIndex(at: 12), 2)
        XCTAssertEqual(timeline.segmentIndex(at: 20), 2)
    }

    func testTimeMapsToPartAndOffset() {
        let timeline = RecordingTimeline(attachments: twoParts)
        XCTAssertEqual(timeline.location(of: 3).part, 0)
        XCTAssertEqual(timeline.location(of: 3).offset, 3)
        XCTAssertEqual(timeline.location(of: 12).part, 1)
        XCTAssertEqual(timeline.location(of: 12).offset, 2, accuracy: 0.0001)
        XCTAssertEqual(timeline.location(of: 99).part, 1)
        XCTAssertEqual(timeline.location(of: 99).offset, 5, accuracy: 0.0001)
        XCTAssertEqual(timeline.location(of: -5).offset, 0)
    }

    func testMeasuredDurationsReplaceTheStoredOnes() {
        let timeline = RecordingTimeline(attachments: twoParts, durations: [10.5, 5])
        XCTAssertEqual(timeline.duration, 15.5)
        XCTAssertEqual(timeline.segments.last?.start ?? 0, 11.5, accuracy: 0.0001)
    }

    func testAPartWithoutSegmentsIsOnePassage() {
        let timeline = RecordingTimeline(attachments: [
            part(0, durationMs: 10000, text: "A.", segments: [TranscriptSegment(startMs: 0, endMs: 2000, text: "A.")]),
            part(1, durationMs: 4000, text: "Ohne Zeiten.", segments: nil),
        ])
        XCTAssertEqual(timeline.segments.map(\.text), ["A.", "Ohne Zeiten."])
        XCTAssertEqual(timeline.segments.last?.start, 10)
        XCTAssertEqual(timeline.segments.last?.end, 14)
    }

    func testWithoutTimestampsTheTranscriptStaysPlain() {
        let timeline = RecordingTimeline(attachments: [part(0, durationMs: 9000, text: "Heute war nervig.", segments: nil)])
        XCTAssertFalse(timeline.hasTimestamps)
        XCTAssertTrue(timeline.segments.isEmpty)
        XCTAssertEqual(timeline.transcript, "Heute war nervig.")
        XCTAssertNil(timeline.segmentIndex(at: 3))
    }

    func testSegmentsDecodeFromTheServer() throws {
        let json = """
        {"id":"a","note_id":"n","ordinal":0,"kind":"audio","mime_type":"audio/mp4","byte_size":10,"sha256":"s",
         "duration_ms":9000,"derived_text":"CLAUDE.md angepasst","raw_text":"Cloud.md angepasst",
         "segments":[{"start_ms":0,"end_ms":2400,"text":"CLAUDE.md angepasst"}]}
        """
        let attachment = try StateJSON.decoder.decode(NoteAttachment.self, from: Data(json.utf8))
        XCTAssertEqual(attachment.rawText, "Cloud.md angepasst")
        XCTAssertEqual(attachment.segments, [TranscriptSegment(startMs: 0, endMs: 2400, text: "CLAUDE.md angepasst")])
    }
}
