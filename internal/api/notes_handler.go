package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/nicremo/state/internal/state"
)

// Notes are readable and writable by the owner, paired devices and paired
// agent harnesses. Runners execute reminders and never see notes. Archiving
// is limited to the owner and devices by the state service.
var noteActorKinds = []state.ActorKind{state.ActorKindOwner, state.ActorKindDevice, state.ActorKindHarness}

func (handler *Handler) createNote(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, noteActorKinds...)
	if !ok {
		return
	}
	var input state.CreateNoteInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if input.Source == "" {
		input.Source = "rest"
	}
	note, err := handler.state.CreateNote(request.Context(), actor, input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusCreated, note)
}

func (handler *Handler) listNotes(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateKind(writer, request, noteActorKinds...); !ok {
		return
	}
	limit, err := queryInteger(request, "limit", 100)
	if err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	includeArchived := false
	if raw := request.URL.Query().Get("include_archived"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(writer, state.ErrInvalidInput, nil)
			return
		}
		includeArchived = parsed
	}
	notes, err := handler.state.ListNotes(request.Context(), state.NoteListOptions{
		Query:           request.URL.Query().Get("q"),
		IncludeArchived: includeArchived,
		Limit:           limit,
	})
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"notes": notes})
}

func (handler *Handler) getNote(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateKind(writer, request, noteActorKinds...); !ok {
		return
	}
	note, err := handler.state.GetNote(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, note)
}

func (handler *Handler) updateNote(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, noteActorKinds...)
	if !ok {
		return
	}
	var input state.UpdateNoteInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if input.Source == "" {
		input.Source = "rest"
	}
	noteID := request.PathValue("id")
	note, err := handler.state.UpdateNote(request.Context(), actor, noteID, input)
	if err != nil {
		var details any
		if errors.Is(err, state.ErrRevisionConflict) {
			if current, getErr := handler.state.GetNote(request.Context(), noteID); getErr == nil {
				details = map[string]any{"server": current}
			}
		}
		writeError(writer, err, details)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusOK, note)
}

func (handler *Handler) getNoteHistory(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateKind(writer, request, noteActorKinds...); !ok {
		return
	}
	events, err := handler.state.ListNoteHistory(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"events": events})
}
