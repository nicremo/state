package notesai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nicremo/state/internal/state"
)

// Worker processes note jobs one at a time: transcription first, then the
// notes agent. Every paid call is checked against the monthly limit before
// it is made and counted after.
type Worker struct {
	service *state.Service
	gateway *Gateway
	media   *MediaStore
	logger  *slog.Logger
	kick    chan struct{}
	backoff []time.Duration
	now     func() time.Time
}

func NewWorker(service *state.Service, gateway *Gateway, media *MediaStore, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		service: service,
		gateway: gateway,
		media:   media,
		logger:  logger,
		kick:    make(chan struct{}, 1),
		backoff: []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute},
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// Kick wakes the worker after a processing request.
func (worker *Worker) Kick() {
	select {
	case worker.kick <- struct{}{}:
	default:
	}
}

// Run processes jobs until the context ends. Jobs a crash left running are
// queued again first.
func (worker *Worker) Run(ctx context.Context) {
	if err := worker.service.RequeueRunningNoteJobs(ctx); err != nil {
		worker.logger.Warn("notes AI could not requeue jobs", "error", err)
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		for {
			processed, err := worker.RunOnce(ctx)
			if err != nil && !errors.Is(err, context.Canceled) {
				worker.logger.Warn("notes AI job failed", "error", err)
			}
			if !processed || ctx.Err() != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-worker.kick:
		}
	}
}

// RunOnce claims and processes one due job. It reports whether there was one.
func (worker *Worker) RunOnce(ctx context.Context) (bool, error) {
	job, found, err := worker.service.ClaimNoteJob(ctx)
	if err != nil || !found {
		return false, err
	}
	return true, worker.process(ctx, job)
}

func (worker *Worker) process(ctx context.Context, job state.NoteJob) error {
	note, err := worker.service.GetNoteView(ctx, job.NoteID)
	if errors.Is(err, state.ErrNotFound) {
		return worker.service.FinishNoteJob(ctx, job, state.NoteJobDone, nil)
	}
	if err != nil {
		return err
	}
	if note.Archived {
		return worker.service.FinishNoteJob(ctx, job, state.NoteJobDone, nil)
	}
	settings, err := worker.service.GetNoteAISettings(ctx)
	if err != nil {
		return err
	}
	capabilities := worker.gateway.Capabilities(ctx, settings)
	stop := func(status string, message string) error {
		if err := worker.service.SetNoteProcessing(ctx, note.ID, state.NoteProcessing{Status: status, Error: message, Model: settings.AgentModel}); err != nil {
			return err
		}
		return worker.service.FinishNoteJob(ctx, job, state.NoteJobDone, nil)
	}
	switch {
	case !worker.gateway.Configured():
		return stop(state.NoteProcessingNotConfigured, "")
	case !settings.Consent:
		return stop(state.NoteProcessingConsentRequired, "")
	case !capabilities.AIAvailable:
		return stop(state.NoteProcessingFailed, unavailableMessage(capabilities.Reason, settings.AgentModel))
	}
	budget := &monthlyBudget{service: worker.service, limit: settings.MonthlyLimitUSD}
	if err := budget.Allow(ctx, 0); err != nil {
		return stop(state.NoteProcessingBudgetExhausted, budgetMessage(settings.MonthlyLimitUSD))
	}
	if err := worker.service.SetNoteProcessing(ctx, note.ID, state.NoteProcessing{Status: state.NoteProcessingRunning, Model: settings.AgentModel}); err != nil {
		return err
	}

	outcome, err := worker.run(ctx, note, settings, capabilities, budget)
	if err != nil {
		return worker.fail(ctx, job, note.ID, settings, err)
	}
	if err := worker.service.ApplyNoteAgentOutcome(ctx, job, note.Revision, outcome); err != nil {
		return err
	}
	return worker.service.FinishNoteJob(ctx, job, state.NoteJobDone, nil)
}

