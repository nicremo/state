package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Notes are managed by a server-side AI: photos and speech become notes, and
// every note gets an AI title, summary, related notes and reminder proposals.
// Everything the AI produces lives next to the note, outside its revision, so
// the AI can never cause an edit conflict with a person typing in the app.
// The one exception is filling the empty document of a photo or voice note,
// which is a normal revisioned update and only happens while the note is
// still exactly as the job found it.

type NoteCapture string

const (
	NoteCaptureText  NoteCapture = "text"
	NoteCaptureImage NoteCapture = "image"
	NoteCaptureAudio NoteCapture = "audio"
)

const NoteFieldSourceAI NoteFieldSource = "ai"

const (
	NoteAttachmentImage = "image"
	NoteAttachmentAudio = "audio"
)

const (
	NoteProcessingIdle            = "idle"
	NoteProcessingQueued          = "queued"
	NoteProcessingRunning         = "processing"
	NoteProcessingReady           = "ready"
	NoteProcessingNeedsReview     = "needs_review"
	NoteProcessingFailed          = "failed"
	NoteProcessingConsentRequired = "consent_required"
	NoteProcessingNotConfigured   = "not_configured"
	NoteProcessingBudgetExhausted = "budget_exhausted"
)

const (
	NoteJobProcess  = "process"
	NoteJobOrganize = "organize"

	NoteJobQueued  = "queued"
	NoteJobRunning = "running"
	NoteJobDone    = "done"
	NoteJobFailed  = "failed"
)

const (
	ReminderProposalPending   = "pending"
	ReminderProposalAccepted  = "accepted"
	ReminderProposalDismissed = "dismissed"
)

const (
	DefaultNotesAgentModel     = "deepseek/deepseek-v4.1-flash"
	DefaultTranscriptionModel  = "openai/whisper-large-v3-turbo"
	DefaultMonthlyLimitUSD     = 10.0
	maxNoteRelationsPerJob     = 5
	maxReminderProposalsPerJob = 3
	maxAttachmentTextBytes     = 262144
	maxRelationReasonRunes     = 280
	organizeQuietPeriod        = 45 * time.Second
)

const (
	AuditActionNoteAttachmentAdded    AuditAction = "note.attachment_added"
	AuditActionNoteProcessing         AuditAction = "note.processing"
	AuditActionNoteAIUpdated          AuditAction = "note.ai_updated"
	AuditActionNoteRelationDismissed  AuditAction = "note.relation_dismissed"
	AuditActionNoteProposalAccepted   AuditAction = "note.proposal_accepted"
	AuditActionNoteProposalDismissed  AuditAction = "note.proposal_dismissed"
	AuditActionNotesAISettingsUpdated AuditAction = "notes_ai.settings_updated"
)

// NotesAgentActor is recorded for everything the notes AI writes.
func NotesAgentActor() Actor {
	return Actor{ID: "notes-agent", Kind: ActorKindSystem, DisplayName: "Notes AI"}
}

