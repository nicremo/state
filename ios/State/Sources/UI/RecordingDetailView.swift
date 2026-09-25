import SwiftUI

/// The recording of a voice note with its full transcript. While it plays,
/// the passage being spoken is marked and kept in view; tapping a passage
/// jumps there. Without timestamps the transcript is shown as plain text.
struct RecordingDetailView: View {
    @Bindable var model: AppModel
    let note: Note
    @State private var player = RecordingPlayer()
    @State private var loadFailed = false
    @State private var scrubbing: TimeInterval?
    @Environment(\.dismiss) private var dismiss

    private var timeline: RecordingTimeline {
        RecordingTimeline(attachments: note.attachments ?? [], durations: player.isLoaded ? player.durations : nil)
    }

    var body: some View {
        let timeline = timeline
        NavigationStack {
            VStack(spacing: 0) {
                transcript(timeline)
                Divider()
                controls(timeline)
            }
            .background(StateTheme.ground.ignoresSafeArea())
            .navigationTitle(String(localized: "Recording"))
            .stateInlineNavigationTitle()
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button(String(localized: "Done")) { dismiss() }
                }
                if !timeline.transcript.isEmpty {
                    ToolbarItem(placement: .primaryAction) {
                        ShareLink(item: timeline.transcript) {
                            Label(String(localized: "Share transcript"), systemImage: "square.and.arrow.up")
                        }
                        .accessibilityIdentifier("recording-share")
                    }
                }
            }
        }
        .accessibilityIdentifier("recording-screen")
        .task { await load() }
        .onDisappear { player.stop() }
    }

    // MARK: Transcript

    @ViewBuilder
    private func transcript(_ timeline: RecordingTimeline) -> some View {
        if timeline.hasTimestamps {
            let active = timeline.segmentIndex(at: scrubbing ?? player.currentTime)
            ScrollViewReader { proxy in
                ScrollView {
                    VStack(alignment: .leading, spacing: StateTheme.Space.tight) {
                        ForEach(timeline.segments) { segment in
                            segmentRow(segment, active: segment.id == active)
                        }
                    }
                    .padding(StateTheme.Space.section)
                }
                .onChange(of: active) { _, index in
                    guard let index, player.isPlaying || scrubbing != nil else { return }
                    withAnimation(.easeInOut(duration: 0.25)) {
                        proxy.scrollTo(index, anchor: .center)
                    }
                }
            }
        } else {
            ScrollView {
                VStack(alignment: .leading, spacing: StateTheme.Space.block) {
                    if timeline.transcript.isEmpty {
                        Text("No transcript yet. It appears once the notes AI has processed the recording.")
                            .font(.callout)
                            .foregroundStyle(.secondary)
                    } else {
                        Text(timeline.transcript)
                            .font(.body)
                            .foregroundStyle(StateTheme.graphite)
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .accessibilityIdentifier("recording-transcript")
                        Text("This transcript has no timestamps, so the spoken passage cannot be marked.")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                .padding(StateTheme.Space.section)
            }
        }
    }

    private func segmentRow(_ segment: RecordingTimeline.Segment, active: Bool) -> some View {
        Button {
            player.seek(to: segment.start)
            if !player.isPlaying { player.play() }
        } label: {
            Text(segment.text)
                .font(.body)
                .foregroundStyle(active ? StateTheme.graphite : StateTheme.graphite.opacity(0.6))
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.vertical, StateTheme.Space.snug)
                .padding(.horizontal, StateTheme.Space.inner)
                .background(
                    RoundedRectangle(cornerRadius: 8, style: .continuous)
                        .fill(active ? StateTheme.accentSoft : Color.clear)
                )
                .contentShape(Rectangle())
                .animation(.easeOut(duration: 0.2), value: active)
        }
        .buttonStyle(.plain)
        .id(segment.id)
        .accessibilityLabel(segment.text)
        .accessibilityValue(Self.clock(segment.start))
        .accessibilityHint(String(localized: "Plays the recording from here"))
        .accessibilityAddTraits(active ? .isSelected : [])
        .accessibilityIdentifier("recording-segment-\(segment.id)")
    }

    // MARK: Player

    private func controls(_ timeline: RecordingTimeline) -> some View {
        let duration = max(timeline.duration, player.duration)
        let position = scrubbing ?? player.currentTime
        return VStack(spacing: StateTheme.Space.inner) {
            if loadFailed {
                Text("The recording could not be loaded. Check the connection to your server.")
                    .font(.caption)
                    .foregroundStyle(.orange)
            }
            Slider(
                value: Binding(get: { min(position, max(duration, 0.1)) }, set: { scrubbing = $0 }),
                in: 0...max(duration, 0.1)
            ) { editing in
                if !editing, let target = scrubbing {
                    player.seek(to: target)
                    scrubbing = nil
                }
            }
            .tint(StateTheme.accent)
            .disabled(!player.isLoaded)
            .accessibilityLabel(String(localized: "Position"))
            .accessibilityValue(String(localized: "\(Self.clock(position)) of \(Self.clock(duration))"))
            .accessibilityIdentifier("recording-scrubber")
            HStack {
                Text(Self.clock(position))
                    .accessibilityIdentifier("recording-elapsed")
                Spacer()
                Text(Self.clock(duration))
                    .accessibilityIdentifier("recording-duration")
            }
            .font(.caption.monospacedDigit())
            .foregroundStyle(.secondary)
            .accessibilityElement(children: .ignore)
            HStack(spacing: StateTheme.Space.section) {
                Button { player.skip(by: -15) } label: {
                    Image(systemName: "gobackward.15").font(.title2)
                        .frame(width: StateControlMetrics.tapTarget, height: StateControlMetrics.tapTarget)
                }
                .accessibilityLabel(String(localized: "Back 15 seconds"))
                .accessibilityIdentifier("recording-back")
                Button { player.toggle() } label: {
                    Image(systemName: player.isPlaying ? "pause.circle.fill" : "play.circle.fill")
                        .font(.system(size: 52))
                        .frame(width: 64, height: 64)
                }
                .accessibilityLabel(player.isPlaying ? String(localized: "Pause") : String(localized: "Play recording"))
                .accessibilityIdentifier("recording-play")
                Button { player.skip(by: 15) } label: {
                    Image(systemName: "goforward.15").font(.title2)
                        .frame(width: StateControlMetrics.tapTarget, height: StateControlMetrics.tapTarget)
                }
                .accessibilityLabel(String(localized: "Forward 15 seconds"))
                .accessibilityIdentifier("recording-forward")
            }
            .buttonStyle(.plain)
            .foregroundStyle(StateTheme.accent)
            .disabled(!player.isLoaded)
        }
        .padding(.horizontal, StateTheme.Space.section)
        .padding(.vertical, StateTheme.Space.block)
        .background(StateTheme.ground)
    }

    private func load() async {
        guard !player.isLoaded else { return }
        var parts: [Data] = []
        for part in timeline.parts {
            guard let data = await model.attachmentData(noteID: note.id, attachment: part) else {
                loadFailed = true
                return
            }
            parts.append(data)
        }
        loadFailed = !player.load(parts)
    }

    static func clock(_ seconds: TimeInterval) -> String {
        Duration.seconds(max(0, seconds.rounded(.down))).formatted(.time(pattern: .minuteSecond))
    }
}
