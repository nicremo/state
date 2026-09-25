import AVFoundation
import Foundation
import ImageIO
import Observation
import UniformTypeIdentifiers

/// Prepares photos for a note: at most 2560 pixels on the long side, JPEG,
/// orientation applied. Handwriting stays legible and the upload stays small.
enum NoteImagePreparation {
    static let maxPixelSize = 2560
    static let quality = 0.85

    static func jpeg(from data: Data) -> Data? {
        guard let source = CGImageSourceCreateWithData(data as CFData, nil) else { return nil }
        let options: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceThumbnailMaxPixelSize: maxPixelSize,
        ]
        guard let image = CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary) else { return nil }
        let output = NSMutableData()
        guard let destination = CGImageDestinationCreateWithData(output, UTType.jpeg.identifier as CFString, 1, nil) else { return nil }
        CGImageDestinationAddImage(destination, image, [kCGImageDestinationLossyCompressionQuality: quality] as CFDictionary)
        guard CGImageDestinationFinalize(destination) else { return nil }
        return output as Data
    }

    static func media(from data: Data) -> AppModel.CapturedMedia? {
        jpeg(from: data).map { AppModel.CapturedMedia(data: $0, mimeType: "image/jpeg", fileExtension: "jpg") }
    }
}

/// Records a voice note as AAC in segments. The server gives the segment
/// length: OpenRouter's transcription providers stop after about a minute
/// of work per request, so a long recording is several short ones.
@MainActor
@Observable
final class VoiceRecorder {
    enum Phase: Equatable {
        case idle
        case recording
        case denied
        case failed(String)
    }

    private(set) var phase: Phase = .idle
    private(set) var elapsed: TimeInterval = 0
    private(set) var level: Double = 0
    private(set) var segmentCount = 0
    private(set) var reachedLimit = false

    private var recorder: AVAudioRecorder?
    private var segments: [(url: URL, duration: TimeInterval)] = []
    private var segmentStart = Date()
    private var finishedDuration: TimeInterval = 0
    private var ticker: Task<Void, Never>?
    private var segmentSeconds: TimeInterval = 300
    private var maxSegments = 12

    static let settings: [String: Any] = [
        AVFormatIDKey: kAudioFormatMPEG4AAC,
        AVSampleRateKey: 32000,
        AVNumberOfChannelsKey: 1,
        AVEncoderBitRateKey: 48000,
        AVEncoderAudioQualityKey: AVAudioQuality.medium.rawValue,
    ]

    /// Whether the current segment is full and the next one should start.
    nonisolated static func shouldRollOver(segmentElapsed: TimeInterval, segmentSeconds: TimeInterval) -> Bool {
        segmentSeconds > 0 && segmentElapsed >= segmentSeconds
    }

    func start(segmentSeconds: Int, maxSegments: Int) async {
        self.segmentSeconds = TimeInterval(max(segmentSeconds, 10))
        self.maxSegments = max(maxSegments, 1)
        guard await AVAudioApplication.requestRecordPermission() else {
            phase = .denied
            return
        }
        #if os(iOS)
        do {
            let session = AVAudioSession.sharedInstance()
            try session.setCategory(.playAndRecord, mode: .spokenAudio, options: [.defaultToSpeaker])
            try session.setActive(true)
        } catch {
            phase = .failed(error.localizedDescription)
            return
        }
        #endif
        startSegment()
        ticker = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .milliseconds(100))
                self?.tick()
            }
        }
    }

    private func startSegment() {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent("voice-\(UUID().uuidString).m4a")
        do {
            let recorder = try AVAudioRecorder(url: url, settings: Self.settings)
            recorder.isMeteringEnabled = true
            guard recorder.record() else {
                phase = .failed(String(localized: "The recording could not start."))
                return
            }
            self.recorder = recorder
            segmentStart = Date()
            segmentCount = segments.count + 1
            phase = .recording
        } catch {
            phase = .failed(error.localizedDescription)
        }
    }

    private func finishSegment() {
        guard let recorder else { return }
        let duration = recorder.currentTime
        recorder.stop()
        segments.append((recorder.url, duration))
        finishedDuration += duration
        self.recorder = nil
    }

    private func tick() {
        guard phase == .recording, let recorder else { return }
        recorder.updateMeters()
        // -50 dB is silence for a phone microphone in a quiet room.
        level = max(0, min(1, (Double(recorder.averagePower(forChannel: 0)) + 50) / 50))
        let segmentElapsed = recorder.currentTime
        elapsed = finishedDuration + segmentElapsed
        if Self.shouldRollOver(segmentElapsed: segmentElapsed, segmentSeconds: segmentSeconds) {
            finishSegment()
            if segments.count < maxSegments {
                startSegment()
            } else {
                reachedLimit = true
                stopTicking()
            }
        }
    }

    /// Ends the recording and returns its segments, oldest first.
    func finish() -> [AppModel.CapturedMedia] {
        finishSegment()
        stopTicking()
        let media = segments.compactMap { segment -> AppModel.CapturedMedia? in
            guard let data = try? Data(contentsOf: segment.url), !data.isEmpty else { return nil }
            return AppModel.CapturedMedia(data: data, mimeType: "audio/mp4", fileExtension: "m4a", durationMs: Int64(segment.duration * 1000))
        }
        removeFiles()
        return media
    }

    /// Throws the recording away. Nothing is stored.
    func cancel() {
        recorder?.stop()
        recorder = nil
        stopTicking()
        removeFiles()
    }

    private func stopTicking() {
        ticker?.cancel()
        ticker = nil
        if phase == .recording { phase = .idle }
        #if os(iOS)
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
        #endif
    }

    private func removeFiles() {
        for segment in segments {
            try? FileManager.default.removeItem(at: segment.url)
        }
        segments = []
    }
}

/// Plays one recording of a note.
@MainActor
@Observable
final class VoicePlayer {
    private(set) var playingID: String?
    private var player: AVAudioPlayer?
    private var watcher: Task<Void, Never>?

    func toggle(id: String, data: Data) {
        if playingID == id {
            stop()
            return
        }
        stop()
        #if os(iOS)
        try? AVAudioSession.sharedInstance().setCategory(.playback, mode: .spokenAudio)
        try? AVAudioSession.sharedInstance().setActive(true)
        #endif
        guard let player = try? AVAudioPlayer(data: data), player.play() else { return }
        self.player = player
        playingID = id
        watcher = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .milliseconds(250))
                guard let self, let player = self.player else { return }
                if !player.isPlaying {
                    self.stop()
                    return
                }
            }
        }
    }

    func stop() {
        player?.stop()
        player = nil
        playingID = nil
        watcher?.cancel()
        watcher = nil
    }
}
