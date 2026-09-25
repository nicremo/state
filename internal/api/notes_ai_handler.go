package api

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/nicremo/state/internal/notesai"
	"github.com/nicremo/state/internal/state"
)

// NotesAI is what the REST API needs from the notes AI: the private media
// store, the model capabilities and a way to wake the worker.
type NotesAI interface {
	Media() *notesai.MediaStore
	Capabilities(ctx context.Context, settings state.NoteAISettings) notesai.Capabilities
	Kick()
	SetKey(ctx context.Context, key string) error
	RemoveKey() error
}

// setNotesAIKey takes the owner's OpenRouter key from the app. The server
// checks it with OpenRouter, keeps it in its own secret store and never
// sends it back; the app does not keep it either.
func (handler *Handler) setNotesAIKey(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, noteActorKinds...)
	if !ok {
		return
	}
	if !state.CanManageNotesAIKey(actor) {
		writeError(writer, state.ErrForbidden, nil)
		return
	}
	var input struct {
		Key string `json:"key"`
	}
	if err := decodeJSON(request, &input); err != nil || handler.notesAI == nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if err := handler.notesAI.SetKey(request.Context(), input.Key); err != nil {
		reason := "key_unverified"
		switch {
		case errors.Is(err, notesai.ErrMalformedKey):
			reason = "malformed_key"
		case errors.Is(err, notesai.ErrInvalidKey):
			reason = "invalid_key"
		}
		writeError(writer, state.ErrInvalidInput, map[string]string{"reason": reason})
		return
	}
	handler.finishKeyChange(writer, request, actor, true)
}

func (handler *Handler) removeNotesAIKey(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, noteActorKinds...)
	if !ok {
		return
	}
	if !state.CanManageNotesAIKey(actor) {
		writeError(writer, state.ErrForbidden, nil)
		return
	}
	if handler.notesAI == nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if err := handler.notesAI.RemoveKey(); err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.finishKeyChange(writer, request, actor, false)
}

func (handler *Handler) finishKeyChange(writer http.ResponseWriter, request *http.Request, actor state.Actor, configured bool) {
	if err := handler.state.RecordNotesAIKeyChange(request.Context(), actor, configured); err != nil {
		writeError(writer, err, nil)
		return
	}
	settings, err := handler.state.GetNoteAISettings(request.Context())
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusOK, handler.settingsResponse(settings))
}

// uploadNoteAttachment stores one photo or recording. The body is the raw
// file; the headers carry the request ID, order and the client's hash, which
// the server checks against what it received before anything is attached.
func (handler *Handler) uploadNoteAttachment(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, noteActorKinds...)
	if !ok {
		return
	}
	if handler.notesAI == nil || handler.notesAI.Media() == nil {
		writeError(writer, state.ErrInvalidInput, map[string]string{"reason": "media_unavailable"})
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || !notesai.AllowedMedia(mediaType) {
		writeError(writer, state.ErrInvalidInput, map[string]string{"reason": "unsupported_media_type"})
		return
	}
	kind := state.NoteAttachmentImage
	limit := handler.state.NoteMediaPolicy().MaxImageBytes
	if strings.HasPrefix(mediaType, "audio/") {
		kind = state.NoteAttachmentAudio
		limit = handler.state.NoteMediaPolicy().MaxAudioBytes
	}
	requestID := request.Header.Get("X-State-Client-Request-Id")
	claimedHash := strings.ToLower(request.Header.Get("X-State-Content-SHA256"))
	ordinal, ordinalErr := strconv.Atoi(request.Header.Get("X-State-Ordinal"))
	duration := int64(0)
	if raw := request.Header.Get("X-State-Duration-Ms"); raw != "" {
		duration, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			ordinalErr = err
		}
	}
	if requestID == "" || claimedHash == "" || ordinalErr != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if request.ContentLength > limit {
		writeError(writer, state.ErrInvalidInput, map[string]string{"reason": "too_large"})
		return
	}
	defer request.Body.Close()
	stored, err := handler.notesAI.Media().Stage(http.MaxBytesReader(writer, request.Body, limit+1), limit, mediaType)
	if err != nil {
		reason := "media_rejected"
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			reason = "too_large"
		}
		if errors.Is(err, notesai.ErrMediaRejected) || errors.As(err, &tooLarge) {
			writeError(writer, state.ErrInvalidInput, map[string]string{"reason": reason})
			return
		}
		writeError(writer, err, nil)
		return
	}
	if stored.SHA256 != claimedHash {
		stored.Discard()
		writeError(writer, state.ErrInvalidInput, map[string]string{"reason": "hash_mismatch"})
		return
	}
	noteID := request.PathValue("id")
	if _, err := handler.state.AddNoteAttachment(request.Context(), actor, noteID, state.AddNoteAttachmentInput{
		Kind:            kind,
		MimeType:        mediaType,
		ByteSize:        stored.Size,
		SHA256:          stored.SHA256,
		DurationMS:      duration,
		Ordinal:         ordinal,
		ClientRequestID: requestID,
	}); err != nil {
		// Refused uploads leave nothing behind on the disk.
		stored.Discard()
		writeError(writer, err, nil)
		return
	}
	if err := stored.Commit(); err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	handler.writeNoteViewByID(writer, request, http.StatusCreated, noteID)
}

