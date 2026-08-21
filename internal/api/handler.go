package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	stateauth "github.com/nicremo/state/internal/auth"
	statepush "github.com/nicremo/state/internal/push"
	"github.com/nicremo/state/internal/state"
)

const maxRequestBodyBytes = 1 << 20

type Config struct {
	Auth    *stateauth.Manager
	State   *state.Service
	Push    *statepush.Service
	Version string
}

type Handler struct {
	auth    *stateauth.Manager
	state   *state.Service
	push    *statepush.Service
	version string
	router  *http.ServeMux
}

type ReminderDetail struct {
	Reminder    state.Reminder     `json:"reminder"`
	Comments    []state.Comment    `json:"comments"`
	Occurrences []state.Occurrence `json:"occurrences"`
	History     []state.AuditEvent `json:"history"`
	Runs        []state.AgentRun   `json:"runs"`
}

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

type Export struct {
	APIVersion  string           `json:"api_version"`
	GeneratedAt time.Time        `json:"generated_at"`
	Cursor      int64            `json:"cursor"`
	Reminders   []ReminderDetail `json:"reminders"`
}

func NewHandler(config Config) http.Handler {
	handler := &Handler{
		auth:    config.Auth,
		state:   config.State,
		push:    config.Push,
		version: config.Version,
		router:  http.NewServeMux(),
	}
	handler.registerRoutes()
	return securityHeaders(handler.router)
}

func (handler *Handler) registerRoutes() {
	handler.router.HandleFunc("GET /health/live", handler.healthLive)
	handler.router.HandleFunc("GET /health/ready", handler.healthReady)
	handler.router.HandleFunc("GET /version", handler.getVersion)
	handler.router.HandleFunc("POST /api/v1/pairing/owner", handler.bootstrapOwner)
	handler.router.HandleFunc("POST /api/v1/pairing/codes", handler.createPairingCode)
	handler.router.HandleFunc("POST /api/v1/pairing/exchange", handler.exchangePairingCode)
	handler.router.HandleFunc("POST /api/v1/credentials/rotate", handler.rotateCredential)
	handler.router.HandleFunc("POST /api/v1/credentials/revoke", handler.revokeCredential)
	handler.router.HandleFunc("GET /api/v1/agents", handler.listAgents)
	handler.router.HandleFunc("DELETE /api/v1/agents/{id}", handler.revokeActor)
	handler.router.HandleFunc("GET /api/v1/devices", handler.listDevices)
	handler.router.HandleFunc("DELETE /api/v1/devices/{id}", handler.revokeActor)
	handler.router.HandleFunc("PUT /api/v1/devices/push", handler.registerDevicePush)
	handler.router.HandleFunc("DELETE /api/v1/devices/push", handler.deleteDevicePush)
	handler.router.HandleFunc("POST /api/v1/devices/push/confirmations", handler.confirmDeviceOccurrences)
	handler.router.HandleFunc("POST /api/v1/reminders", handler.createReminder)
	handler.router.HandleFunc("GET /api/v1/reminders", handler.listReminders)
	handler.router.HandleFunc("GET /api/v1/reminders/{id}", handler.getReminder)
	handler.router.HandleFunc("PATCH /api/v1/reminders/{id}", handler.updateReminder)
	handler.router.HandleFunc("GET /api/v1/reminders/{id}/history", handler.getReminderHistory)
	handler.router.HandleFunc("POST /api/v1/reminders/{id}/comments", handler.addComment)
	handler.router.HandleFunc("GET /api/v1/reminders/{id}/comments", handler.listComments)
	handler.router.HandleFunc("GET /api/v1/reminders/{id}/occurrences", handler.listOccurrences)
	handler.router.HandleFunc("POST /api/v1/occurrences/{id}/complete", handler.completeOccurrence)
	handler.router.HandleFunc("POST /api/v1/occurrences/{id}/snooze", handler.snoozeOccurrence)
	handler.router.HandleFunc("GET /api/v1/changes", handler.getChanges)
	handler.router.HandleFunc("GET /api/v1/briefing", handler.getBriefing)
	handler.router.HandleFunc("GET /api/v1/export", handler.exportState)
	handler.router.HandleFunc("POST /api/v1/projects", handler.createProject)
	handler.router.HandleFunc("GET /api/v1/projects", handler.listProjects)
	handler.router.HandleFunc("GET /api/v1/projects/{id}", handler.getProject)
	handler.router.HandleFunc("PATCH /api/v1/projects/{id}", handler.updateProject)
	handler.router.HandleFunc("POST /api/v1/policies", handler.createPolicy)
	handler.router.HandleFunc("GET /api/v1/policies", handler.listPolicies)
	handler.router.HandleFunc("GET /api/v1/policies/{id}", handler.getPolicy)
	handler.router.HandleFunc("PATCH /api/v1/policies/{id}", handler.updatePolicy)
	handler.router.HandleFunc("POST /api/v1/runner/registration", handler.registerRunner)
	handler.router.HandleFunc("POST /api/v1/runner/claims", handler.claimAgentRun)
	handler.router.HandleFunc("POST /api/v1/runner/runs/{id}/events", handler.reportRunEvent)
	handler.router.HandleFunc("POST /api/v1/runner/runs/{id}/complete", handler.completeRun)
	handler.router.HandleFunc("POST /api/v1/runner/runs/{id}/approval", handler.requestRunApproval)
	handler.router.HandleFunc("GET /api/v1/runners", handler.listRunners)
	handler.router.HandleFunc("PATCH /api/v1/runners/{id}", handler.updateRunner)
	handler.router.HandleFunc("GET /api/v1/runs", handler.listRuns)
	handler.router.HandleFunc("GET /api/v1/runs/{id}", handler.getRun)
	handler.router.HandleFunc("POST /api/v1/runs", handler.createManualRun)
	handler.router.HandleFunc("POST /api/v1/runs/{id}/cancel", handler.cancelRun)
	handler.router.HandleFunc("POST /api/v1/runs/{id}/approval", handler.approveRun)
}

