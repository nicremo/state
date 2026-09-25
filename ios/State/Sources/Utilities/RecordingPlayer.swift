import AVFoundation
import Foundation
import Observation

/// Plays all parts of a recording as one: play, pause, seek anywhere on the
/// timeline and skip back or forward. `currentTime` is on the whole
/// recording's timeline, so the transcript can follow it.
@MainActor
@Observable
final class RecordingPlayer {
    private(set) var isPlaying = false
    private(set) var currentTime: TimeInterval = 0
    /// The measured length of each loaded part.
    private(set) var durations: [TimeInterval] = []
    private(set) var isLoaded = false

    private var players: [AVAudioPlayer] = []
    private var part = 0
    // Cancelled in deinit as well, should a view go away without stop().
    @ObservationIgnored nonisolated(unsafe) private var ticker: Task<Void, Never>?

    deinit {
        ticker?.cancel()
    }

    var duration: TimeInterval { durations.reduce(0, +) }

    private var offsets: [TimeInterval] {
        var running: TimeInterval = 0
        return durations.map { length in
            defer { running += length }
            return running
        }
    }

    /// Loads the parts in their order. It fails as a whole when one part is
    /// not playable, since the timeline would be wrong without it.
    @discardableResult
    func load(_ parts: [Data]) -> Bool {
        stop()
        let loaded = parts.compactMap { try? AVAudioPlayer(data: $0) }
        guard !loaded.isEmpty, loaded.count == parts.count else {
            players = []
            durations = []
            isLoaded = false
            return false
        }
        loaded.forEach { $0.prepareToPlay() }
        players = loaded
        durations = loaded.map(\.duration)
        part = 0
        currentTime = 0
        isLoaded = true
        return true
    }

    func toggle() {
        isPlaying ? pause() : play()
    }

    func play() {
        guard isLoaded else { return }
        if currentTime >= duration - 0.05 {
            seek(to: 0)
        }
        #if os(iOS)
        try? AVAudioSession.sharedInstance().setCategory(.playback, mode: .spokenAudio)
        try? AVAudioSession.sharedInstance().setActive(true)
        #endif
        guard players[part].play() else { return }
        isPlaying = true
        startTicker()
    }

    func pause() {
        players[safe: part]?.pause()
        isPlaying = false
        ticker?.cancel()
        ticker = nil
        refreshTime()
    }

    func stop() {
        players.forEach { $0.stop() }
        isPlaying = false
        ticker?.cancel()
        ticker = nil
    }

    func skip(by seconds: TimeInterval) {
        seek(to: currentTime + seconds)
    }

    /// Moves to `time` on the whole timeline, into the right part.
    func seek(to time: TimeInterval) {
        guard isLoaded else { return }
        let clamped = min(max(time, 0), duration)
        var target = 0
        for (index, offset) in offsets.enumerated() where offset <= clamped {
            target = index
        }
        if target != part {
            players[part].pause()
            part = target
        }
        players[part].currentTime = min(clamped - offsets[part], players[part].duration)
        currentTime = clamped
        if isPlaying {
            players[part].play()
        }
    }

    private func startTicker() {
        ticker?.cancel()
        ticker = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .milliseconds(100))
                self?.advance()
            }
        }
    }

    /// Follows the playing part and moves on to the next one when it ends.
    private func advance() {
        guard isPlaying, let player = players[safe: part] else { return }
        if player.isPlaying {
            refreshTime()
            return
        }
        if part + 1 < players.count {
            part += 1
            players[part].currentTime = 0
            players[part].play()
            refreshTime()
        } else {
            isPlaying = false
            ticker?.cancel()
            ticker = nil
            currentTime = duration
        }
    }

    private func refreshTime() {
        guard let player = players[safe: part] else { return }
        currentTime = min(offsets[part] + player.currentTime, duration)
    }
}

private extension Array {
    subscript(safe index: Int) -> Element? {
        indices.contains(index) ? self[index] : nil
    }
}
