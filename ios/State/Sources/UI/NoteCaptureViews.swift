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

/// The plus button with its photo and voice sheets. The iPhone puts it on
/// the notes list; the iPad and the Mac put it bottom right of the note
/// column, which stays on screen where a list column may be folded away.
struct NoteCaptureHost: ViewModifier {
    @Bindable var model: AppModel
    var isEnabled: Bool
    var onText: () -> Void
    /// Receives the identifier of a new photo or voice note to open it.
    var onCreated: (String) -> Void

    @State private var capturesPhoto = false
    @State private var capturesVoice = false

    func body(content: Content) -> some View {
        content
            .overlay(alignment: .bottomTrailing) {
                if isEnabled {
                    NoteCaptureButton(
                        onText: onText,
                        onPhoto: { capturesPhoto = true },
                        onVoice: { capturesVoice = true }
                    )
                }
            }
            .sheet(isPresented: $capturesPhoto) {
                PhotoCaptureSheet(capabilities: model.noteCapabilities) { media in
                    create(kind: "image", media: media)
                }
                .task { await model.refreshNoteCapabilities() }
            }
            .sheet(isPresented: $capturesVoice) {
                VoiceCaptureSheet(capabilities: model.noteCapabilities) { media in
                    create(kind: "audio", media: media)
                }
            }
            #if DEBUG
            .task {
                guard isEnabled else { return }
                switch StateLaunch.initialCapture {
                case "image": capturesPhoto = true
                case "audio": capturesVoice = true
                default: break
                }
            }
            #endif
    }

    /// Stores a photo or voice note and opens it, so the owner sees the
    /// upload and the AI at work.
    private func create(kind: String, media: [AppModel.CapturedMedia]) {
        Task {
            if let identifier = await model.createCaptureNote(kind: kind, media: media) {
                onCreated(identifier)
            }
        }
    }
}

extension View {
    func noteCapture(model: AppModel, isEnabled: Bool, onText: @escaping () -> Void, onCreated: @escaping (String) -> Void) -> some View {
        modifier(NoteCaptureHost(model: model, isEnabled: isEnabled, onText: onText, onCreated: onCreated))
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
                if VNDocumentCameraViewController.isSupported, capabilities.acceptsPhotos {
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
                .disabled(!capabilities.acceptsPhotos)
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
            DocumentScanner(limit: capabilities.photoLimit) { pages in
                scans = false
                guard !pages.isEmpty else { return }
                isPreparing = true
                Task {
                    let media = await Task.detached(priority: .userInitiated) {
                        pages.compactMap { page in page.jpegData(compressionQuality: 1).flatMap(NoteImagePreparation.media(from:)) }
                    }.value
                    isPreparing = false
                    finish(media)
                }
            }
            .ignoresSafeArea()
        }
        #endif
        .presentationDetents([.medium, .large])
    }

    private var limitText: String {
        let limit = capabilities.photoLimit
        if !capabilities.acceptsPhotos {
            return String(localized: "The model \(capabilities.agent.model) cannot read photos. Choose a model that reads images in the server settings.")
        }
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
            guard let data = try? await item.loadTransferable(type: Data.self) else { continue }
            // Decoding a full-size photo is heavy; it stays off the main thread.
            if let prepared = await Task.detached(priority: .userInitiated, operation: { NoteImagePreparation.media(from: data) }).value {
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
    let onFinish: ([UIImage]) -> Void

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
        let onFinish: ([UIImage]) -> Void

        init(limit: Int, onFinish: @escaping ([UIImage]) -> Void) {
            self.limit = limit
            self.onFinish = onFinish
        }

        func documentCameraViewController(_ controller: VNDocumentCameraViewController, didFinishWith scan: VNDocumentCameraScan) {
            // The pages are encoded off the main thread by the sheet.
            onFinish((0..<min(scan.pageCount, limit)).map { scan.imageOfPage(at: $0) })
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
                case let .failed(message) where !recorder.hasRecording:
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
        // Leaving without Stop keeps nothing, not even a temporary file.
        .onDisappear { recorder.cancel() }
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
            if recorder.phase == .paused {
                Text("Paused by a call or another app. What was recorded is kept.")
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .multilineTextAlignment(.center)
            }
            if case let .failed(message) = recorder.phase {
                Text(message)
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .multilineTextAlignment(.center)
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
            .disabled(!recorder.hasRecording)
            .accessibilityIdentifier("voice-stop")
        }
    }
}