func (handler *Handler) healthLive(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (handler *Handler) healthReady(writer http.ResponseWriter, _ *http.Request) {
	if handler.auth == nil || handler.state == nil || handler.push == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ready"})
}

func (handler *Handler) getVersion(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{
		"name":        "state-server",
		"version":     handler.version,
		"api_version": "v1",
	})
}

func (handler *Handler) bootstrapOwner(writer http.ResponseWriter, request *http.Request) {
	var input stateauth.OwnerBootstrapRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	credential, err := handler.auth.BootstrapOwner(request.Context(), request.Header.Get("X-State-Bootstrap-Token"), input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusCreated, credential)
}

func (handler *Handler) createPairingCode(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	var input stateauth.PairingCodeRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	pairingCode, err := handler.auth.CreatePairingCode(request.Context(), actor, input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusCreated, pairingCode)
}

func (handler *Handler) exchangePairingCode(writer http.ResponseWriter, request *http.Request) {
	input := struct {
		Code string `json:"code"`
	}{}
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	credential, err := handler.auth.ExchangePairingCode(request.Context(), input.Code)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusCreated, credential)
}

func (handler *Handler) rotateCredential(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticate(writer, request); !ok {
		return
	}
	credential, err := handler.auth.RotateCredential(request.Context(), bearerToken(request))
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, credential)
}

func (handler *Handler) revokeCredential(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticate(writer, request); !ok {
		return
	}
	if err := handler.auth.RevokeCredential(request.Context(), bearerToken(request)); err != nil {
		writeError(writer, err, nil)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) listAgents(writer http.ResponseWriter, request *http.Request) {
	handler.listActors(writer, request, state.ActorKindHarness)
}

func (handler *Handler) listDevices(writer http.ResponseWriter, request *http.Request) {
	handler.listActors(writer, request, state.ActorKindDevice)
}

func (handler *Handler) listActors(writer http.ResponseWriter, request *http.Request, kind state.ActorKind) {
	actor, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	actors, err := handler.auth.ListActors(request.Context(), actor, kind)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"actors": actors})
}

