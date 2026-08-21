import SwiftUI

struct ReminderEditorView: View {
    @Environment(\.dismiss) private var dismiss
    @Bindable var model: AppModel
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
                        .textInputAutocapitalization(.sentences)
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
                    Picker("Policy", selection: $draft.executionPolicyID) {
                        Text("None").tag(nil as String?)
                        ForEach(model.policies) { policy in
                            Text(policy.name).tag(policy.id as String?)
                        }
                    }
                    Button {
                        showsPolicyEditor = true
                    } label: {
                        Label("New policy…", systemImage: "plus")
                    }
                } header: {
                    Text("Agent execution")
                } footer: {
                    if model.policies.isEmpty {
                        Text("No policies yet. Create one and a runner can execute this reminder on your Mac when it is due.")
                    } else {
                        Text("A runner executes this reminder with the policy's capabilities when it is due.")
                    }
                }
            }
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
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
}
