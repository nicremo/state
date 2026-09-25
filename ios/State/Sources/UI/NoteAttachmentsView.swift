import SwiftUI

/// Everything the notes AI adds to a note, shown under its title: what it is
/// doing, the original photos and recordings with the text read from them,
/// the reminders it proposes and the notes it found related. The originals
/// always stay visible next to what the AI made of them.
struct NoteAIStatusView: View {
    @Bindable var model: AppModel
    let note: Note
    let uploads: [NoteUpload]

    var body: some View {
        if let line = statusLine {
            HStack(alignment: .firstTextBaseline, spacing: StateTheme.Space.inner) {
                if note.processing?.isWorking == true || !waitingUploads.isEmpty {
                    ProgressView().controlSize(.small)
                } else {
                    Image(systemName: line.symbol)
                        .foregroundStyle(line.tint)
                        .accessibilityHidden(true)
                }
                VStack(alignment: .leading, spacing: StateTheme.Space.tight) {
                    Text(line.text)
                        .font(.subheadline)
                        .foregroundStyle(StateTheme.graphite)
                    if let detail = line.detail {
                        Text(detail)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    if line.canRetry {
                        Button(String(localized: "Try again")) {
                            Task { await model.retryNoteProcessing(noteID: note.id) }
                        }
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(StateTheme.accent)
                        .buttonStyle(.plain)
                        .padding(.top, StateTheme.Space.tight)
                    }
                }
                Spacer(minLength: 0)
            }
            .padding(StateTheme.Space.block)
            .background(StateTheme.accentSoft, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("note-processing")
        }
    }

    private var waitingUploads: [NoteUpload] { uploads.filter { $0.status == NoteUpload.pending } }
    private var failedUploads: [NoteUpload] { uploads.filter { $0.status == NoteUpload.failed } }

    private struct Line {
        var symbol: String
        var tint: Color
        var text: String
        var detail: String?
        var canRetry = false
    }

    private var statusLine: Line? {
        if let failed = failedUploads.first {
            return Line(symbol: "exclamationmark.icloud", tint: .orange, text: String(localized: "A file was not uploaded"), detail: failed.error, canRetry: true)
        }
        if !waitingUploads.isEmpty {
            return Line(symbol: "icloud.and.arrow.up", tint: .secondary, text: String(localized: "Uploading \(waitingUploads.count) files"))
        }
        guard let processing = note.processing else { return nil }
        switch processing.status {
        case NoteProcessing.queued:
            return Line(symbol: "clock", tint: .secondary, text: String(localized: "Waiting for the notes AI"), detail: processing.error)
        case NoteProcessing.running:
            return Line(symbol: "sparkles", tint: StateTheme.accent, text: String(localized: "The notes AI is reading this note"))
        case NoteProcessing.needsReview:
            return Line(symbol: "eye", tint: .orange, text: String(localized: "Please check the text"), detail: String(localized: "Some words could not be read with certainty and are marked."))
        case NoteProcessing.failed:
            return Line(symbol: "exclamationmark.triangle", tint: .orange, text: String(localized: "The notes AI could not process this note"), detail: processing.error, canRetry: true)
        case NoteProcessing.consentRequired:
            return Line(symbol: "hand.raised", tint: .secondary, text: String(localized: "The notes AI is off"), detail: String(localized: "Turn it on in Settings, Notes AI. The original is kept."), canRetry: true)
        case NoteProcessing.notConfigured:
            return Line(symbol: "key", tint: .secondary, text: String(localized: "No OpenRouter key on your server"), detail: String(localized: "The note is kept. It is processed once a key is set up."), canRetry: true)
        case NoteProcessing.budgetExhausted:
            return Line(symbol: "gauge.with.dots.needle.100percent", tint: .orange, text: String(localized: "Monthly AI limit reached"), detail: String(localized: "Raise the limit in Settings or wait for next month."), canRetry: true)
        default:
            return nil
        }
    }
}

/// Photos in their order, then recordings with their transcripts.
struct NoteMediaView: View {
    @Bindable var model: AppModel
    let note: Note
    @State private var player = VoicePlayer()
    @State private var inspected: NoteAttachment?

    var body: some View {
        let attachments = (note.attachments ?? []).sorted { $0.ordinal < $1.ordinal }
        let images = attachments.filter(\.isImage)
        let recordings = attachments.filter(\.isAudio)
        VStack(alignment: .leading, spacing: StateTheme.Space.block) {
            if !images.isEmpty {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: StateTheme.Space.inner) {
                        ForEach(images) { attachment in
                            Button {
                                inspected = attachment
                            } label: {
                                AttachmentImage(model: model, noteID: note.id, attachment: attachment)
                                    .frame(width: 112, height: 148)
                                    .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                                    .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).stroke(StateTheme.graphite.opacity(0.1)))
                            }
                            .buttonStyle(.plain)
                            .accessibilityLabel(String(localized: "Photo \(attachment.ordinal + 1)"))
                            .accessibilityHint(String(localized: "Shows the photo and the text read from it"))
                        }
                    }
                }
            }
            ForEach(recordings) { recording in
                recordingRow(recording, part: (recordings.firstIndex(of: recording) ?? 0) + 1, parts: recordings.count)
            }
        }
        .sheet(item: $inspected) { attachment in
            PhotoInspector(model: model, noteID: note.id, attachment: attachment)
        }
        .onDisappear { player.stop() }
    }

    private func recordingRow(_ attachment: NoteAttachment, part: Int, parts: Int) -> some View {
        VStack(alignment: .leading, spacing: StateTheme.Space.snug) {
            HStack(spacing: StateTheme.Space.inner) {
                Button {
                    Task {
                        if let data = await model.attachmentData(noteID: note.id, attachment: attachment) {
                            player.toggle(id: attachment.id, data: data)
                        }
                    }
                } label: {
                    Image(systemName: player.playingID == attachment.id ? "pause.circle.fill" : "play.circle.fill")
                        .font(.title)
                        .foregroundStyle(StateTheme.accent)
                        .frame(width: StateControlMetrics.tapTarget, height: StateControlMetrics.tapTarget)
                }
                .buttonStyle(.plain)
                .accessibilityLabel(player.playingID == attachment.id ? String(localized: "Pause") : String(localized: "Play recording"))
                VStack(alignment: .leading, spacing: 0) {
                    Text(parts > 1 ? String(localized: "Recording, part \(part)") : String(localized: "Recording"))
                        .font(.subheadline.weight(.semibold))
                    if let duration = attachment.durationMs {
                        Text(Duration.milliseconds(duration), format: .time(pattern: .minuteSecond))
                            .font(.caption.monospacedDigit())
                            .foregroundStyle(.secondary)
                    }
                }
                Spacer(minLength: 0)
            }
            if let transcript = attachment.derivedText, !transcript.isEmpty {
                VStack(alignment: .leading, spacing: StateTheme.Space.tight) {
                    Text("Transcript")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.secondary)
                    Text(transcript)
                        .font(.callout)
                        .foregroundStyle(StateTheme.graphite.opacity(0.86))
                        .textSelection(.enabled)
                }
                .accessibilityIdentifier("note-transcript")
            }
        }
        .padding(StateTheme.Space.group)
        .background(StateTheme.accentSoft.opacity(0.6), in: RoundedRectangle(cornerRadius: 12, style: .continuous))
    }
}