func (handler *Handler) revokeActor(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	if err := handler.auth.RevokeActor(request.Context(), actor, request.PathValue("id")); err != nil {
		writeError(writer, err, nil)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) registerDevicePush(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	var input statepush.RegisterDeviceInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	route, err := handler.push.RegisterDevice(request.Context(), actor, input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, route)
}

func (handler *Handler) deleteDevicePush(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	if err := handler.push.DeleteDevice(request.Context(), actor); err != nil {
		writeError(writer, err, nil)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) confirmDeviceOccurrences(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	input := struct {
		OccurrenceIDs []string `json:"occurrence_ids"`
	}{}
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if err := handler.push.ConfirmOccurrences(request.Context(), actor, input.OccurrenceIDs); err != nil {
		writeError(writer, err, nil)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) createReminder(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	var input state.CreateReminderInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if input.Source == "" {
		input.Source = "rest"
	}
	reminder, err := handler.state.CreateReminder(request.Context(), actor, input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusCreated, reminder)
}

func (handler *Handler) listReminders(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticate(writer, request); !ok {
		return
	}
	limit, err := queryInteger(request, "limit", 100)
	if err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	query := strings.TrimSpace(request.URL.Query().Get("q"))
	var reminders []state.Reminder
	if query != "" {
		reminders, err = handler.state.SearchReminders(request.Context(), query, limit)
	} else {
		includeArchived := false
		if value := request.URL.Query().Get("include_archived"); value != "" {
			includeArchived, err = strconv.ParseBool(value)
			if err != nil {
				writeError(writer, state.ErrInvalidInput, nil)
				return
			}
		}
		reminders, err = handler.state.ListReminders(request.Context(), state.ReminderListOptions{
			IncludeArchived: includeArchived,
			Limit:           limit,
		})
	}
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"reminders": reminders})
}

func (handler *Handler) getReminder(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticate(writer, request); !ok {
		return
	}
	reminderID := request.PathValue("id")
	reminder, err := handler.state.GetReminder(request.Context(), reminderID)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	history, err := handler.state.ListAuditEvents(request.Context(), reminderID)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	comments, err := handler.state.ListComments(request.Context(), reminderID)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	occurrences, err := handler.state.ListOccurrences(request.Context(), reminderID, state.OccurrenceListOptions{Limit: 500})
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	runs, err := handler.state.ListAgentRuns(request.Context(), state.AgentRunListFilter{ReminderID: reminderID, Limit: 20})
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, ReminderDetail{
		Reminder:    reminder,
		Comments:    comments,
		Occurrences: occurrences,
		History:     history,
		Runs:        runs,
	})
}