func (worker *Worker) run(ctx context.Context, note state.NoteView, settings state.NoteAISettings, capabilities Capabilities, budget *monthlyBudget) (state.NoteAgentOutcome, error) {
	transcripts, err := worker.transcribe(ctx, note, settings, capabilities, budget)
	if err != nil {
		return state.NoteAgentOutcome{}, err
	}
	images := make([]AgentImage, 0)
	for _, attachment := range note.Attachments {
		if attachment.Kind != state.NoteAttachmentImage {
			continue
		}
		if !capabilities.Vision.Available {
			return state.NoteAgentOutcome{}, permanent(fmt.Sprintf("The model %s cannot read photos.", settings.AgentModel))
		}
		if len(images) >= capabilities.Vision.MaxImages {
			break
		}
		content, err := worker.media.ReadAll(attachment.SHA256)
		if err != nil {
			return state.NoteAgentOutcome{}, permanent("A photo of this note is missing on the server.")
		}
		images = append(images, AgentImage{AttachmentID: attachment.ID, Mime: attachment.MimeType, Content: content})
	}
	if len(images) == 0 && len(transcripts) == 0 && strings.TrimSpace(note.PlainText) == "" {
		return state.NoteAgentOutcome{}, permanent("There is nothing to process yet.")
	}
	pricing, _ := worker.gateway.Model(settings.AgentModel)
	agent := NewAgent(worker.gateway.Client(), settings.AgentModel, worker.service, budget, pricing)
	return agent.Run(ctx, AgentInput{Note: note, Images: images, Transcripts: transcripts})
}

// transcribe turns every recording into text on the dedicated speech-to-text
// endpoint. Each transcript is stored at once, so a later failure does not
// pay for the same audio twice.
func (worker *Worker) transcribe(ctx context.Context, note state.NoteView, settings state.NoteAISettings, capabilities Capabilities, budget *monthlyBudget) ([]string, error) {
	transcripts := make([]string, 0)
	for _, attachment := range note.Attachments {
		if attachment.Kind != state.NoteAttachmentAudio {
			continue
		}
		if attachment.DerivedText != "" {
			transcripts = append(transcripts, attachment.DerivedText)
			continue
		}
		if !capabilities.Audio.Available {
			return nil, permanent(fmt.Sprintf("The transcription model %s is not available.", settings.TranscriptionModel))
		}
		audio, err := worker.media.ReadAll(attachment.SHA256)
		if err != nil {
			return nil, permanent("A recording of this note is missing on the server.")
		}
		seconds := float64(attachment.DurationMS) / 1000
		if seconds <= 0 {
			seconds = float64(len(audio)) / 6000 // about 48 kbit/s
		}
		estimate := seconds * transcriptionPrice(worker.gateway, settings.TranscriptionModel)
		if err := budget.Allow(ctx, estimate); err != nil {
			return nil, err
		}
		transcript, err := worker.gateway.Client().Transcribe(ctx, TranscribeRequest{Model: settings.TranscriptionModel, Audio: audio, Format: audioFormat(attachment.MimeType)})
		if err != nil {
			return nil, err
		}
		if err := budget.Spend(ctx, transcript.Usage.Cost); err != nil {
			return nil, err
		}
		text := strings.TrimSpace(transcript.Text)
		if err := worker.service.RecordAttachmentTexts(ctx, note.ID, map[string]state.NoteAttachmentText{
			attachment.ID: {Text: text, Kind: "transcript", Model: settings.TranscriptionModel},
		}); err != nil {
			return nil, err
		}
		transcripts = append(transcripts, text)
	}
	return transcripts, nil
}

func transcriptionPrice(gateway *Gateway, model string) float64 {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	if entry, found := findModel(gateway.sttModels, model); found {
		if price := parsePrice(entry.Pricing.Prompt); price > 0 {
			return price
		}
	}
	return 0.0001
}