/// One photo, loaded from this device or the server once.
struct AttachmentImage: View {
    @Bindable var model: AppModel
    let noteID: String
    let attachment: NoteAttachment
    @State private var image: Image?

    var body: some View {
        ZStack {
            StateTheme.accentSoft
            if let image {
                image.resizable().scaledToFill()
            } else {
                ProgressView().controlSize(.small)
            }
        }
        .task(id: attachment.sha256) {
            guard image == nil, let data = await model.attachmentData(noteID: noteID, attachment: attachment) else { return }
            image = Self.image(from: data)
        }
    }

    static func image(from data: Data) -> Image? {
        #if os(iOS)
        UIImage(data: data).map(Image.init(uiImage:))
        #else
        NSImage(data: data).map(Image.init(nsImage:))
        #endif
    }
}

/// The photo next to the text the AI read from it, so a misread word can be
/// checked against the page.
private struct PhotoInspector: View {
    @Bindable var model: AppModel
    let noteID: String
    let attachment: NoteAttachment
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: StateTheme.Space.block) {
                    AttachmentImage(model: model, noteID: noteID, attachment: attachment)
                        .aspectRatio(contentMode: .fit)
                        .frame(maxWidth: .infinity, minHeight: 240)
                        .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
                    if let text = attachment.derivedText, !text.isEmpty {
                        Text("Text read by the notes AI")
                            .font(.caption.weight(.semibold))
                            .foregroundStyle(.secondary)
                        Text(text)
                            .font(.body)
                            .textSelection(.enabled)
                            .accessibilityIdentifier("note-ocr")
                    } else {
                        Text("No text read yet.")
                            .font(.callout)
                            .foregroundStyle(.secondary)
                    }
                }
                .padding(StateTheme.Space.section)
            }
            .background(StateTheme.ground.ignoresSafeArea())
            .navigationTitle(String(localized: "Photo \(attachment.ordinal + 1)"))
            .stateInlineNavigationTitle()
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button(String(localized: "Done")) { dismiss() }
                }
            }
        }
    }
}