type NoteAttachment struct {
	ID           string    `json:"id"`
	NoteID       string    `json:"note_id"`
	Ordinal      int       `json:"ordinal"`
	Kind         string    `json:"kind"`
	MimeType     string    `json:"mime_type"`
	ByteSize     int64     `json:"byte_size"`
	SHA256       string    `json:"sha256"`
	DurationMS   int64     `json:"duration_ms,omitempty"`
	DerivedText  string    `json:"derived_text,omitempty"`
	DerivedKind  string    `json:"derived_kind,omitempty"`
	DerivedModel string    `json:"derived_model,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type NoteProcessing struct {
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	Model     string    `json:"model,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NoteAIResult is the AI's view of a note: title and summary with their
// model, bound to the plain text they were made from.
type NoteAIResult struct {
	Title            string    `json:"title,omitempty"`
	Summary          string    `json:"summary,omitempty"`
	Model            string    `json:"model"`
	SourceHash       string    `json:"source_hash"`
	ProposedDocument string    `json:"proposed_document,omitempty"`
	NeedsReview      bool      `json:"needs_review,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type NoteRelation struct {
	ID            string    `json:"id"`
	NoteID        string    `json:"note_id"`
	RelatedNoteID string    `json:"related_note_id"`
	RelatedTitle  string    `json:"related_title,omitempty"`
	Reason        string    `json:"reason"`
	Confidence    float64   `json:"confidence,omitempty"`
	Archived      bool      `json:"archived,omitempty"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
}

type ReminderProposal struct {
	ID          string    `json:"id"`
	NoteID      string    `json:"note_id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	LocalDate   string    `json:"local_date,omitempty"`
	LocalTime   string    `json:"local_time,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	Status      string    `json:"status"`
	ReminderID  string    `json:"reminder_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// NoteAIState is everything stored next to one note.
type NoteAIState struct {
	Attachments []NoteAttachment   `json:"attachments"`
	Processing  NoteProcessing     `json:"processing"`
	Result      *NoteAIResult      `json:"result,omitempty"`
	Relations   []NoteRelation     `json:"relations"`
	Proposals   []ReminderProposal `json:"reminder_proposals"`
}

// NoteView is a note as clients see it: the note with the AI's title and
// summary where no person wrote one, and everything stored next to it.
type NoteView struct {
	Note
	Attachments []NoteAttachment   `json:"attachments"`
	Processing  NoteProcessing     `json:"processing"`
	AI          *NoteAIResult      `json:"ai,omitempty"`
	Relations   []NoteRelation     `json:"relations"`
	Proposals   []ReminderProposal `json:"reminder_proposals"`
}

type NoteAttachmentText struct {
	Text  string `json:"text"`
	Kind  string `json:"kind"`
	Model string `json:"model"`
}

// NoteAIChange is one atomic write of AI data, stored with one audit event.
type NoteAIChange struct {
	Processing         *NoteProcessing
	Result             *NoteAIResult
	AttachmentTexts    map[string]NoteAttachmentText
	AddRelations       []NoteRelation
	ArchiveRelationIDs []string
	AddProposals       []ReminderProposal
	UpdateProposals    []ReminderProposal
}

type NoteJob struct {
	ID        string    `json:"id"`
	NoteID    string    `json:"note_id"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	Attempts  int       `json:"attempts"`
	NotBefore time.Time `json:"not_before"`
	CreatedAt time.Time `json:"created_at"`
}

type NoteAISettings struct {
	Consent            bool       `json:"consent"`
	ConsentAt          *time.Time `json:"consent_at,omitempty"`
	MonthlyLimitUSD    float64    `json:"monthly_limit_usd"`
	AgentModel         string     `json:"agent_model"`
	TranscriptionModel string     `json:"transcription_model"`
	UpdatedAt          time.Time  `json:"updated_at"`
	// Filled on read, never stored.
	Month             string  `json:"month"`
	SpentThisMonthUSD float64 `json:"spent_this_month_usd"`
}

type UpdateNoteAISettingsInput struct {
	Consent            *bool    `json:"consent,omitempty"`
	MonthlyLimitUSD    *float64 `json:"monthly_limit_usd,omitempty"`
	AgentModel         *string  `json:"agent_model,omitempty"`
	TranscriptionModel *string  `json:"transcription_model,omitempty"`
}

// NoteMediaPolicy bounds uploads. The server computes it from the model's
// capabilities and its own limits; the app only ever reads it.
type NoteMediaPolicy struct {
	MaxImages          int   `json:"max_images"`
	MaxImageBytes      int64 `json:"max_image_bytes"`
	MaxTotalImageBytes int64 `json:"max_total_image_bytes"`
	MaxAudioBytes      int64 `json:"max_audio_bytes"`
	MaxAudioSegments   int   `json:"max_audio_segments"`
	AudioSegmentSecs   int   `json:"audio_segment_seconds"`
}

func DefaultNoteMediaPolicy() NoteMediaPolicy {
	return NoteMediaPolicy{
		MaxImages:          10,
		MaxImageBytes:      8 << 20,
		MaxTotalImageBytes: 40 << 20,
		MaxAudioBytes:      20 << 20,
		MaxAudioSegments:   12,
		AudioSegmentSecs:   300,
	}
}

type AddNoteAttachmentInput struct {
	Kind            string `json:"kind"`
	MimeType        string `json:"mime_type"`
	ByteSize        int64  `json:"byte_size"`
	SHA256          string `json:"sha256"`
	DurationMS      int64  `json:"duration_ms,omitempty"`
	Ordinal         int    `json:"ordinal"`
	Source          string `json:"source,omitempty"`
	ClientRequestID string `json:"client_request_id"`
}

type AcceptReminderProposalInput struct {
	TimeZone        string `json:"time_zone"`
	ClientRequestID string `json:"client_request_id"`
}

// NoteAgentOutcome is what one AI job produced.
type NoteAgentOutcome struct {
	Title           string
	Summary         string
	Document        string
	NeedsReview     bool
	Model           string
	AttachmentTexts map[string]NoteAttachmentText
	Relations       []NoteRelation
	Proposals       []ReminderProposal
}

// NoteAIRepository stores everything the AI keeps next to notes.
type NoteAIRepository interface {
	GetNoteAIState(ctx context.Context, noteID string) (NoteAIState, error)
	AddNoteAttachment(ctx context.Context, attachment NoteAttachment, event AuditEvent, clientRequestID string) (NoteAttachment, error)
	LookupNoteAttachmentRequest(ctx context.Context, clientRequestID string, actorID string) (NoteAttachment, bool, error)
	SaveNoteAIChange(ctx context.Context, noteID string, change NoteAIChange, event AuditEvent) error
	EnqueueNoteJob(ctx context.Context, job NoteJob) error
	ClaimNoteJob(ctx context.Context, now time.Time) (NoteJob, bool, error)
	FinishNoteJob(ctx context.Context, job NoteJob) error
	RequeueRunningNoteJobs(ctx context.Context) error
	GetNoteAISettings(ctx context.Context) (NoteAISettings, bool, error)
	SaveNoteAISettings(ctx context.Context, settings NoteAISettings, event AuditEvent) error
	AddNoteAIUsage(ctx context.Context, month string, costUSD float64) error
	NoteAIUsage(ctx context.Context, month string) (float64, error)
}

// WithNoteAI tells the service whether the OpenRouter key is configured and
// which media limits the current models allow.
func WithNoteAI(available func() bool, policy func() NoteMediaPolicy) ServiceOption {
	return func(service *Service) {
		service.noteAIAvailable = available
		service.noteMediaPolicy = policy
	}
}

func (service *Service) aiAvailable() bool {
	return service.noteAIAvailable != nil && service.noteAIAvailable()
}

// NoteMediaPolicy is the upload policy in force right now.
func (service *Service) NoteMediaPolicy() NoteMediaPolicy {
	return service.mediaPolicy()
}

// NoteAIAvailable reports whether the server has an OpenRouter key.
func (service *Service) NoteAIAvailable() bool {
	return service.aiAvailable()
}

func (service *Service) mediaPolicy() NoteMediaPolicy {
	if service.noteMediaPolicy == nil {
		return DefaultNoteMediaPolicy()
	}
	return service.noteMediaPolicy()
}

func validCapture(capture NoteCapture) bool {
	return capture == "" || capture == NoteCaptureText || capture == NoteCaptureImage || capture == NoteCaptureAudio
}

// isMediaCapture reports a photo or voice note, which may start without text.
func isMediaCapture(capture NoteCapture) bool {
	return capture == NoteCaptureImage || capture == NoteCaptureAudio
}

// GetNoteView returns a note with its AI data. A title or summary a person
// wrote always wins; the AI's only replaces a derived one.
func (service *Service) GetNoteView(ctx context.Context, noteID string) (NoteView, error) {
	note, err := service.GetNote(ctx, noteID)
	if err != nil {
		return NoteView{}, err
	}
	return service.noteView(ctx, note)
}

func (service *Service) ListNoteViews(ctx context.Context, options NoteListOptions) ([]NoteView, error) {
	notes, err := service.ListNotes(ctx, options)
	if err != nil {
		return nil, err
	}
	views := make([]NoteView, 0, len(notes))
	for _, note := range notes {
		view, err := service.noteView(ctx, note)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

// NoteViewOf wraps a note that a mutation just returned.
func (service *Service) NoteViewOf(ctx context.Context, note Note) (NoteView, error) {
	return service.noteView(ctx, note)
}

func (service *Service) noteView(ctx context.Context, note Note) (NoteView, error) {
	aiState, err := service.repository.GetNoteAIState(ctx, note.ID)
	if err != nil {
		return NoteView{}, err
	}
	view := NoteView{
		Note:        note,
		Attachments: aiState.Attachments,
		Processing:  aiState.Processing,
		AI:          aiState.Result,
		Relations:   make([]NoteRelation, 0, len(aiState.Relations)),
		Proposals:   aiState.Proposals,
	}
	if view.Attachments == nil {
		view.Attachments = []NoteAttachment{}
	}
	if view.Proposals == nil {
		view.Proposals = []ReminderProposal{}
	}
	if view.Processing.Status == "" {
		view.Processing.Status = NoteProcessingIdle
	}
	if result := aiState.Result; result != nil {
		if note.TitleSource == NoteFieldSourceDerived && result.Title != "" {
			view.Title = result.Title
			view.TitleSource = NoteFieldSourceAI
		}
		if note.SummarySource == NoteFieldSourceDerived && result.Summary != "" {
			view.Summary = result.Summary
			view.SummarySource = NoteFieldSourceAI
		}
	}
	for _, relation := range aiState.Relations {
		if relation.Archived {
			continue
		}
		// A relation shows on both notes, always naming the other one.
		if relation.NoteID != note.ID {
			relation.NoteID, relation.RelatedNoteID = note.ID, relation.NoteID
		}
		related, err := service.repository.GetNote(ctx, relation.RelatedNoteID)
		if err != nil || related.Archived {
			continue
		}
		relation.RelatedTitle = related.Title
		view.Relations = append(view.Relations, relation)
	}
	return view, nil
}

func (service *Service) AddNoteAttachment(ctx context.Context, actor Actor, noteID string, input AddNoteAttachmentInput) (NoteAttachment, error) {
	if actor.ID == "" || actor.Kind == "" || noteID == "" || input.ClientRequestID == "" {
		return NoteAttachment{}, ErrInvalidInput
	}
	if actor.Kind == ActorKindRunner {
		return NoteAttachment{}, ErrForbidden
	}
	if stored, found, err := service.repository.LookupNoteAttachmentRequest(ctx, input.ClientRequestID, actor.ID); err != nil {
		return NoteAttachment{}, err
	} else if found {
		if stored.NoteID != noteID {
			return NoteAttachment{}, ErrInvalidInput
		}
		return stored, nil
	}
	note, err := service.repository.GetNote(ctx, noteID)
	if err != nil {
		return NoteAttachment{}, err
	}
	if note.Archived || input.Ordinal < 0 || input.ByteSize <= 0 || !validSHA256(input.SHA256) || input.DurationMS < 0 {
		return NoteAttachment{}, ErrInvalidInput
	}
	aiState, err := service.repository.GetNoteAIState(ctx, noteID)
	if err != nil {
		return NoteAttachment{}, err
	}
	if err := checkAttachmentLimits(service.mediaPolicy(), aiState.Attachments, input); err != nil {
		return NoteAttachment{}, err
	}
	attachmentID, err := service.newID()
	if err != nil {
		return NoteAttachment{}, fmt.Errorf("generate attachment ID: %w", err)
	}
	eventID, err := service.newID()
	if err != nil {
		return NoteAttachment{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	now := service.clock().UTC()
	attachment := NoteAttachment{
		ID:         attachmentID,
		NoteID:     noteID,
		Ordinal:    input.Ordinal,
		Kind:       input.Kind,
		MimeType:   input.MimeType,
		ByteSize:   input.ByteSize,
		SHA256:     input.SHA256,
		DurationMS: input.DurationMS,
		CreatedAt:  now,
	}
	event, err := service.buildAuditEvent(eventID, "", AuditActionNoteAttachmentAdded, actor, now, nil, defaultSource(input.Source), "", nil, attachmentSnapshot(attachment), []string{"attachments"}, note.Revision, "", input.ClientRequestID)
	if err != nil {
		return NoteAttachment{}, err
	}
	event.NoteID = noteID
	return service.repository.AddNoteAttachment(ctx, attachment, event, input.ClientRequestID)
}

func checkAttachmentLimits(policy NoteMediaPolicy, existing []NoteAttachment, input AddNoteAttachmentInput) error {
	count, total := 0, int64(0)
	for _, attachment := range existing {
		if attachment.Kind == input.Kind {
			count++
			total += attachment.ByteSize
		}
	}
	switch input.Kind {
	case NoteAttachmentImage:
		if !strings.HasPrefix(input.MimeType, "image/") || policy.MaxImages <= 0 || count >= policy.MaxImages ||
			input.ByteSize > policy.MaxImageBytes || total+input.ByteSize > policy.MaxTotalImageBytes {
			return ErrInvalidInput
		}
	case NoteAttachmentAudio:
		if !strings.HasPrefix(input.MimeType, "audio/") || count >= policy.MaxAudioSegments || input.ByteSize > policy.MaxAudioBytes {
			return ErrInvalidInput
		}
	default:
		return ErrInvalidInput
	}
	return nil
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validSHA256(value string) bool {
	return sha256Pattern.MatchString(value)
}

// attachmentSnapshot keeps metadata only: no bytes, no storage path.
func attachmentSnapshot(attachment NoteAttachment) map[string]any {
	return map[string]any{
		"id":          attachment.ID,
		"kind":        attachment.Kind,
		"mime_type":   attachment.MimeType,
		"byte_size":   attachment.ByteSize,
		"sha256":      attachment.SHA256,
		"ordinal":     attachment.Ordinal,
		"duration_ms": attachment.DurationMS,
	}
}

func defaultSource(source string) string {
	if source == "" {
		return "rest"
	}
	return source
}

// RequestNoteProcessing asks the AI to process a note once its media is
// uploaded. Without a key or without the owner's consent nothing is sent;
// the note says why.
func (service *Service) RequestNoteProcessing(ctx context.Context, actor Actor, noteID string, clientRequestID string) (NoteProcessing, error) {
	if actor.ID == "" || actor.Kind == "" || noteID == "" || clientRequestID == "" {
		return NoteProcessing{}, ErrInvalidInput
	}
	if actor.Kind == ActorKindRunner {
		return NoteProcessing{}, ErrForbidden
	}
	note, err := service.repository.GetNote(ctx, noteID)
	if err != nil {
		return NoteProcessing{}, err
	}
	aiState, err := service.repository.GetNoteAIState(ctx, noteID)
	if err != nil {
		return NoteProcessing{}, err
	}
	if aiState.Processing.RequestID == clientRequestID {
		return aiState.Processing, nil
	}
	settings, err := service.GetNoteAISettings(ctx)
	if err != nil {
		return NoteProcessing{}, err
	}
	now := service.clock().UTC()
	processing := NoteProcessing{Status: NoteProcessingQueued, RequestID: clientRequestID, UpdatedAt: now}
	switch {
	case !service.aiAvailable():
		processing.Status = NoteProcessingNotConfigured
	case !settings.Consent:
		processing.Status = NoteProcessingConsentRequired
	case note.Archived:
		return NoteProcessing{}, ErrInvalidInput
	}
	if err := service.saveAIChange(ctx, actor, note, AuditActionNoteProcessing, NoteAIChange{Processing: &processing}, map[string]any{"status": processing.Status}, clientRequestID); err != nil {
		return NoteProcessing{}, err
	}
	if processing.Status == NoteProcessingQueued {
		if err := service.enqueueNoteJob(ctx, noteID, NoteJobProcess, now); err != nil {
			return NoteProcessing{}, err
		}
	}
	return processing, nil
}

func (service *Service) enqueueNoteJob(ctx context.Context, noteID string, kind string, notBefore time.Time) error {
	jobID, err := service.newID()
	if err != nil {
		return fmt.Errorf("generate job ID: %w", err)
	}
	return service.repository.EnqueueNoteJob(ctx, NoteJob{
		ID:        jobID,
		NoteID:    noteID,
		Kind:      kind,
		Status:    NoteJobQueued,
		NotBefore: notBefore,
		CreatedAt: service.clock().UTC(),
	})
}

// scheduleOrganize queues an AI pass after a text change, once typing has
// paused. It is best effort: the note itself is already stored.
func (service *Service) scheduleOrganize(ctx context.Context, before Note, after Note) {
	if isMediaCapture(after.Capture) && strings.TrimSpace(after.PlainText) == "" {
		return
	}
	if before.PlainText == after.PlainText || strings.TrimSpace(after.PlainText) == "" || !service.aiAvailable() {
		return
	}
	settings, err := service.GetNoteAISettings(ctx)
	if err != nil || !settings.Consent {
		return
	}
	_ = service.enqueueNoteJob(ctx, after.ID, NoteJobOrganize, service.clock().UTC().Add(organizeQuietPeriod))
}

func (service *Service) ClaimNoteJob(ctx context.Context) (NoteJob, bool, error) {
	return service.repository.ClaimNoteJob(ctx, service.clock().UTC())
}

// FinishNoteJob stores a job's end, or its next attempt when retryAt is set.
func (service *Service) FinishNoteJob(ctx context.Context, job NoteJob, status string, retryAt *time.Time) error {
	job.Status = status
	if retryAt != nil {
		job.Status = NoteJobQueued
		job.Attempts++
		job.NotBefore = *retryAt
	}
	return service.repository.FinishNoteJob(ctx, job)
}

func (service *Service) RequeueRunningNoteJobs(ctx context.Context) error {
	return service.repository.RequeueRunningNoteJobs(ctx)
}

// SetNoteProcessing records a processing step the worker reached.
func (service *Service) SetNoteProcessing(ctx context.Context, noteID string, processing NoteProcessing) error {
	note, err := service.repository.GetNote(ctx, noteID)
	if err != nil {
		return err
	}
	aiState, err := service.repository.GetNoteAIState(ctx, noteID)
	if err != nil {
		return err
	}
	processing.RequestID = aiState.Processing.RequestID
	processing.UpdatedAt = service.clock().UTC()
	processing.Error = truncateRunes(singleLine(processing.Error), 280)
	return service.saveAIChange(ctx, NotesAgentActor(), note, AuditActionNoteProcessing, NoteAIChange{Processing: &processing}, map[string]any{"status": processing.Status, "error": processing.Error, "model": processing.Model}, "")
}

// RecordAttachmentTexts stores OCR or transcripts as soon as they exist, so a
// later failure of the agent does not lose a paid transcription.
func (service *Service) RecordAttachmentTexts(ctx context.Context, noteID string, texts map[string]NoteAttachmentText) error {
	if len(texts) == 0 {
		return nil
	}
	note, err := service.repository.GetNote(ctx, noteID)
	if err != nil {
		return err
	}
	cleaned := cleanAttachmentTexts(texts)
	return service.saveAIChange(ctx, NotesAgentActor(), note, AuditActionNoteAIUpdated, NoteAIChange{AttachmentTexts: cleaned}, map[string]any{"attachment_texts": attachmentTextDigest(cleaned)}, "")
}

func cleanAttachmentTexts(texts map[string]NoteAttachmentText) map[string]NoteAttachmentText {
	cleaned := make(map[string]NoteAttachmentText, len(texts))
	for id, text := range texts {
		value := strings.ToValidUTF8(text.Text, "")
		if len(value) > maxAttachmentTextBytes {
			value = strings.ToValidUTF8(value[:maxAttachmentTextBytes], "")
		}
		cleaned[id] = NoteAttachmentText{Text: strings.TrimSpace(value), Kind: text.Kind, Model: text.Model}
	}
	return cleaned
}

// attachmentTextDigest keeps the audit event small: the texts themselves are
// derived data and can be regenerated from the stored media.
func attachmentTextDigest(texts map[string]NoteAttachmentText) map[string]any {
	digest := make(map[string]any, len(texts))
	for id, text := range texts {
		sum := sha256.Sum256([]byte(text.Text))
		digest[id] = map[string]any{"kind": text.Kind, "model": text.Model, "sha256": hex.EncodeToString(sum[:]), "bytes": len(text.Text)}
	}
	return digest
}

// ApplyNoteAgentOutcome stores what a job produced. startRevision is the note
// revision the job read; the empty document of a photo or voice note is only
// filled if nobody changed the note since.
func (service *Service) ApplyNoteAgentOutcome(ctx context.Context, job NoteJob, startRevision int64, outcome NoteAgentOutcome) error {
	agent := NotesAgentActor()
	note, err := service.repository.GetNote(ctx, job.NoteID)
	if err != nil {
		return err
	}
	if len(outcome.AttachmentTexts) > 0 {
		if err := service.RecordAttachmentTexts(ctx, note.ID, outcome.AttachmentTexts); err != nil {
			return err
		}
	}
	document := strings.TrimSpace(strings.ToValidUTF8(outcome.Document, ""))
	proposedDocument := ""
	if isMediaCapture(note.Capture) && document != "" && len(document) <= MaxNoteDocumentBytes {
		if strings.TrimSpace(note.Document) == "" && note.Revision == startRevision {
			updated, err := service.UpdateNote(ctx, agent, note.ID, UpdateNoteInput{
				Document:         &document,
				ExpectedRevision: note.Revision,
				Source:           "notes-ai",
				ClientRequestID:  "notes-agent-" + job.ID + "-document",
			})
			switch {
			case err == nil:
				note = updated
			case err == ErrRevisionConflict:
				proposedDocument = document
			default:
				return err
			}
		} else {
			proposedDocument = document
		}
	}

	aiState, err := service.repository.GetNoteAIState(ctx, note.ID)
	if err != nil {
		return err
	}
	now := service.clock().UTC()
	result := NoteAIResult{
		Title:            truncateRunes(singleLine(outcome.Title), MaxNoteTitleRunes),
		Summary:          truncateRunes(singleLine(outcome.Summary), MaxNoteSummaryRunes),
		Model:            singleLine(outcome.Model),
		SourceHash:       plainTextHash(note.PlainText),
		ProposedDocument: proposedDocument,
		NeedsReview:      outcome.NeedsReview,
		UpdatedAt:        now,
	}
	relations, err := service.acceptedRelations(ctx, note, aiState.Relations, outcome.Relations, now)
	if err != nil {
		return err
	}
	proposals, err := service.acceptedProposals(note, outcome.Proposals, now)
	if err != nil {
		return err
	}
	status := NoteProcessingReady
	if outcome.NeedsReview {
		status = NoteProcessingNeedsReview
	}
	processing := NoteProcessing{Status: status, Model: result.Model, RequestID: aiState.Processing.RequestID, UpdatedAt: now}
	change := NoteAIChange{Processing: &processing, Result: &result, AddRelations: relations, AddProposals: proposals}
	after := map[string]any{
		"title":       result.Title,
		"summary":     result.Summary,
		"model":       result.Model,
		"source_hash": result.SourceHash,
		"status":      status,
		"relations":   len(relations),
		"proposals":   len(proposals),
	}
	return service.saveAIChange(ctx, agent, note, AuditActionNoteAIUpdated, change, after, "")
}

func plainTextHash(plainText string) string {
	sum := sha256.Sum256([]byte(plainText))
	return hex.EncodeToString(sum[:])
}

// acceptedRelations keeps the agent's links that point at a real, active,
// different note not linked yet, at most a handful per job.
func (service *Service) acceptedRelations(ctx context.Context, note Note, existing []NoteRelation, proposed []NoteRelation, now time.Time) ([]NoteRelation, error) {
	linked := map[string]bool{note.ID: true}
	for _, relation := range existing {
		if !relation.Archived {
			linked[relation.NoteID] = true
			linked[relation.RelatedNoteID] = true
		}
	}
	accepted := make([]NoteRelation, 0)
	for _, relation := range proposed {
		if len(accepted) >= maxNoteRelationsPerJob || linked[relation.RelatedNoteID] || relation.RelatedNoteID == "" {
			continue
		}
		related, err := service.repository.GetNote(ctx, relation.RelatedNoteID)
		if err != nil || related.Archived {
			continue
		}
		id, err := service.newID()
		if err != nil {
			return nil, fmt.Errorf("generate relation ID: %w", err)
		}
		linked[relation.RelatedNoteID] = true
		accepted = append(accepted, NoteRelation{
			ID:            id,
			NoteID:        note.ID,
			RelatedNoteID: relation.RelatedNoteID,
			Reason:        truncateRunes(singleLine(relation.Reason), maxRelationReasonRunes),
			Confidence:    math.Max(0, math.Min(1, relation.Confidence)),
			CreatedBy:     NotesAgentActor().ID,
			CreatedAt:     now,
		})
	}
	return accepted, nil
}

var (
	localDatePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	localTimePattern = regexp.MustCompile(`^[0-9]{2}:[0-9]{2}$`)
)

// acceptedProposals keeps well-formed reminder proposals. They are only
// proposals: a reminder exists once the owner confirms one in the app.
func (service *Service) acceptedProposals(note Note, proposed []ReminderProposal, now time.Time) ([]ReminderProposal, error) {
	accepted := make([]ReminderProposal, 0)
	for _, proposal := range proposed {
		title := truncateRunes(singleLine(proposal.Title), MaxNoteTitleRunes)
		if len(accepted) >= maxReminderProposalsPerJob || title == "" {
			continue
		}
		if proposal.LocalDate != "" {
			if _, err := time.Parse("2006-01-02", proposal.LocalDate); err != nil || !localDatePattern.MatchString(proposal.LocalDate) {
				continue
			}
		}
		if proposal.LocalTime != "" {
			if _, err := time.Parse("15:04", proposal.LocalTime); err != nil || !localTimePattern.MatchString(proposal.LocalTime) || proposal.LocalDate == "" {
				continue
			}
		}
		id, err := service.newID()
		if err != nil {
			return nil, fmt.Errorf("generate proposal ID: %w", err)
		}
		accepted = append(accepted, ReminderProposal{
			ID:          id,
			NoteID:      note.ID,
			Title:       title,
			Description: truncateRunes(strings.TrimSpace(strings.ToValidUTF8(proposal.Description, "")), 2000),
			LocalDate:   proposal.LocalDate,
			LocalTime:   proposal.LocalTime,
			Reason:      truncateRunes(singleLine(proposal.Reason), maxRelationReasonRunes),
			Status:      ReminderProposalPending,
			CreatedAt:   now,
			UpdatedAt:   now,
		})
	}
	return accepted, nil
}

// AcceptReminderProposal turns a proposal into a reminder. Only the owner and
// the owner's devices confirm; an agent inference is never consent.
func (service *Service) AcceptReminderProposal(ctx context.Context, actor Actor, noteID string, proposalID string, input AcceptReminderProposalInput) (Reminder, error) {
	if actor.Kind != ActorKindOwner && actor.Kind != ActorKindDevice {
		return Reminder{}, ErrForbidden
	}
	if input.ClientRequestID == "" {
		return Reminder{}, ErrInvalidInput
	}
	note, proposal, err := service.findProposal(ctx, noteID, proposalID)
	if err != nil {
		return Reminder{}, err
	}
	if proposal.Status == ReminderProposalAccepted {
		return service.repository.GetReminder(ctx, proposal.ReminderID)
	}
	if proposal.Status != ReminderProposalPending {
		return Reminder{}, ErrInvalidInput
	}
	var schedule *Schedule
	if proposal.LocalDate != "" {
		if input.TimeZone == "" {
			return Reminder{}, ErrInvalidInput
		}
		schedule = &Schedule{LocalDate: proposal.LocalDate, LocalTime: proposal.LocalTime, TimeZone: input.TimeZone, Mode: TimeZoneModeFloating}
	}
	description := proposal.Description
	if title := strings.TrimSpace(note.Title); title != "" {
		description = strings.TrimSpace(description + "\n\n" + "Note: " + title)
	}
	reminder, err := service.CreateReminder(ctx, actor, CreateReminderInput{
		Title:           proposal.Title,
		Description:     description,
		Schedule:        schedule,
		Source:          "notes-ai-proposal",
		SourceExcerpt:   proposal.Reason,
		ClientRequestID: input.ClientRequestID,
	})
	if err != nil {
		return Reminder{}, err
	}
	proposal.Status = ReminderProposalAccepted
	proposal.ReminderID = reminder.ID
	proposal.UpdatedAt = service.clock().UTC()
	if err := service.saveAIChange(ctx, actor, note, AuditActionNoteProposalAccepted, NoteAIChange{UpdateProposals: []ReminderProposal{proposal}}, map[string]any{"proposal_id": proposal.ID, "reminder_id": reminder.ID}, ""); err != nil {
		return Reminder{}, err
	}
	return reminder, nil
}

func (service *Service) DismissReminderProposal(ctx context.Context, actor Actor, noteID string, proposalID string) (ReminderProposal, error) {
	if actor.Kind != ActorKindOwner && actor.Kind != ActorKindDevice {
		return ReminderProposal{}, ErrForbidden
	}
	note, proposal, err := service.findProposal(ctx, noteID, proposalID)
	if err != nil {
		return ReminderProposal{}, err
	}
	if proposal.Status != ReminderProposalPending {
		return proposal, nil
	}
	proposal.Status = ReminderProposalDismissed
	proposal.UpdatedAt = service.clock().UTC()
	if err := service.saveAIChange(ctx, actor, note, AuditActionNoteProposalDismissed, NoteAIChange{UpdateProposals: []ReminderProposal{proposal}}, map[string]any{"proposal_id": proposal.ID}, ""); err != nil {
		return ReminderProposal{}, err
	}
	return proposal, nil
}

func (service *Service) findProposal(ctx context.Context, noteID string, proposalID string) (Note, ReminderProposal, error) {
	note, err := service.repository.GetNote(ctx, noteID)
	if err != nil {
		return Note{}, ReminderProposal{}, err
	}
	aiState, err := service.repository.GetNoteAIState(ctx, noteID)
	if err != nil {
		return Note{}, ReminderProposal{}, err
	}
	for _, proposal := range aiState.Proposals {
		if proposal.ID == proposalID {
			return note, proposal, nil
		}
	}
	return Note{}, ReminderProposal{}, ErrNotFound
}

// DismissNoteRelation removes a link the AI made. It is archived, not
// deleted, like everything else in State.
func (service *Service) DismissNoteRelation(ctx context.Context, actor Actor, noteID string, relationID string) error {
	if actor.Kind != ActorKindOwner && actor.Kind != ActorKindDevice {
		return ErrForbidden
	}
	note, err := service.repository.GetNote(ctx, noteID)
	if err != nil {
		return err
	}
	aiState, err := service.repository.GetNoteAIState(ctx, noteID)
	if err != nil {
		return err
	}
	for _, relation := range aiState.Relations {
		if relation.ID == relationID {
			if relation.Archived {
				return nil
			}
			return service.saveAIChange(ctx, actor, note, AuditActionNoteRelationDismissed, NoteAIChange{ArchiveRelationIDs: []string{relationID}}, map[string]any{"relation_id": relationID}, "")
		}
	}
	return ErrNotFound
}

func (service *Service) saveAIChange(ctx context.Context, actor Actor, note Note, action AuditAction, change NoteAIChange, after map[string]any, clientRequestID string) error {
	eventID, err := service.newID()
	if err != nil {
		return fmt.Errorf("generate audit event ID: %w", err)
	}
	if clientRequestID == "" {
		// The audit log keys every event by request; server-side writes have
		// no client request, so the event is its own.
		clientRequestID = eventID
	}
	now := service.clock().UTC()
	fields := make([]string, 0, 4)
	for key := range after {
		fields = append(fields, key)
	}
	sort.Strings(fields)
	event, err := service.buildAuditEvent(eventID, "", action, actor, now, nil, "notes-ai", "", nil, after, fields, note.Revision, "", clientRequestID)
	if err != nil {
		return err
	}
	event.NoteID = note.ID
	return service.repository.SaveNoteAIChange(ctx, note.ID, change, event)
}

// GetNoteAISettings returns the stored settings or the defaults, with this
// month's spending.
func (service *Service) GetNoteAISettings(ctx context.Context) (NoteAISettings, error) {
	settings, found, err := service.repository.GetNoteAISettings(ctx)
	if err != nil {
		return NoteAISettings{}, err
	}
	if !found {
		settings = NoteAISettings{MonthlyLimitUSD: DefaultMonthlyLimitUSD, AgentModel: DefaultNotesAgentModel, TranscriptionModel: DefaultTranscriptionModel}
	}
	settings.Month = service.clock().UTC().Format("2006-01")
	spent, err := service.repository.NoteAIUsage(ctx, settings.Month)
	if err != nil {
		return NoteAISettings{}, err
	}
	settings.SpentThisMonthUSD = spent
	return settings, nil
}

var modelIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*/[a-zA-Z0-9][a-zA-Z0-9._:-]*$`)

func (service *Service) UpdateNoteAISettings(ctx context.Context, actor Actor, input UpdateNoteAISettingsInput) (NoteAISettings, error) {
	if actor.Kind != ActorKindOwner && actor.Kind != ActorKindDevice {
		return NoteAISettings{}, ErrForbidden
	}
	settings, err := service.GetNoteAISettings(ctx)
	if err != nil {
		return NoteAISettings{}, err
	}
	now := service.clock().UTC()
	if input.Consent != nil {
		if *input.Consent && !settings.Consent {
			settings.ConsentAt = &now
		}
		settings.Consent = *input.Consent
	}
	if input.MonthlyLimitUSD != nil {
		limit := *input.MonthlyLimitUSD
		if math.IsNaN(limit) || limit <= 0 || limit > 1000 {
			return NoteAISettings{}, ErrInvalidInput
		}
		settings.MonthlyLimitUSD = math.Round(limit*100) / 100
	}
	for _, model := range []struct {
		input  *string
		target *string
	}{{input.AgentModel, &settings.AgentModel}, {input.TranscriptionModel, &settings.TranscriptionModel}} {
		if model.input == nil {
			continue
		}
		if !modelIDPattern.MatchString(*model.input) || utf8.RuneCountInString(*model.input) > 120 {
			return NoteAISettings{}, ErrInvalidInput
		}
		*model.target = *model.input
	}
	settings.UpdatedAt = now
	eventID, err := service.newID()
	if err != nil {
		return NoteAISettings{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	stored := settings
	stored.Month, stored.SpentThisMonthUSD = "", 0
	event, err := service.buildAuditEvent(eventID, "", AuditActionNotesAISettingsUpdated, actor, now, nil, "rest", "", nil, stored, []string{"settings"}, 0, "", eventID)
	if err != nil {
		return NoteAISettings{}, err
	}
	if err := service.repository.SaveNoteAISettings(ctx, stored, event); err != nil {
		return NoteAISettings{}, err
	}
	return settings, nil
}

func (service *Service) AddNoteAIUsage(ctx context.Context, costUSD float64) error {
	if costUSD <= 0 || math.IsNaN(costUSD) || math.IsInf(costUSD, 0) {
		return nil
	}
	return service.repository.AddNoteAIUsage(ctx, service.clock().UTC().Format("2006-01"), costUSD)
}