func (handler *Handler) exportState(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	if actor.Kind != state.ActorKindOwner {
		writeError(writer, state.ErrForbidden, nil)
		return
	}

	const pageSize = 500
	cursor := int64(0)
	reminderIDs := make(map[string]struct{})
	for {
		changes, err := handler.state.ListChanges(request.Context(), cursor, pageSize)
		if err != nil {
			writeError(writer, err, nil)
			return
		}
		for _, change := range changes {
			reminderIDs[change.Event.ReminderID] = struct{}{}
			cursor = change.Cursor
		}
		if len(changes) < pageSize {
			break
		}
	}

	reminders, err := handler.state.ListReminders(request.Context(), state.ReminderListOptions{
		IncludeArchived: true,
		Limit:           pageSize,
	})
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	for _, reminder := range reminders {
		reminderIDs[reminder.ID] = struct{}{}
	}

	ids := make([]string, 0, len(reminderIDs))
	for id := range reminderIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	details := make([]ReminderDetail, 0, len(ids))
	for _, id := range ids {
		reminder, err := handler.state.GetReminder(request.Context(), id)
		if err != nil {
			writeError(writer, err, nil)
			return
		}
		comments, err := handler.state.ListComments(request.Context(), id)
		if err != nil {
			writeError(writer, err, nil)
			return
		}
		occurrences, err := handler.state.ListOccurrences(request.Context(), id, state.OccurrenceListOptions{Limit: pageSize})
		if err != nil {
			writeError(writer, err, nil)
			return
		}
		history, err := handler.state.ListAuditEvents(request.Context(), id)
		if err != nil {
			writeError(writer, err, nil)
			return
		}
		runs, err := handler.state.ListAgentRuns(request.Context(), state.AgentRunListFilter{ReminderID: id, Limit: 20})
		if err != nil {
			writeError(writer, err, nil)
			return
		}
		details = append(details, ReminderDetail{
			Reminder:    reminder,
			Comments:    comments,
			Occurrences: occurrences,
			History:     history,
			Runs:        runs,
		})
	}

	writeJSON(writer, http.StatusOK, Export{
		APIVersion:  "v1",
		GeneratedAt: time.Now().UTC(),
		Cursor:      cursor,
		Reminders:   details,
	})
}

func (handler *Handler) updateReminder(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	var input state.UpdateReminderInput
	if err := decodeUpdateReminder(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if input.Source == "" {
		input.Source = "rest"
	}
	reminderID := request.PathValue("id")
	reminder, err := handler.state.UpdateReminder(request.Context(), actor, reminderID, input)
	if err != nil {
		var details any
		if errors.Is(err, state.ErrRevisionConflict) {
			current, getErr := handler.state.GetReminder(request.Context(), reminderID)
			if getErr == nil {
				details = map[string]any{"server": current}
			}
		}
		writeError(writer, err, details)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusOK, reminder)
}

func (handler *Handler) getReminderHistory(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticate(writer, request); !ok {
		return
	}
	history, err := handler.state.ListAuditEvents(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"events": history})
}

func (handler *Handler) addComment(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	var input state.AddCommentInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if input.Source == "" {
		input.Source = "rest"
	}
	comment, err := handler.state.AddComment(request.Context(), actor, request.PathValue("id"), input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusCreated, comment)
}

func (handler *Handler) listComments(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticate(writer, request); !ok {
		return
	}
	comments, err := handler.state.ListComments(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"comments": comments})
}

func (handler *Handler) listOccurrences(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticate(writer, request); !ok {
		return
	}
	limit, err := queryInteger(request, "limit", 100)
	if err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	options := state.OccurrenceListOptions{Limit: limit}
	if status := request.URL.Query().Get("status"); status != "" {
		parsed := state.OccurrenceStatus(status)
		if parsed != state.OccurrenceStatusPending && parsed != state.OccurrenceStatusCompleted && parsed != state.OccurrenceStatusSnoozed {
			writeError(writer, state.ErrInvalidInput, nil)
			return
		}
		options.Status = &parsed
	}
	occurrences, err := handler.state.ListOccurrences(request.Context(), request.PathValue("id"), options)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"occurrences": occurrences})
}

func (handler *Handler) completeOccurrence(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	var input state.CompleteOccurrenceInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if input.Source == "" {
		input.Source = "rest"
	}
	occurrence, err := handler.state.CompleteOccurrence(request.Context(), actor, request.PathValue("id"), input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusOK, occurrence)
}

func (handler *Handler) snoozeOccurrence(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	var input state.SnoozeOccurrenceInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if input.Source == "" {
		input.Source = "rest"
	}
	occurrence, err := handler.state.SnoozeOccurrence(request.Context(), actor, request.PathValue("id"), input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusOK, occurrence)
}

func (handler *Handler) createProject(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, state.ActorKindOwner)
	if !ok {
		return
	}
	var input state.CreateProjectInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if input.Source == "" {
		input.Source = "rest"
	}
	project, err := handler.state.CreateProject(request.Context(), actor, input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusCreated, project)
}

