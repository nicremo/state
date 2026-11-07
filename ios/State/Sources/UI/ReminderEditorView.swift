import SwiftUI

struct ReminderEditorView: View {
    @Environment(\.dismiss) private var dismiss
    @Bindable var model: AppModel
    @AppStorage(AgentPreferences.t3OnlyKey) private var t3Only = true
    @State private var draft: ReminderDraft
    @State private var isSaving = false
    @State private var showsPolicyEditor = false
    private let title: LocalizedStringKey
    private let onSave: (ReminderDraft) async -> Void

    init(model: AppModel, reminder: Reminder? = nil, onSave: @escaping (ReminderDraft) async -> Void) {
        self.model = model
        var draft = ReminderDraft()
        if let reminder {
            draft.title = reminder.title
            draft.description = reminder.description ?? ""
            draft.scheduled = reminder.schedule != nil
            draft.includesTime = reminder.schedule?.localTime != nil
            draft.date = reminder.schedule.flatMap(StateDateFormatter.date) ?? Date()
            draft.prewarningMinutes = reminder.schedule?.prewarningMinutes ?? 0
            draft.recurrence = reminder.recurrence?.frequency
            draft.timeZoneMode = reminder.schedule?.mode ?? .floating
            draft.timeZoneIdentifier = reminder.schedule?.timeZone ?? TimeZone.current.identifier
            draft.executionPolicyID = reminder.executionPolicyID
            draft.executionModel = reminder.executionModel
        }
        _draft = State(initialValue: draft)
        title = reminder == nil ? "New reminder" : "Edit reminder"
        self.onSave = onSave
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("Reminder") {
                    TextField("Title", text: $draft.title, axis: .vertical)
                        .stateSentenceAutocapitalization()
                    TextField("Description", text: $draft.description, axis: .vertical)
                        .lineLimit(3...8)
                }

                Section("Schedule") {
                    Toggle("Set a date", isOn: $draft.scheduled.animation())
                    if draft.scheduled {
                        DatePicker(
                            "Date",
                            selection: $draft.date,
                            displayedComponents: draft.includesTime ? [.date, .hourAndMinute] : [.date]
                        )
                        Toggle("Specific time", isOn: $draft.includesTime.animation())
                        if draft.includesTime {
                            Picker("Advance notice", selection: $draft.prewarningMinutes) {
                                Text("At due time").tag(0)
                                Text("10 minutes before").tag(10)
                                Text("1 hour before").tag(60)
                                Text("1 day before").tag(1_440)
                            }
                        }
                        Picker("Time zone behavior", selection: $draft.timeZoneMode) {
                            Text("Moves with me").tag(TimeZoneMode.floating)
                            Text("Fixed location time").tag(TimeZoneMode.fixed)
                        }
                        LabeledContent("Time zone", value: draft.timeZoneIdentifier)
                            .foregroundStyle(.secondary)
                    }
                }

                Section("Repeat") {
                    Picker("Frequency", selection: $draft.recurrence) {
                        Text("Never").tag(nil as RecurrenceFrequency?)
                        Text("Daily").tag(RecurrenceFrequency.daily as RecurrenceFrequency?)
                        Text("Weekly").tag(RecurrenceFrequency.weekly as RecurrenceFrequency?)
                        Text("Monthly").tag(RecurrenceFrequency.monthly as RecurrenceFrequency?)
                        Text("Yearly").tag(RecurrenceFrequency.yearly as RecurrenceFrequency?)
                    }
                }

                Section {
                    AgentProjectPicker(model: model, policies: editorPolicies, policyID: $draft.executionPolicyID, allowsNone: true)
                    if selectedPolicy?.adapter == AgentNames.t3Code {
                        AgentModelPicker(catalog: model.modelCatalog, selection: $draft.executionModel)
                    }
                    Button {
                        showsPolicyEditor = true
                    } label: {
                        Label("New policy…", systemImage: "plus")
                    }
                } header: {
                    Text("Agent")
                } footer: {
                    if model.policies.isEmpty {
                        Text("No policies yet. Create one and a runner can execute this reminder on your Mac when it is due.")
                    } else if selectedPolicy?.adapter == AgentNames.t3Code {
                        Text("When the reminder is due, a T3 Code session starts on your Mac with this model. If the Mac is off, it starts as soon as the Mac is back.")
                    } else {
                        Text("A runner executes this reminder with the policy's capabilities when it is due.")
                    }
                }
                .onChange(of: draft.executionPolicyID) { _, _ in
                    // A T3 Code assignment always carries a model, so a
                    // scheduled session never depends on recent manual use.
                    if selectedPolicy?.adapter == AgentNames.t3Code, draft.executionModel == nil {
                        draft.executionModel = model.modelCatalog.suggested
                    }
                }
            }
            .navigationTitle(title)
            .stateInlineNavigationTitle()
            .stateBackground()
            .interactiveDismissDisabled(isSaving)
            .task {
                // The policy list arrives with the sync; a fresh install opens
                // the editor before the first pull has finished.
                if model.policies.isEmpty {
                    await model.synchronize()
                }
            }
            .sheet(isPresented: $showsPolicyEditor) {
                PolicyEditorView(model: model)
            }
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                        .disabled(isSaving)
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") {
                        isSaving = true
                        Task {
                            await onSave(draft)
                            dismiss()
                        }
                    }
                    .disabled(draft.title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || isSaving)
                }
            }
        }
    }

    /// The policies the agent section offers; the assigned one always stays
    /// in the list, so editing never drops it.
    private var editorPolicies: [ExecutionPolicy] {
        var list = model.agentPolicies(t3Only: t3Only)
        if let assigned = model.policies.first(where: { $0.id == draft.executionPolicyID }), !list.contains(where: { $0.id == assigned.id }) {
            list.append(assigned)
        }
        return list
    }

    private var selectedPolicy: ExecutionPolicy? {
        model.policies.first { $0.id == draft.executionPolicyID }
    }
}
