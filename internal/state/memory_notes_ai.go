package state

import (
	"context"
	"sort"
	"time"
)

// memoryNoteAI holds the AI data of MemoryRepository. It lives in its own
// struct so the reminder fields stay readable.
type memoryNoteAI struct {
	states             map[string]*NoteAIState
	requestAttachments map[string]NoteAttachment
	requestActors      map[string]string
	jobs               map[string]NoteJob
	settings           *NoteAISettings
	dictionary         *NotesDictionary
	usage              map[string]float64
}

func (repository *MemoryRepository) noteAI() *memoryNoteAI {
	if repository.ai == nil {
		repository.ai = &memoryNoteAI{
			states:             make(map[string]*NoteAIState),
			requestAttachments: make(map[string]NoteAttachment),
			requestActors:      make(map[string]string),
			jobs:               make(map[string]NoteJob),
			usage:              make(map[string]float64),
		}
	}
	return repository.ai
}

func (repository *MemoryRepository) aiState(noteID string) *NoteAIState {
	store := repository.noteAI()
	if state, ok := store.states[noteID]; ok {
		return state
	}
	state := &NoteAIState{}
	store.states[noteID] = state
	return state
}

func (repository *MemoryRepository) GetNoteAIState(_ context.Context, noteID string) (NoteAIState, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if _, ok := repository.notes[noteID]; !ok {
		return NoteAIState{}, ErrNotFound
	}
	current := repository.aiState(noteID)
	copied := NoteAIState{
		Attachments: append([]NoteAttachment(nil), current.Attachments...),
		Processing:  current.Processing,
		Proposals:   append([]ReminderProposal(nil), current.Proposals...),
	}
	if current.Result != nil {
		result := *current.Result
		copied.Result = &result
	}
	// Relations are stored once and shown on both notes.
	for _, state := range repository.noteAI().states {
		for _, relation := range state.Relations {
			if relation.NoteID == noteID || relation.RelatedNoteID == noteID {
				copied.Relations = append(copied.Relations, relation)
			}
		}
	}
	sort.Slice(copied.Attachments, func(left, right int) bool {
		return copied.Attachments[left].Ordinal < copied.Attachments[right].Ordinal
	})
	return copied, nil
}

func (repository *MemoryRepository) AddNoteAttachment(_ context.Context, attachment NoteAttachment, event AuditEvent, clientRequestID string) (NoteAttachment, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	store := repository.noteAI()
	if existing, ok := store.requestAttachments[clientRequestID]; ok {
		return existing, nil
	}
	if _, ok := repository.notes[attachment.NoteID]; !ok {
		return NoteAttachment{}, ErrNotFound
	}
	repository.appendAuditEvent(event)
	state := repository.aiState(attachment.NoteID)
	state.Attachments = append(state.Attachments, attachment)
	store.requestAttachments[clientRequestID] = attachment
	store.requestActors[clientRequestID] = event.Actor.ID
	return attachment, nil
}

func (repository *MemoryRepository) LookupNoteAttachmentRequest(_ context.Context, clientRequestID string, actorID string) (NoteAttachment, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	store := repository.noteAI()
	attachment, ok := store.requestAttachments[clientRequestID]
	if !ok {
		return NoteAttachment{}, false, nil
	}
	if store.requestActors[clientRequestID] != actorID {
		return NoteAttachment{}, false, ErrForbidden
	}
	return attachment, true, nil
}

func (repository *MemoryRepository) SaveNoteAIChange(_ context.Context, noteID string, change NoteAIChange, event AuditEvent) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if _, ok := repository.notes[noteID]; !ok {
		return ErrNotFound
	}
	state := repository.aiState(noteID)
	if change.Processing != nil {
		state.Processing = *change.Processing
	}
	if change.Result != nil {
		result := *change.Result
		state.Result = &result
	}
	for index, attachment := range state.Attachments {
		if text, ok := change.AttachmentTexts[attachment.ID]; ok {
			state.Attachments[index].DerivedText = text.Text
			state.Attachments[index].DerivedKind = text.Kind
			state.Attachments[index].DerivedModel = text.Model
		}
	}
	state.Relations = append(state.Relations, change.AddRelations...)
	for _, relationID := range change.ArchiveRelationIDs {
		for _, other := range repository.noteAI().states {
			for index := range other.Relations {
				if other.Relations[index].ID == relationID {
					other.Relations[index].Archived = true
				}
			}
		}
	}
	state.Proposals = append(state.Proposals, change.AddProposals...)
	for _, updated := range change.UpdateProposals {
		for index := range state.Proposals {
			if state.Proposals[index].ID == updated.ID {
				state.Proposals[index] = updated
			}
		}
	}
	repository.appendAuditEvent(event)
	return nil
}