func (handler *Handler) listProjects(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateKind(writer, request, state.ActorKindOwner, state.ActorKindDevice); !ok {
		return
	}
	projects, err := handler.state.ListProjects(request.Context())
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"projects": projects})
}

func (handler *Handler) getProject(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateKind(writer, request, state.ActorKindOwner, state.ActorKindDevice); !ok {
		return
	}
	project, err := handler.state.GetProject(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, project)
}

func (handler *Handler) updateProject(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, state.ActorKindOwner)
	if !ok {
		return
	}
	var input state.UpdateProjectInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if input.Source == "" {
		input.Source = "rest"
	}
	project, err := handler.state.UpdateProject(request.Context(), actor, request.PathValue("id"), input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusOK, project)
}

func (handler *Handler) createPolicy(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, state.ActorKindOwner)
	if !ok {
		return
	}
	var input state.CreatePolicyInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if input.Source == "" {
		input.Source = "rest"
	}
	policy, err := handler.state.CreatePolicy(request.Context(), actor, input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusCreated, policy)
}

func (handler *Handler) listPolicies(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateKind(writer, request, state.ActorKindOwner, state.ActorKindDevice); !ok {
		return
	}
	policies, err := handler.state.ListPolicies(request.Context())
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"policies": policies})
}

func (handler *Handler) getPolicy(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateKind(writer, request, state.ActorKindOwner, state.ActorKindDevice); !ok {
		return
	}
	policy, err := handler.state.GetPolicy(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, policy)
}

func (handler *Handler) updatePolicy(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, state.ActorKindOwner)
	if !ok {
		return
	}
	var input state.UpdatePolicyInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if input.Source == "" {
		input.Source = "rest"
	}
	policy, err := handler.state.UpdatePolicy(request.Context(), actor, request.PathValue("id"), input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusOK, policy)
}

func (handler *Handler) registerRunner(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, state.ActorKindRunner)
	if !ok {
		return
	}
	var input state.RegisterRunnerInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if input.Source == "" {
		input.Source = "rest"
	}
	runner, err := handler.state.RegisterRunner(request.Context(), actor, input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusCreated, runner)
}

func (handler *Handler) claimAgentRun(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, state.ActorKindRunner)
	if !ok {
		return
	}
	waitSeconds := 0
	if value := request.URL.Query().Get("wait_seconds"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			writeError(writer, state.ErrInvalidInput, nil)
			return
		}
		waitSeconds = parsed
	}
	run, err := handler.state.ClaimAgentRun(request.Context(), actor, state.ClaimRunInput{WaitSeconds: waitSeconds})
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusOK, run)
}

func (handler *Handler) reportRunEvent(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, state.ActorKindRunner)
	if !ok {
		return
	}
	var input state.ReportRunEventInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	input.RunID = request.PathValue("id")
	run, err := handler.state.ReportAgentRunEvent(request.Context(), actor, input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusOK, run)
}

func (handler *Handler) completeRun(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, state.ActorKindRunner)
	if !ok {
		return
	}
	var input state.CompleteRunInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	input.RunID = request.PathValue("id")
	if input.Source == "" {
		input.Source = "rest"
	}
	run, err := handler.state.CompleteAgentRun(request.Context(), actor, input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusOK, run)
}

func (handler *Handler) requestRunApproval(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, state.ActorKindRunner)
	if !ok {
		return
	}
	var input state.RequestApprovalInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	input.RunID = request.PathValue("id")
	run, err := handler.state.RequestAgentApproval(request.Context(), actor, input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusOK, run)
}

func (handler *Handler) listRunners(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateKind(writer, request, state.ActorKindOwner, state.ActorKindDevice); !ok {
		return
	}
	runners, err := handler.state.ListRunners(request.Context())
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"runners": runners})
}

