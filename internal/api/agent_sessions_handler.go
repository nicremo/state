package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/nicremo/state/internal/state"
)

// Agent sessions are driven by the owner and the owner's devices only.
var sessionActorKinds = []state.ActorKind{state.ActorKindOwner, state.ActorKindDevice}

func (handler *Handler) listAgentSessions(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateKind(writer, request, sessionActorKinds...); !ok {
		return
	}
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	sessions, err := handler.state.ListAgentSessions(request.Context(), limit)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"sessions": sessions})
}

func (handler *Handler) getAgentSession(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateKind(writer, request, sessionActorKinds...); !ok {
		return
	}
	session, err := handler.state.GetAgentSession(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, session)
}

func (handler *Handler) startAgentSession(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, sessionActorKinds...)
	if !ok {
		return
	}
	var input state.StartAgentSessionInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if input.Source == "" {
		input.Source = "rest"
	}
	session, err := handler.state.StartAgentSession(request.Context(), actor, input)
	handler.writeSession(writer, request, actor, http.StatusCreated, session, err)
}

func (handler *Handler) sendAgentSessionMessage(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, sessionActorKinds...)
	if !ok {
		return
	}
	var input state.SendAgentSessionMessageInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	input.SessionID = request.PathValue("id")
	if input.Source == "" {
		input.Source = "rest"
	}
	session, err := handler.state.SendAgentSessionMessage(request.Context(), actor, input)
	handler.writeSession(writer, request, actor, http.StatusOK, session, err)
}

func (handler *Handler) openAgentSessionOnMac(writer http.ResponseWriter, request *http.Request) {
	handler.sessionAction(writer, request, handler.state.OpenAgentSessionOnMac)
}

func (handler *Handler) closeAgentSession(writer http.ResponseWriter, request *http.Request) {
	handler.sessionAction(writer, request, handler.state.CloseAgentSession)
}

func (handler *Handler) sessionAction(writer http.ResponseWriter, request *http.Request, action func(ctx context.Context, actor state.Actor, input state.AgentSessionActionInput) (state.AgentSessionView, error)) {
	actor, ok := handler.authenticateKind(writer, request, sessionActorKinds...)
	if !ok {
		return
	}
	var input state.AgentSessionActionInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	input.SessionID = request.PathValue("id")
	if input.Source == "" {
		input.Source = "rest"
	}
	session, err := action(request.Context(), actor, input)
	handler.writeSession(writer, request, actor, http.StatusOK, session, err)
}

func (handler *Handler) writeSession(writer http.ResponseWriter, request *http.Request, actor state.Actor, status int, session state.AgentSessionView, err error) {
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, status, session)
}
