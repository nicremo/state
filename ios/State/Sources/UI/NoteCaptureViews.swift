import PhotosUI
import SwiftUI
#if os(iOS)
import VisionKit
#endif

/// The one large plus of the notes list, bottom right, as asked for: it
/// offers exactly Text, Photo and Voice.
struct NoteCaptureButton: View {
    var onText: () -> Void
    var onPhoto: () -> Void
    var onVoice: () -> Void

    var body: some View {
        Menu {
            Button(action: onText) {
                Label(String(localized: "Text"), systemImage: "text.alignleft")
            }
            Button(action: onPhoto) {
                Label(String(localized: "Photo"), systemImage: "photo.on.rectangle")
            }
            Button(action: onVoice) {
                Label(String(localized: "Voice"), systemImage: "waveform")
            }
        } label: {
            Image(systemName: "plus")
                .font(.title2.weight(.semibold))
                .foregroundStyle(StateTheme.onAccent)
                .frame(width: 60, height: 60)
                .background(StateTheme.accent, in: Circle())
                .shadow(color: .black.opacity(0.18), radius: 10, y: 4)
                .contentShape(Circle())
        }
        .menuIndicator(.hidden)
        .buttonStyle(.plain)
        .accessibilityLabel(String(localized: "New note"))
        .accessibilityIdentifier("note-capture")
        .padding(StateTheme.Space.section)
    }
}

/// Photos for a note: take them with the document camera, which handles
/// several notebook pages in one go, or pick them from the library. The
/// most the picker allows comes from the server.
struct PhotoCaptureSheet: View {
    let capabilities: NoteCapabilities
    let onComplete: ([AppModel.CapturedMedia]) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var items: [PhotosPickerItem] = []
    @State private var isPreparing = false
    @State private var scans = false
    @State private var failed = false

    var body: some View {
        NavigationStack {
            VStack(alignment: .leading, spacing: StateTheme.Space.block) {
                Text(limitText)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("photo-limit")

                #if os(iOS)
                if VNDocumentCameraViewController.isSupported {
                    Button {
                        scans = true
                    } label: {
                        Label(String(localized: "Take photos"), systemImage: "camera")
                            .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.statePrimary)
                }
                #endif

                PhotosPicker(
                    selection: $items,
                    maxSelectionCount: capabilities.photoLimit,
                    selectionBehavior: .ordered,
                    matching: .images
                ) {
                    Label(String(localized: "Choose from library"), systemImage: "photo.on.rectangle")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.stateSecondary)
                .accessibilityIdentifier("photo-library")

                if isPreparing {
                    ProgressView(String(localized: "Preparing photos"))
                        .frame(maxWidth: .infinity)
                }
                if failed {
                    Label(String(localized: "These photos could not be read."), systemImage: "exclamationmark.triangle")
                        .font(.callout)
                        .foregroundStyle(.orange)
                }
                Spacer(minLength: 0)
            }
            .padding(StateTheme.Space.section)
            .frame(maxWidth: 560)
            .frame(maxWidth: .infinity)
            .background(StateTheme.ground.ignoresSafeArea())
            .navigationTitle(String(localized: "Photo"))
            .stateInlineNavigationTitle()
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button(String(localized: "Cancel")) { dismiss() }
                }
            }
        }
        .onChange(of: items) { _, selected in
            guard !selected.isEmpty else { return }
            Task { await prepare(selected) }
        }
        #if os(iOS)
        .fullScreenCover(isPresented: $scans) {
            DocumentScanner(limit: capabilities.photoLimit) { images in
                scans = false
                guard !images.isEmpty else { return }
                finish(images.compactMap(NoteImagePreparation.media(from:)))
            }
            .ignoresSafeArea()
        }
        #endif
        .presentationDetents([.medium, .large])
    }

    private var limitText: String {
        let limit = capabilities.photoLimit
        if capabilities.vision.available {
            return String(localized: "Up to \(limit) photos per note, the limit of \(capabilities.vision.model). Handwritten pages become text.")
        }
        return String(localized: "Up to \(limit) photo per note while the notes AI is not available. The photo is kept and processed later.")
    }

    private func prepare(_ selected: [PhotosPickerItem]) async {
        isPreparing = true
        failed = false
        var media: [AppModel.CapturedMedia] = []
        for item in selected.prefix(capabilities.photoLimit) {
            if let data = try? await item.loadTransferable(type: Data.self), let prepared = NoteImagePreparation.media(from: data) {
                media.append(prepared)
            }
        }
        isPreparing = false
        items = []
        guard !media.isEmpty else {
            failed = true
            return
        }
        finish(media)
    }

    private func finish(_ media: [AppModel.CapturedMedia]) {
        guard !media.isEmpty else {
            failed = true
            return
        }
        onComplete(media)
        dismiss()
    }
}