/// The AI's suggestions: a body for a note whose text was already started,
/// reminders to confirm, and related notes.
struct NoteAISuggestionsView: View {
    @Bindable var model: AppModel
    let note: Note
    var onOpenNote: ((String) -> Void)?

    var body: some View {
        VStack(alignment: .leading, spacing: StateTheme.Space.section) {
            if let proposed = note.ai?.proposedDocument, !proposed.isEmpty {
                section(String(localized: "Suggested by the notes AI")) {
                    MarkdownView(proposed, style: .document)
                    Button(String(localized: "Add to note")) {
                        Task { await model.adoptProposedDocument(noteID: note.id) }
                    }
                    .buttonStyle(.statePill)
                }
            }
            let proposals = (note.reminderProposals ?? []).filter { $0.status == ReminderProposal.pending }
            if !proposals.isEmpty {
                section(String(localized: "Proposed reminders")) {
                    ForEach(proposals) { proposal in
                        proposalCard(proposal)
                    }
                }
            }
            let relations = note.relations ?? []
            if !relations.isEmpty {
                section(String(localized: "Related notes")) {
                    ForEach(relations) { relation in
                        relationRow(relation)
                    }
                }
            }
            if let ai = note.ai, note.titleSource == Note.aiSource || note.summarySource == Note.aiSource {
                Label(String(localized: "Title and summary by the notes AI (\(ai.model))"), systemImage: "sparkles")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .accessibilityIdentifier("note-ai-provenance")
            }
        }
    }

