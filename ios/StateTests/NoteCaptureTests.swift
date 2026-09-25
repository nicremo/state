import CoreGraphics
import ImageIO
import UniformTypeIdentifiers
import XCTest
@testable import State

/// Photos are shrunk before upload, recordings roll over into segments, and
/// the photo limit always comes from the server.
final class NoteCaptureTests: XCTestCase {
    func testPhotosAreShrunkToTheUploadSize() throws {
        let large = try pngData(width: 4000, height: 3000)
        let prepared = try XCTUnwrap(NoteImagePreparation.jpeg(from: large))
        let source = try XCTUnwrap(CGImageSourceCreateWithData(prepared as CFData, nil))
        let properties = try XCTUnwrap(CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any])
        XCTAssertEqual(properties[kCGImagePropertyPixelWidth] as? Int, 2560)
        XCTAssertEqual(properties[kCGImagePropertyPixelHeight] as? Int, 1920)
        XCTAssertEqual(Array(prepared.prefix(3)), [0xFF, 0xD8, 0xFF], "the server checks the JPEG signature")
        XCTAssertNil(NoteImagePreparation.jpeg(from: Data("not an image".utf8)))
    }

    func testRecordingsRollOverAtTheServerSegmentLength() {
        XCTAssertFalse(VoiceRecorder.shouldRollOver(segmentElapsed: 299.9, segmentSeconds: 300))
        XCTAssertTrue(VoiceRecorder.shouldRollOver(segmentElapsed: 300, segmentSeconds: 300))
        XCTAssertFalse(VoiceRecorder.shouldRollOver(segmentElapsed: 1000, segmentSeconds: 0))
    }

    func testThePhotoLimitComesFromTheServer() throws {
        let json = """
        {"ai_available":true,"consent":true,"agent":{"model":"deepseek/deepseek-v4.1-flash","available":true,"tool_calls":true},
         "vision":{"available":true,"max_images":7,"max_image_bytes":8388608,"max_total_bytes":41943040,"model":"deepseek/deepseek-v4.1-flash","limit_source":"server_policy"},
         "audio":{"available":true,"max_segment_bytes":20971520,"max_segments":12,"segment_seconds":300,"model":"openai/whisper-large-v3-turbo"}}
        """
        let capabilities = try StateJSON.decoder.decode(NoteCapabilities.self, from: Data(json.utf8))
        XCTAssertEqual(capabilities.photoLimit, 7)
        XCTAssertEqual(capabilities.audio.segmentSeconds, 300)
        XCTAssertEqual(NoteCapabilities.offline.photoLimit, 1, "without the server one photo can still be kept")
    }

    func testCaptureNotesHaveTheirOwnPlaceholderTitle() {
        var note = Note.local(id: "n", title: "", document: "", at: Date())
        let app = Bundle(for: AppModel.self)
        XCTAssertEqual(note.placeholderTitle, String(localized: "New note", bundle: app))
        note.capture = "image"
        XCTAssertEqual(note.placeholderTitle, String(localized: "Photo note", bundle: app))
        note.capture = "audio"
        XCTAssertEqual(note.placeholderTitle, String(localized: "Voice note", bundle: app))
    }

    private func pngData(width: Int, height: Int) throws -> Data {
        let context = try XCTUnwrap(CGContext(data: nil, width: width, height: height, bitsPerComponent: 8, bytesPerRow: 0,
                                              space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue))
        context.setFillColor(CGColor(red: 0.2, green: 0.4, blue: 0.6, alpha: 1))
        context.fill(CGRect(x: 0, y: 0, width: width, height: height))
        let image = try XCTUnwrap(context.makeImage())
        let data = NSMutableData()
        let destination = try XCTUnwrap(CGImageDestinationCreateWithData(data, UTType.png.identifier as CFString, 1, nil))
        CGImageDestinationAddImage(destination, image, nil)
        XCTAssertTrue(CGImageDestinationFinalize(destination))
        return data as Data
    }
}

/// A pull to refresh that SwiftUI cancels is not an error for the owner.
final class CancellationPresentationTests: XCTestCase {
    func testCancellationsAreNotShownAsErrors() {
        XCTAssertTrue(AppModel.isCancellation(CancellationError()))
        XCTAssertTrue(AppModel.isCancellation(URLError(.cancelled)))
        XCTAssertFalse(AppModel.isCancellation(URLError(.notConnectedToInternet)))
        XCTAssertFalse(AppModel.isCancellation(StateAPIError.unauthorized))
    }
}

/// The note says exactly which part the AI wrote.
final class NoteProvenanceTests: XCTestCase {
    @MainActor
    func testProvenanceNamesOnlyWhatTheAIWrote() {
        var note = Note.local(id: "n", title: "Mein Titel", document: "Text", at: Date())
        XCTAssertNil(NoteAISuggestionsView.provenance(note, model: "m"))
        note.summarySource = Note.aiSource
        XCTAssertEqual(NoteAISuggestionsView.provenance(note, model: "m"), String(localized: "Summary by the notes AI (\("m"))", bundle: Bundle(for: AppModel.self)))
        note.titleSource = Note.aiSource
        XCTAssertEqual(NoteAISuggestionsView.provenance(note, model: "m"), String(localized: "Title and summary by the notes AI (\("m"))", bundle: Bundle(for: AppModel.self)))
    }
}
