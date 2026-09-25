import SwiftUI

/// The owner's dictionary for voice notes: words the notes AI spells exactly
/// so, and corrections for what speech recognition mishears. It lives on the
/// owner's server; only the owner and the owner's devices change it.
struct NotesDictionaryView: View {
    @Bindable var model: AppModel
    @State private var wordRows: [WordRow] = []
    @State private var correctionRows: [CorrectionRow] = []
    @State private var revision: Int64 = 0
    @State private var saved: NotesDictionary?
    @State private var message: String?
    @State private var saving = false
    @State private var importing = false
    @State private var importText = ""
    @State private var importMessage: String?

    private struct WordRow: Identifiable, Hashable {
        let id = UUID()
        var text: String
    }

    private struct CorrectionRow: Identifiable, Hashable {
        let id = UUID()
        var from: String
        var to: String
        var mode: DictionaryCorrection.Mode
    }

    private var draft: NotesDictionary {
        NotesDictionary(
            words: wordRows.map { $0.text.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty },
            corrections: correctionRows
                .map { DictionaryCorrection(from: $0.from.trimmingCharacters(in: .whitespaces), to: $0.to.trimmingCharacters(in: .whitespaces), mode: $0.mode) }
                .filter { !$0.from.isEmpty && !$0.to.isEmpty },
            revision: revision
        )
    }

    private var hasChanges: Bool {
        guard let saved else { return false }
        return draft.words != saved.words || draft.corrections != saved.corrections
    }

    var body: some View {
        Form {
            if saved == nil {
                ProgressView().frame(maxWidth: .infinity)
            } else {
                wordsSection
                correctionsSection
                Section {
                    Button(String(localized: "Paste a list")) {
                        importText = ""
                        importMessage = nil
                        importing = true
                    }
                    .accessibilityIdentifier("dictionary-import")
                } footer: {
                    Text("One entry per line: word: Term, always: heard -> meant, or context: heard -> meant.")
                }
                if let message {
                    Section {
                        Text(message)
                            .font(.footnote)
                            .foregroundStyle(.orange)
                            .accessibilityIdentifier("dictionary-message")
                    }
                }
            }
        }
        .formStyle(.grouped)
        .navigationTitle(String(localized: "Dictionary"))
        .stateBackground()
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button {
                    Task { await save() }
                } label: {
                    if saving {
                        ProgressView()
                    } else {
                        Text("Save")
                    }
                }
                .disabled(!hasChanges || saving)
                .accessibilityIdentifier("dictionary-save")
            }
        }
        .sheet(isPresented: $importing) { importSheet }
        .task {
            await model.loadNotesDictionary()
            apply(model.notesDictionary ?? .empty)
        }
    }

    private var wordsSection: some View {
        Section {
            ForEach($wordRows) { $row in
                TextField(String(localized: "Word or name"), text: $row.text)
                    .autocorrectionDisabled()
                    #if os(iOS)
                    .textInputAutocapitalization(.never)
                    #endif
                    .accessibilityIdentifier("dictionary-word")
            }
            .onDelete { wordRows.remove(atOffsets: $0) }
            Button(String(localized: "Add word")) { wordRows.append(WordRow(text: "")) }
                .accessibilityIdentifier("dictionary-add-word")
        } header: {
            Text("Words")
        } footer: {
            Text("Names and terms the notes AI spells exactly like this.")
        }
    }

    private var correctionsSection: some View {
        Section {
            ForEach($correctionRows) { $row in
                VStack(alignment: .leading, spacing: StateTheme.Space.inner) {
                    HStack(spacing: StateTheme.Space.inner) {
                        TextField(String(localized: "Heard"), text: $row.from)
                            .accessibilityIdentifier("dictionary-heard")
                        Image(systemName: "arrow.right")
                            .foregroundStyle(.secondary)
                            .accessibilityHidden(true)
                        TextField(String(localized: "Meant"), text: $row.to)
                            .accessibilityIdentifier("dictionary-meant")
                    }
                    .autocorrectionDisabled()
                    #if os(iOS)
                    .textInputAutocapitalization(.never)
                    #endif
                    Picker(String(localized: "Replace"), selection: $row.mode) {
                        Text("Always").tag(DictionaryCorrection.Mode.always)
                        Text("By context").tag(DictionaryCorrection.Mode.context)
                    }
                    .pickerStyle(.segmented)
                    .accessibilityIdentifier("dictionary-mode")
                }
                .padding(.vertical, StateTheme.Space.tight)
            }
            .onDelete { correctionRows.remove(atOffsets: $0) }
            Button(String(localized: "Add correction")) {
                correctionRows.append(CorrectionRow(from: "", to: "", mode: .always))
            }
            .accessibilityIdentifier("dictionary-add-correction")
        } header: {
            Text("Misheard")
        } footer: {
            Text("Always replaces the word in every transcript, only as a whole word. By context is for words that also exist in everyday speech, such as Note: the notes AI corrects them only where the context fits. The original transcript is kept.")
        }
    }

    private var importSheet: some View {
        NavigationStack {
            VStack(alignment: .leading, spacing: StateTheme.Space.inner) {
                TextEditor(text: $importText)
                    .font(.body.monospaced())
                    .autocorrectionDisabled()
                    #if os(iOS)
                    .textInputAutocapitalization(.never)
                    #endif
                    .scrollContentBackground(.hidden)
                    .padding(StateTheme.Space.inner)
                    .background(StateTheme.accentSoft.opacity(0.5), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
                    .accessibilityIdentifier("dictionary-import-text")
                if let importMessage {
                    Text(importMessage)
                        .font(.footnote)
                        .foregroundStyle(.orange)
                }
            }
            .padding(StateTheme.Space.section)
            .background(StateTheme.ground.ignoresSafeArea())
            .navigationTitle(String(localized: "Paste a list"))
            .stateInlineNavigationTitle()
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button(String(localized: "Cancel")) { importing = false }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(String(localized: "Add")) { importList() }
                        .disabled(importText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                        .accessibilityIdentifier("dictionary-import-add")
                }
            }
        }
    }

    private func importList() {
        guard let parsed = NotesDictionaryImport.parse(importText) else {
            importMessage = String(localized: "A line has an unknown format. Use word:, always: or context:.")
            return
        }
        var merged = draft
        merged.merge(parsed)
        wordRows = merged.words.map { WordRow(text: $0) }
        correctionRows = merged.corrections.map { CorrectionRow(from: $0.from, to: $0.to, mode: $0.mode) }
        importing = false
    }

    private func apply(_ dictionary: NotesDictionary) {
        saved = dictionary
        revision = dictionary.revision
        wordRows = dictionary.words.map { WordRow(text: $0) }
        correctionRows = dictionary.corrections.map { CorrectionRow(from: $0.from, to: $0.to, mode: $0.mode) }
    }

    private func save() async {
        saving = true
        message = nil
        let outcome = await model.saveNotesDictionary(draft)
        saving = false
        switch outcome {
        case .saved:
            apply(model.notesDictionary ?? draft)
        case .conflict:
            // Keep what the owner typed; the next save replaces the newer
            // version on purpose.
            revision = model.notesDictionary?.revision ?? revision
            message = String(localized: "Another device changed the dictionary. Save again to keep this version.")
        case .invalid:
            message = String(localized: "An entry is too long, contains control characters, or maps a word to itself.")
        case .failed:
            message = String(localized: "The dictionary could not be saved. Check the connection to your server.")
        }
    }
}