func audioFormat(mime string) string {
	switch mime {
	case "audio/mpeg":
		return "mp3"
	case "audio/wav":
		return "wav"
	case "audio/aac":
		return "aac"
	default:
		return "m4a"
	}
}

// fail records why a job did not finish. Temporary provider problems are
// retried with a growing delay; everything else is shown on the note.
func (worker *Worker) fail(ctx context.Context, job state.NoteJob, noteID string, settings state.NoteAISettings, cause error) error {
	var providerError *ProviderError
	switch {
	case errors.Is(cause, ErrBudgetExhausted):
		if err := worker.service.SetNoteProcessing(ctx, noteID, state.NoteProcessing{Status: state.NoteProcessingBudgetExhausted, Error: budgetMessage(settings.MonthlyLimitUSD), Model: settings.AgentModel}); err != nil {
			return err
		}
		return worker.service.FinishNoteJob(ctx, job, state.NoteJobDone, nil)
	case errors.As(cause, &providerError) && providerError.Retryable && job.Attempts < len(worker.backoff)-1:
		retryAt := worker.now().Add(worker.backoff[job.Attempts])
		if err := worker.service.SetNoteProcessing(ctx, noteID, state.NoteProcessing{Status: state.NoteProcessingQueued, Error: "OpenRouter is busy, State tries again shortly.", Model: settings.AgentModel}); err != nil {
			return err
		}
		return worker.service.FinishNoteJob(ctx, job, state.NoteJobQueued, &retryAt)
	}
	if err := worker.service.SetNoteProcessing(ctx, noteID, state.NoteProcessing{Status: state.NoteProcessingFailed, Error: failureMessage(cause), Model: settings.AgentModel}); err != nil {
		return err
	}
	worker.logger.Info("notes AI job failed", "note_id", noteID, "reason", failureMessage(cause))
	return worker.service.FinishNoteJob(ctx, job, state.NoteJobFailed, nil)
}

type permanentError struct{ message string }

func (err permanentError) Error() string { return err.message }

func permanent(message string) error { return permanentError{message: message} }

// failureMessage is what the note shows. It is written for the owner and
// never contains the key or a raw provider response.
func failureMessage(err error) string {
	var providerError *ProviderError
	var permanentFailure permanentError
	switch {
	case errors.As(err, &permanentFailure):
		return permanentFailure.message
	case errors.As(err, &providerError):
		return providerError.Error()
	case errors.Is(err, ErrNoResult):
		return "The model gave no usable answer."
	case errors.Is(err, ErrNotConfigured):
		return "No OpenRouter key on the server."
	default:
		return "Processing failed on the server."
	}
}

func unavailableMessage(reason string, model string) string {
	switch reason {
	case ReasonAgentModelMissing:
		return fmt.Sprintf("The model %s is not offered by OpenRouter right now.", model)
	case ReasonAgentNeedsToolCalls:
		return fmt.Sprintf("The model %s cannot call tools.", model)
	case ReasonCatalogUnavailable:
		return "OpenRouter's model list could not be loaded."
	default:
		return "AI processing is not available."
	}
}

func budgetMessage(limit float64) string {
	return fmt.Sprintf("The monthly limit of %.2f USD is reached.", limit)
}

// monthlyBudget checks spending against the owner's monthly limit.
type monthlyBudget struct {
	service *state.Service
	limit   float64
	mu      sync.Mutex
}

func (budget *monthlyBudget) Allow(ctx context.Context, estimateUSD float64) error {
	budget.mu.Lock()
	defer budget.mu.Unlock()
	settings, err := budget.service.GetNoteAISettings(ctx)
	if err != nil {
		return err
	}
	if settings.SpentThisMonthUSD+estimateUSD > budget.limit || settings.SpentThisMonthUSD >= budget.limit {
		return ErrBudgetExhausted
	}
	return nil
}

func (budget *monthlyBudget) Spend(ctx context.Context, costUSD float64) error {
	return budget.service.AddNoteAIUsage(ctx, costUSD)
}
