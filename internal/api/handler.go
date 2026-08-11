package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	stateauth "github.com/nicremo/state/internal/auth"
	"github.com/nicremo/state/internal/state"
)

const maxRequestBodyBytes = 1 << 20

type Config struct {
	Auth    *stateauth.Manager
	State   *state.Service
	Version string
}

type Handler struct {
	auth    *stateauth.Manager
	state   *state.Service
	version string
	router  *http.ServeMux
}

type ReminderDetail struct {
	Reminder state.Reminder     `json:"reminder"`
	History  []state.AuditEvent `json:"history"`
}

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func NewHandler(config Config) http.Handler {
	handler := &Handler{
		auth:    config.Auth,
		state:   config.State,
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
	handler.router.HandleFunc("POST /api/v1/reminders", handler.createReminder)
	handler.router.HandleFunc("GET /api/v1/reminders/{id}", handler.getReminder)
	handler.router.HandleFunc("PATCH /api/v1/reminders/{id}", handler.updateReminder)
	handler.router.HandleFunc("GET /api/v1/reminders/{id}/history", handler.getReminderHistory)
}

func (handler *Handler) healthLive(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (handler *Handler) healthReady(writer http.ResponseWriter, _ *http.Request) {
	if handler.auth == nil || handler.state == nil {
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
	writeJSON(writer, http.StatusCreated, reminder)
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
	writeJSON(writer, http.StatusOK, ReminderDetail{Reminder: reminder, History: history})
}

func (handler *Handler) updateReminder(writer http.ResponseWriter, request *http.Request) {
	actor, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	var input state.UpdateReminderInput
	if err := decodeJSON(request, &input); err != nil {
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

func (handler *Handler) authenticate(writer http.ResponseWriter, request *http.Request) (state.Actor, bool) {
	header := request.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		writeError(writer, stateauth.ErrInvalidCredential, nil)
		return state.Actor{}, false
	}
	actor, err := handler.auth.Authenticate(request.Context(), strings.TrimPrefix(header, "Bearer "))
	if err != nil {
		writeError(writer, err, nil)
		return state.Actor{}, false
	}
	return actor, true
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