// EnqueueNoteJob keeps one waiting job per note. A processing request
// replaces a waiting organize pass; another organize pass only moves the
// waiting one later, which debounces typing.
func (repository *MemoryRepository) EnqueueNoteJob(_ context.Context, job NoteJob) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	store := repository.noteAI()
	for id, waiting := range store.jobs {
		if waiting.NoteID != job.NoteID || waiting.Status != NoteJobQueued {
			continue
		}
		if waiting.Kind == NoteJobProcess && job.Kind == NoteJobOrganize {
			return nil
		}
		waiting.Kind = job.Kind
		waiting.NotBefore = job.NotBefore
		store.jobs[id] = waiting
		return nil
	}
	store.jobs[job.ID] = job
	return nil
}

func (repository *MemoryRepository) ClaimNoteJob(_ context.Context, now time.Time) (NoteJob, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	var chosen *NoteJob
	for _, job := range repository.noteAI().jobs {
		if job.Status != NoteJobQueued || job.NotBefore.After(now) {
			continue
		}
		if chosen == nil || job.NotBefore.Before(chosen.NotBefore) || (job.NotBefore.Equal(chosen.NotBefore) && job.ID < chosen.ID) {
			candidate := job
			chosen = &candidate
		}
	}
	if chosen == nil {
		return NoteJob{}, false, nil
	}
	chosen.Status = NoteJobRunning
	repository.noteAI().jobs[chosen.ID] = *chosen
	return *chosen, true, nil
}

func (repository *MemoryRepository) FinishNoteJob(_ context.Context, job NoteJob) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if _, ok := repository.noteAI().jobs[job.ID]; !ok {
		return ErrNotFound
	}
	repository.noteAI().jobs[job.ID] = job
	return nil
}

func (repository *MemoryRepository) RequeueRunningNoteJobs(_ context.Context) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	for id, job := range repository.noteAI().jobs {
		if job.Status == NoteJobRunning {
			job.Status = NoteJobQueued
			repository.noteAI().jobs[id] = job
		}
	}
	return nil
}

func (repository *MemoryRepository) GetNoteAISettings(_ context.Context) (NoteAISettings, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if repository.noteAI().settings == nil {
		return NoteAISettings{}, false, nil
	}
	return *repository.noteAI().settings, true, nil
}

func (repository *MemoryRepository) SaveNoteAISettings(_ context.Context, settings NoteAISettings, event AuditEvent) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	repository.appendAuditEvent(event)
	repository.noteAI().settings = &settings
	return nil
}

func (repository *MemoryRepository) AddNoteAIUsage(_ context.Context, month string, costUSD float64) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	repository.noteAI().usage[month] += costUSD
	return nil
}

func (repository *MemoryRepository) NoteAIUsage(_ context.Context, month string) (float64, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	return repository.noteAI().usage[month], nil
}

// noteSearchText is what the search index holds for a note: its own text
// plus the AI's title, summary, OCR and transcripts.
func (repository *MemoryRepository) noteSearchText(note Note) Note {
	state, ok := repository.noteAI().states[note.ID]
	if !ok {
		return note
	}
	if state.Result != nil {
		note.Summary += "\n" + state.Result.Title + "\n" + state.Result.Summary
	}
	for _, attachment := range state.Attachments {
		note.PlainText += "\n" + attachment.DerivedText
	}
	return note
}

func (repository *MemoryRepository) GetNotesDictionary(_ context.Context) (NotesDictionary, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	stored := repository.noteAI().dictionary
	if stored == nil {
		return NotesDictionary{}, false, nil
	}
	dictionary := *stored
	dictionary.Words = append([]string(nil), stored.Words...)
	dictionary.Corrections = append([]DictionaryCorrection(nil), stored.Corrections...)
	return dictionary, true, nil
}

func (repository *MemoryRepository) SaveNotesDictionary(_ context.Context, dictionary NotesDictionary, event AuditEvent) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	var revision int64
	if stored := repository.noteAI().dictionary; stored != nil {
		revision = stored.Revision
	}
	if revision != dictionary.Revision-1 {
		return ErrRevisionConflict
	}
	repository.appendAuditEvent(event)
	repository.noteAI().dictionary = &dictionary
	return nil
}