func (handler *Handler) downloadNoteAttachment(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateKind(writer, request, noteActorKinds...); !ok {
		return
	}
	if handler.notesAI == nil || handler.notesAI.Media() == nil {
		writeError(writer, state.ErrNotFound, nil)
		return
	}
	view, err := handler.state.GetNoteView(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	for _, attachment := range view.Attachments {
		if attachment.ID != request.PathValue("attachment_id") {
			continue
		}
		reader, size, err := handler.notesAI.Media().Open(attachment.SHA256)
		if err != nil {
			writeError(writer, state.ErrNotFound, nil)
			return
		}
		defer reader.Close()
		writer.Header().Set("Content-Type", attachment.MimeType)
		writer.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		writer.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		writer.Header().Set("ETag", `"`+attachment.SHA256+`"`)
		writer.WriteHeader(http.StatusOK)
		_, _ = io.Copy(writer, reader)
		return
	}
	writeError(writer, state.ErrNotFound, nil)
}

func (handler *Handler) requestNoteProcessing(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, noteActorKinds...)
	if !ok {
		return
	}
	var input struct {
		ClientRequestID string `json:"client_request_id"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	noteID := request.PathValue("id")
	processing, err := handler.state.RequestNoteProcessing(request.Context(), actor, noteID, input.ClientRequestID)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	if processing.Status == state.NoteProcessingQueued && handler.notesAI != nil {
		handler.notesAI.Kick()
	}
	handler.notifySync(request.Context(), actor.ID)
	handler.writeNoteViewByID(writer, request, http.StatusAccepted, noteID)
}

func (handler *Handler) getNoteProcessing(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateKind(writer, request, noteActorKinds...); !ok {
		return
	}
	view, err := handler.state.GetNoteView(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"processing": view.Processing, "ai": view.AI, "attachments": view.Attachments})
}

func (handler *Handler) listNoteRelations(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateKind(writer, request, noteActorKinds...); !ok {
		return
	}
	view, err := handler.state.GetNoteView(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"relations": view.Relations})
}

func (handler *Handler) dismissNoteRelation(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, noteActorKinds...)
	if !ok {
		return
	}
	var input struct {
		Archived bool `json:"archived"`
	}
	if err := decodeJSON(request, &input); err != nil || !input.Archived {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	noteID := request.PathValue("id")
	if err := handler.state.DismissNoteRelation(request.Context(), actor, noteID, request.PathValue("relation_id")); err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	handler.writeNoteViewByID(writer, request, http.StatusOK, noteID)
}

func (handler *Handler) acceptReminderProposal(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, noteActorKinds...)
	if !ok {
		return
	}
	var input state.AcceptReminderProposalInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	reminder, err := handler.state.AcceptReminderProposal(request.Context(), actor, request.PathValue("id"), request.PathValue("proposal_id"), input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusCreated, reminder)
}

func (handler *Handler) dismissReminderProposal(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, noteActorKinds...)
	if !ok {
		return
	}
	noteID := request.PathValue("id")
	if _, err := handler.state.DismissReminderProposal(request.Context(), actor, noteID, request.PathValue("proposal_id")); err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	handler.writeNoteViewByID(writer, request, http.StatusOK, noteID)
}

// getNoteCapabilities tells the app what to offer: whether AI runs at all,
// how many photos the model takes and how long a recording segment may be.
func (handler *Handler) getNoteCapabilities(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateKind(writer, request, noteActorKinds...); !ok {
		return
	}
	settings, err := handler.state.GetNoteAISettings(request.Context())
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	var capabilities notesai.Capabilities
	if handler.notesAI != nil {
		capabilities = handler.notesAI.Capabilities(request.Context(), settings)
	} else {
		capabilities = notesai.ResolveCapabilities(nil, nil, settings, false, handler.state.NoteMediaPolicy())
	}
	writeJSON(writer, http.StatusOK, capabilities)
}

func (handler *Handler) getNotesAISettings(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateKind(writer, request, noteActorKinds...); !ok {
		return
	}
	settings, err := handler.state.GetNoteAISettings(request.Context())
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, handler.settingsResponse(settings))
}

func (handler *Handler) updateNotesAISettings(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, noteActorKinds...)
	if !ok {
		return
	}
	var input state.UpdateNoteAISettingsInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	settings, err := handler.state.UpdateNoteAISettings(request.Context(), actor, input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusOK, handler.settingsResponse(settings))
}

// settingsResponse adds whether the server holds a key. The key itself is
// never part of any response.
func (handler *Handler) settingsResponse(settings state.NoteAISettings) map[string]any {
	return map[string]any{
		"consent":              settings.Consent,
		"consent_at":           settings.ConsentAt,
		"monthly_limit_usd":    settings.MonthlyLimitUSD,
		"agent_model":          settings.AgentModel,
		"transcription_model":  settings.TranscriptionModel,
		"month":                settings.Month,
		"spent_this_month_usd": settings.SpentThisMonthUSD,
		"key_configured":       handler.state.NoteAIAvailable(),
	}
}

func (handler *Handler) writeNoteViewByID(writer http.ResponseWriter, request *http.Request, status int, noteID string) {
	view, err := handler.state.GetNoteView(request.Context(), noteID)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, status, view)
}