func (handler *Handler) updateRunner(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, state.ActorKindOwner)
	if !ok {
		return
	}
	var input state.UpdateRunnerInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if input.Source == "" {
		input.Source = "rest"
	}
	runner, err := handler.state.UpdateRunner(request.Context(), actor, request.PathValue("id"), input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusOK, runner)
}

func (handler *Handler) listRuns(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateKind(writer, request, state.ActorKindOwner, state.ActorKindDevice); !ok {
		return
	}
	limit, err := queryInteger(request, "limit", 100)
	if err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	filter := state.AgentRunListFilter{
		ReminderID: strings.TrimSpace(request.URL.Query().Get("reminder_id")),
		RunnerID:   strings.TrimSpace(request.URL.Query().Get("runner_id")),
		Limit:      limit,
	}
	if value := strings.TrimSpace(request.URL.Query().Get("status")); value != "" {
		status := state.AgentRunStatus(value)
		switch status {
		case state.AgentRunStatusPlanned,
			state.AgentRunStatusEligible,
			state.AgentRunStatusClaimed,
			state.AgentRunStatusRunning,
			state.AgentRunStatusNeedsApproval,
			state.AgentRunStatusSucceeded,
			state.AgentRunStatusFailed,
			state.AgentRunStatusCancelled,
			state.AgentRunStatusExpired:
			filter.Status = &status
		default:
			writeError(writer, state.ErrInvalidInput, nil)
			return
		}
	}
	runs, err := handler.state.ListAgentRuns(request.Context(), filter)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"runs": runs})
}

func (handler *Handler) getRun(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateKind(writer, request, state.ActorKindOwner, state.ActorKindDevice); !ok {
		return
	}
	run, err := handler.state.GetAgentRun(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, run)
}

func (handler *Handler) createManualRun(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, state.ActorKindOwner)
	if !ok {
		return
	}
	var input state.CreateManualRunInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	if input.Source == "" {
		input.Source = "rest"
	}
	run, err := handler.state.CreateManualRun(request.Context(), actor, input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusCreated, run)
}

func (handler *Handler) cancelRun(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, state.ActorKindOwner)
	if !ok {
		return
	}
	var input state.CancelRunInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	input.RunID = request.PathValue("id")
	if input.Source == "" {
		input.Source = "rest"
	}
	run, err := handler.state.CancelAgentRun(request.Context(), actor, input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusOK, run)
}

func (handler *Handler) approveRun(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticateKind(writer, request, state.ActorKindOwner)
	if !ok {
		return
	}
	var input state.ApproveRunInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	input.RunID = request.PathValue("id")
	if input.Source == "" {
		input.Source = "rest"
	}
	run, err := handler.state.ApproveAgentRun(request.Context(), actor, input)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	handler.notifySync(request.Context(), actor.ID)
	writeJSON(writer, http.StatusOK, run)
}

func (handler *Handler) notifySync(parent context.Context, excludedActorID string) {
	if handler.push == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	_ = handler.push.NotifySync(ctx, excludedActorID)
}

func (handler *Handler) getChanges(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticate(writer, request); !ok {
		return
	}
	after, err := queryInteger64(request, "after", 0)
	if err != nil || after < 0 {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	limit, err := queryInteger(request, "limit", 100)
	if err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	changes, err := handler.state.ListChanges(request.Context(), after, limit)
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	cursor := after
	if len(changes) > 0 {
		cursor = changes[len(changes)-1].Cursor
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"changes": changes,
		"cursor":  cursor,
	})
}