#if os(iOS)
/// The system document camera: several pages, straightened and cropped.
private struct DocumentScanner: UIViewControllerRepresentable {
    let limit: Int
    let onFinish: ([Data]) -> Void

    func makeUIViewController(context: Context) -> VNDocumentCameraViewController {
        let controller = VNDocumentCameraViewController()
        controller.delegate = context.coordinator
        return controller
    }

    func updateUIViewController(_ controller: VNDocumentCameraViewController, context: Context) {}

    func makeCoordinator() -> Coordinator {
        Coordinator(limit: limit, onFinish: onFinish)
    }

    final class Coordinator: NSObject, VNDocumentCameraViewControllerDelegate {
        let limit: Int
        let onFinish: ([Data]) -> Void

        init(limit: Int, onFinish: @escaping ([Data]) -> Void) {
            self.limit = limit
            self.onFinish = onFinish
        }

        func documentCameraViewController(_ controller: VNDocumentCameraViewController, didFinishWith scan: VNDocumentCameraScan) {
            let pages = (0..<min(scan.pageCount, limit)).compactMap { scan.imageOfPage(at: $0).jpegData(compressionQuality: 0.95) }
            onFinish(pages)
        }

        func documentCameraViewControllerDidCancel(_ controller: VNDocumentCameraViewController) {
            onFinish([])
        }

        func documentCameraViewController(_ controller: VNDocumentCameraViewController, didFailWithError error: Error) {
            onFinish([])
        }
    }
}
#endif

/// A voice note: record, see it is working, stop to keep it. Cancel throws
/// it away. Long recordings are split in segments the server can transcribe.
struct VoiceCaptureSheet: View {
    let capabilities: NoteCapabilities
    let onComplete: ([AppModel.CapturedMedia]) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var recorder = VoiceRecorder()

    var body: some View {
        NavigationStack {
            VStack(spacing: StateTheme.Space.section) {
                Spacer(minLength: 0)
                switch recorder.phase {
                case .denied:
                    ContentUnavailableView(
                        String(localized: "No microphone access"),
                        systemImage: "mic.slash",
                        description: Text("Allow State to use the microphone in Settings to record voice notes.")
                    )
                case let .failed(message):
                    ContentUnavailableView(String(localized: "Recording failed"), systemImage: "exclamationmark.triangle", description: Text(message))
                default:
                    recording
                }
                Spacer(minLength: 0)
            }
            .padding(StateTheme.Space.section)
            .frame(maxWidth: .infinity)
            .background(StateTheme.ground.ignoresSafeArea())
            .navigationTitle(String(localized: "Voice"))
            .stateInlineNavigationTitle()
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button(String(localized: "Cancel")) {
                        recorder.cancel()
                        dismiss()
                    }
                }
            }
        }
        .task {
            await recorder.start(segmentSeconds: capabilities.audio.segmentSeconds, maxSegments: capabilities.audio.maxSegments)
        }
        .presentationDetents([.medium])
        .interactiveDismissDisabled(recorder.elapsed > 0)
    }

    private var recording: some View {
        VStack(spacing: StateTheme.Space.block) {
            Text(Duration.seconds(recorder.elapsed), format: .time(pattern: .minuteSecond))
                .font(.system(size: 48, weight: .semibold, design: .rounded).monospacedDigit())
                .foregroundStyle(StateTheme.graphite)
                .accessibilityIdentifier("voice-elapsed")
            Capsule()
                .fill(StateTheme.accentSoft)
                .frame(width: 220, height: 8)
                .overlay(alignment: .leading) {
                    Capsule()
                        .fill(StateTheme.accent)
                        .frame(width: max(8, 220 * recorder.level), height: 8)
                        .animation(.linear(duration: 0.1), value: recorder.level)
                }
                .accessibilityHidden(true)
            if recorder.segmentCount > 1 {
                Text("Part \(recorder.segmentCount)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if recorder.reachedLimit {
                Text("The longest recording is reached.")
                    .font(.caption)
                    .foregroundStyle(.orange)
            }
            Button {
                let media = recorder.finish()
                if !media.isEmpty { onComplete(media) }
                dismiss()
            } label: {
                Label(String(localized: "Stop and save"), systemImage: "stop.fill")
                    .frame(maxWidth: 280)
            }
            .buttonStyle(.statePrimary)
            .disabled(recorder.phase != .recording && !recorder.reachedLimit)
            .accessibilityIdentifier("voice-stop")
        }
    }
}
