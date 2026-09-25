import Foundation

/// A voice note's recording as the owner hears it: all parts one after the
/// other, with the transcript's passages placed on that one timeline. The
/// server stores times per part; the offsets of earlier parts are added here.
struct RecordingTimeline: Equatable {
    struct Segment: Hashable, Identifiable {
        let id: Int
        let start: TimeInterval
        let end: TimeInterval
        let text: String
    }

    /// The recording's parts in the order they were recorded.
    let parts: [NoteAttachment]
    /// Start of each part on the timeline.
    let offsets: [TimeInterval]
    let duration: TimeInterval
    /// Passages in time order. Empty when no part has timestamps.
    let segments: [Segment]
    let transcript: String

    var hasTimestamps: Bool { !segments.isEmpty }

    /// `durations` are the measured lengths of the loaded audio, which win
    /// over the lengths stored with the upload.
    init(attachments: [NoteAttachment], durations: [TimeInterval]? = nil) {
        let parts = attachments.filter(\.isAudio).sorted { $0.ordinal < $1.ordinal }
        let lengths: [TimeInterval] = parts.enumerated().map { index, part in
            if let durations, index < durations.count, durations[index] > 0 { return durations[index] }
            if let stored = part.durationMs, stored > 0 { return TimeInterval(stored) / 1000 }
            return TimeInterval(part.segments?.map(\.endMs).max() ?? 0) / 1000
        }
        var offsets: [TimeInterval] = []
        var running: TimeInterval = 0
        for length in lengths {
            offsets.append(running)
            running += length
        }
        let timed = parts.contains { !($0.segments ?? []).isEmpty }
        var segments: [Segment] = []
        if timed {
            for (index, part) in parts.enumerated() {
                let offset = offsets[index]
                if let partSegments = part.segments, !partSegments.isEmpty {
                    for segment in partSegments.sorted(by: { $0.startMs < $1.startMs }) {
                        let start = offset + TimeInterval(segment.startMs) / 1000
                        let end = offset + TimeInterval(segment.endMs) / 1000
                        for sentence in Self.sentences(of: segment.text, from: start, to: end) {
                            segments.append(Segment(id: segments.count, start: sentence.start, end: sentence.end, text: sentence.text))
                        }
                    }
                } else if let text = part.derivedText, !text.isEmpty {
                    segments.append(Segment(id: segments.count, start: offset, end: offset + lengths[index], text: text))
                }
            }
        }
        self.parts = parts
        self.offsets = offsets
        self.duration = running
        self.segments = segments
        self.transcript = parts.compactMap(\.derivedText).filter { !$0.isEmpty }.joined(separator: "\n\n")
    }

    /// Splits a passage into its sentences and gives each a share of the
    /// passage's time by its length. Some providers return one passage for
    /// a whole short recording, which would mark everything at once.
    static func sentences(of text: String, from start: TimeInterval, to end: TimeInterval) -> [(text: String, start: TimeInterval, end: TimeInterval)] {
        var parts: [String] = []
        var current = ""
        let characters = Array(text)
        for (index, character) in characters.enumerated() {
            current.append(character)
            let next = index + 1 < characters.count ? characters[index + 1] : nil
            // A sentence ends at . ! ? followed by a space, not inside
            // "CLAUDE.md" or "2.5".
            if ".!?".contains(character), next == nil || next == " " {
                parts.append(current.trimmingCharacters(in: .whitespaces))
                current = ""
            }
        }
        let rest = current.trimmingCharacters(in: .whitespaces)
        if !rest.isEmpty { parts.append(rest) }
        parts = parts.filter { !$0.isEmpty }
        guard parts.count > 1 else { return [(text.trimmingCharacters(in: .whitespaces), start, end)] }
        let total = Double(parts.reduce(0) { $0 + $1.count })
        var cursor = start
        return parts.enumerated().map { index, part in
            let length = index == parts.count - 1 ? end - cursor : (end - start) * Double(part.count) / total
            defer { cursor += length }
            return (part, cursor, cursor + length)
        }
    }

    /// The passage spoken at `time`: the last one that started. Between two
    /// passages the previous one stays marked, so the highlight never flickers.
    func segmentIndex(at time: TimeInterval) -> Int? {
        guard let first = segments.first, time >= first.start else { return nil }
        var low = 0
        var high = segments.count - 1
        while low < high {
            let middle = (low + high + 1) / 2
            if segments[middle].start <= time {
                low = middle
            } else {
                high = middle - 1
            }
        }
        return low
    }

    /// Which part plays at `time`, and where in it.
    func location(of time: TimeInterval) -> (part: Int, offset: TimeInterval) {
        guard !parts.isEmpty else { return (0, 0) }
        let clamped = min(max(time, 0), duration)
        var part = 0
        for index in offsets.indices where offsets[index] <= clamped {
            part = index
        }
        return (part, clamped - offsets[part])
    }
}