func (handler *Handler) getBriefing(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticate(writer, request); !ok {
		return
	}
	after, err := queryInteger64(request, "after", 0)
	if err != nil || after < 0 {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	limit, err := queryInteger(request, "limit", 50)
	if err != nil {
		writeError(writer, state.ErrInvalidInput, nil)
		return
	}
	briefing, err := handler.state.GetBriefing(request.Context(), state.BriefingOptions{
		AfterCursor: after,
		Limit:       limit,
	})
	if err != nil {
		writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusOK, briefing)
}

func (handler *Handler) authenticate(writer http.ResponseWriter, request *http.Request) (state.Actor, bool) {
	token := bearerToken(request)
	if token == "" {
		writeError(writer, stateauth.ErrInvalidCredential, nil)
		return state.Actor{}, false
	}
	actor, err := handler.auth.Authenticate(request.Context(), token)
	if err != nil {
		writeError(writer, err, nil)
		return state.Actor{}, false
	}
	return actor, true
}

// authenticateKind authenticates the caller and enforces the route's actor
// kinds. The state service repeats every check; the edge gate keeps the
// authorization matrix (docs/agent-execution-implementation-plan.md §2.1)
// readable in one place.
func (handler *Handler) authenticateKind(writer http.ResponseWriter, request *http.Request, kinds ...state.ActorKind) (state.Actor, bool) {
	actor, ok := handler.authenticate(writer, request)
	if !ok {
		return state.Actor{}, false
	}
	for _, kind := range kinds {
		if actor.Kind == kind {
			return actor, true
		}
	}
	writeError(writer, state.ErrForbidden, nil)
	return state.Actor{}, false
}

func bearerToken(request *http.Request) string {
	header := request.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
}

func decodeJSON(request *http.Request, output any) error {
	defer request.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(nil, request.Body, maxRequestBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

// decodeUpdateReminder decodes a reminder PATCH body and restores the explicit
// null that clears the execution policy: encoding/json cannot distinguish an
// absent key from a null literal on the double-pointer field.
func decodeUpdateReminder(request *http.Request, input *state.UpdateReminderInput) error {
	defer request.Body.Close()
	body, err := io.ReadAll(http.MaxBytesReader(nil, request.Body, maxRequestBodyBytes))
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return err
	}
	if value, present := fields["execution_policy_id"]; present && strings.TrimSpace(string(value)) == "null" {
		var cleared *string
		input.ExecutionPolicyID = &cleared
	}
	return nil
}

func writeError(writer http.ResponseWriter, err error, details any) {
	status, code := mapError(err)
	message := code
	if status == http.StatusInternalServerError {
		message = "internal error"
	}
	writeJSON(writer, status, ErrorResponse{Code: code, Message: message, Details: details})
}

func mapError(err error) (int, string) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, state.ErrRevisionConflict), errors.Is(err, stateauth.ErrOwnerExists):
		return http.StatusConflict, "revision_conflict"
	case errors.Is(err, state.ErrNotClaimable):
		return http.StatusConflict, "not_claimable"
	case errors.Is(err, state.ErrRunStateConflict):
		return http.StatusConflict, "run_state_conflict"
	case errors.Is(err, state.ErrPolicyViolation):
		return http.StatusForbidden, "policy_violation"
	case errors.Is(err, state.ErrForbidden):
		return http.StatusForbidden, "forbidden"
	case errors.Is(err, state.ErrInvalidInput):
		return http.StatusBadRequest, "invalid_input"
	case errors.Is(err, stateauth.ErrInvalidCredential), errors.Is(err, stateauth.ErrInvalidBootstrapToken):
		return http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, stateauth.ErrInvalidPairingCode):
		return http.StatusNotFound, "pairing_code_not_found"
	case errors.Is(err, stateauth.ErrPairingCodeExpired):
		return http.StatusGone, "pairing_code_expired"
	case errors.Is(err, stateauth.ErrPairingCodeUsed):
		return http.StatusConflict, "pairing_code_used"
	default:
		return http.StatusInternalServerError, "internal_error"
	}
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(writer, request)
	})
}

func queryInteger(request *http.Request, name string, fallback int) (int, error) {
	value := request.URL.Query().Get(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 || parsed > 500 {
		return 0, state.ErrInvalidInput
	}
	return parsed, nil
}

func queryInteger64(request *http.Request, name string, fallback int64) (int64, error) {
	value := request.URL.Query().Get(name)
	if value == "" {
		return fallback, nil
	}
	return strconv.ParseInt(value, 10, 64)
}