    private func section<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: StateTheme.Space.inner) {
            Text(title)
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                .textCase(.uppercase)
                .accessibilityAddTraits(.isHeader)
            content()
        }
    }

    private func proposalCard(_ proposal: ReminderProposal) -> some View {
        VStack(alignment: .leading, spacing: StateTheme.Space.snug) {
            Text(proposal.title)
                .font(.headline)
            if let date = proposal.localDate {
                Label([date, proposal.localTime].compactMap { $0 }.joined(separator: " "), systemImage: "calendar")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            if let reason = proposal.reason, !reason.isEmpty {
                Text("From the note: \(reason)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            HStack(spacing: StateTheme.Space.inner) {
                Button(String(localized: "Create reminder")) {
                    Task { await model.acceptReminderProposal(noteID: note.id, proposal: proposal) }
                }
                .buttonStyle(.statePill)
                Button(String(localized: "Dismiss")) {
                    Task { await model.dismissReminderProposal(noteID: note.id, proposal: proposal) }
                }
                .buttonStyle(.plain)
                .foregroundStyle(StateTheme.accent)
                .frame(minHeight: StateControlMetrics.tapTarget)
            }
        }
        .padding(StateTheme.Space.group)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(StateTheme.accentSoft, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .accessibilityIdentifier("note-proposal")
    }

    private func relationRow(_ relation: NoteRelation) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: StateTheme.Space.inner) {
            Button {
                onOpenNote?(relation.relatedNoteID)
            } label: {
                VStack(alignment: .leading, spacing: StateTheme.Space.tight) {
                    Text(relation.relatedTitle ?? String(localized: "Note"))
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(StateTheme.accent)
                    Text(relation.reason)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .disabled(onOpenNote == nil)
            Button {
                Task { await model.dismissNoteRelation(noteID: note.id, relation: relation) }
            } label: {
                Image(systemName: "xmark.circle")
                    .foregroundStyle(.secondary)
                    .frame(width: StateControlMetrics.tapTarget, height: StateControlMetrics.tapTarget)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(String(localized: "Remove link"))
        }
        .accessibilityIdentifier("note-relation")
    }
}

/// Settings for the notes AI: consent first, then the monthly limit, what
/// was spent, and which models run.
struct NoteAISettingsView: View {
    @Bindable var model: AppModel
    @State private var limit: Double = 10

    var body: some View {
        Form {
            if let settings = model.notesAISettings {
                Section {
                    Toggle(isOn: Binding(
                        get: { settings.consent },
                        set: { value in Task { await model.updateNotesAISettings(consent: value) } }
                    )) {
                        Text("Process notes with AI")
                    }
                    .accessibilityIdentifier("notes-ai-consent")
                } footer: {
                    Text("Photos, recordings and note text are sent through OpenRouter to the selected model provider for processing. The OpenRouter key stays on your server; this device never sees it. Originals are always kept.")
                }

                Section {
                    Stepper(value: $limit, in: 1...100, step: 1) {
                        LabeledContent(String(localized: "Monthly limit"), value: limit.formatted(.currency(code: "USD")))
                    } onEditingChanged: { editing in
                        if !editing, limit != settings.monthlyLimitUsd {
                            Task { await model.updateNotesAISettings(monthlyLimitUSD: limit) }
                        }
                    }
                    .accessibilityIdentifier("notes-ai-limit")
                    LabeledContent(String(localized: "Spent this month"), value: settings.spentThisMonthUsd.formatted(.currency(code: "USD").precision(.fractionLength(2...4))))
                } footer: {
                    Text("Once the limit is reached, notes stay usable and wait for next month or a higher limit.")
                }

                Section(String(localized: "Models")) {
                    LabeledContent(String(localized: "Notes agent"), value: settings.agentModel)
                    LabeledContent(String(localized: "Transcription"), value: settings.transcriptionModel)
                    LabeledContent(String(localized: "Photos per note"), value: "\(model.noteCapabilities.photoLimit)")
                    LabeledContent(String(localized: "OpenRouter key on the server")) {
                        Text(settings.keyConfigured ? String(localized: "Set up") : String(localized: "Missing"))
                            .foregroundStyle(settings.keyConfigured ? Color.secondary : Color.orange)
                    }
                }
            } else {
                ProgressView()
                    .frame(maxWidth: .infinity)
            }
        }
        .formStyle(.grouped)
        .navigationTitle(String(localized: "Notes AI"))
        .stateBackground()
        .task {
            await model.loadNotesAISettings()
            if let settings = model.notesAISettings { limit = settings.monthlyLimitUsd }
        }
    }
}
