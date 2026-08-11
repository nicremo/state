package relay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nicremo/state/internal/pushcrypto"
)

const maxRelayBodyBytes = 16 << 10

var (
	ErrInvalidInput       = errors.New("invalid input")
	ErrInvalidAttestation = errors.New("invalid attestation")
	ErrInvalidCapability  = errors.New("invalid route capability")
	ErrRouteNotFound      = errors.New("route not found")
	ErrRateLimited        = errors.New("rate limited")
)

type Environment string

const (
	EnvironmentSandbox    Environment = "sandbox"
	EnvironmentProduction Environment = "production"
)

type PushType string

const (
	PushTypeAlert      PushType = "alert"
	PushTypeBackground PushType = "background"
)

type Challenge struct {
	Value     string    `json:"challenge"`
	ExpiresAt time.Time `json:"expires_at"`
}

type AttestationProof struct {
	KeyID     string `json:"key_id"`
	Object    string `json:"object"`
	Challenge string `json:"challenge"`
	Assertion string `json:"assertion"`
}

type AttestationInput struct {
	AttestationProof
	APNSTokenHash string      `json:"apns_token_hash"`
	Environment   Environment `json:"environment"`
}

type Route struct {
	ID             string
	APNSToken      string
	Environment    Environment
	CapabilityHash []byte
	AttestationKey string
	CreatedAt      time.Time
}

type RegistrationResponse struct {
	RouteID       string `json:"route_id"`
	Authorization string `json:"authorization"`
}

type Notification struct {
	APNSToken   string
	Environment Environment
	PushType    PushType
	CollapseID  string
	Payload     []byte
}

type Repository interface {
	CreateChallenge(context.Context, Challenge) error
	ConsumeChallenge(context.Context, string, time.Time) error
	CreateRoute(context.Context, Route) error
	GetRoute(context.Context, string) (Route, error)
	UpdateRouteToken(context.Context, string, string) error
	DeleteRoute(context.Context, string) error
}

type Attestor interface {
	Verify(context.Context, AttestationInput) error
}

type Dispatcher interface {
	Send(context.Context, Notification) error
}

type Limiter interface {
	Allow(string) bool
}

type AllowAllLimiter struct{}

func (AllowAllLimiter) Allow(string) bool { return true }

type Config struct {
	Repository   Repository
	Attestor     Attestor
	Dispatcher   Dispatcher
	Limiter      Limiter
	Version      string
	Clock        func() time.Time
	NewID        func() (string, error)
	NewSecret    func() (string, error)
	NewChallenge func() (string, error)
}

type Handler struct {
	repository   Repository
	attestor     Attestor
	dispatcher   Dispatcher
	limiter      Limiter
	version      string
	clock        func() time.Time
	newID        func() (string, error)
	newSecret    func() (string, error)
	newChallenge func() (string, error)
	router       *http.ServeMux
}

func NewHandler(config Config) http.Handler {
	if config.Clock == nil {
		config.Clock = func() time.Time { return time.Now().UTC() }
	}
	if config.NewID == nil {
		config.NewID = func() (string, error) {
			id, err := uuid.NewV7()
			return id.String(), err
		}
	}
	if config.NewSecret == nil {
		config.NewSecret = secureRandomValue
	}
	if config.NewChallenge == nil {
		config.NewChallenge = secureRandomValue
	}
	if config.Limiter == nil {
		config.Limiter = AllowAllLimiter{}
	}
	handler := &Handler{
		repository:   config.Repository,
		attestor:     config.Attestor,
		dispatcher:   config.Dispatcher,
		limiter:      config.Limiter,
		version:      config.Version,
		clock:        config.Clock,
		newID:        config.NewID,
		newSecret:    config.NewSecret,
		newChallenge: config.NewChallenge,
		router:       http.NewServeMux(),
	}
	handler.router.HandleFunc("GET /health/live", handler.healthLive)
	handler.router.HandleFunc("GET /health/ready", handler.healthReady)
	handler.router.HandleFunc("GET /version", handler.getVersion)
	handler.router.HandleFunc("POST /v1/attest/challenges", handler.createChallenge)
	handler.router.HandleFunc("POST /v1/routes", handler.registerRoute)
	handler.router.HandleFunc("PATCH /v1/routes/{id}", handler.updateRoute)
	handler.router.HandleFunc("DELETE /v1/routes/{id}", handler.deleteRoute)
	handler.router.HandleFunc("POST /v1/routes/{id}/notifications", handler.sendNotification)
	return relaySecurityHeaders(handler.router)
}

func (handler *Handler) healthLive(writer http.ResponseWriter, _ *http.Request) {
	writeRelayJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (handler *Handler) healthReady(writer http.ResponseWriter, _ *http.Request) {
	if handler.repository == nil || handler.attestor == nil || handler.dispatcher == nil {
		writeRelayJSON(writer, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeRelayJSON(writer, http.StatusOK, map[string]string{"status": "ready"})
}

func (handler *Handler) getVersion(writer http.ResponseWriter, _ *http.Request) {
	writeRelayJSON(writer, http.StatusOK, map[string]string{"name": "state-relay", "version": handler.version})
}

func (handler *Handler) createChallenge(writer http.ResponseWriter, request *http.Request) {
	if !handler.limiter.Allow("challenge:" + request.RemoteAddr) {
		writeRelayError(writer, ErrRateLimited)
		return
	}
	value, err := handler.newChallenge()
	if err != nil {
		writeRelayError(writer, err)
		return
	}
	challenge := Challenge{Value: value, ExpiresAt: handler.clock().UTC().Add(5 * time.Minute)}
	if err := handler.repository.CreateChallenge(request.Context(), challenge); err != nil {
		writeRelayError(writer, err)
		return
	}
	writeRelayJSON(writer, http.StatusCreated, challenge)
}

func (handler *Handler) registerRoute(writer http.ResponseWriter, request *http.Request) {
	if !handler.limiter.Allow("register:" + request.RemoteAddr) {
		writeRelayError(writer, ErrRateLimited)
		return
	}
	input := struct {
		APNSToken   string           `json:"apns_token"`
		Environment Environment      `json:"environment"`
		Attestation AttestationProof `json:"attestation"`
	}{}
	if err := decodeRelayJSON(writer, request, &input); err != nil || !validAPNSToken(input.APNSToken) || !validEnvironment(input.Environment) || !validAttestation(input.Attestation) {
		writeRelayError(writer, ErrInvalidInput)
		return
	}
	now := handler.clock().UTC()
	if err := handler.repository.ConsumeChallenge(request.Context(), input.Attestation.Challenge, now); err != nil {
		writeRelayError(writer, ErrInvalidAttestation)
		return
	}
	tokenDigest := sha256.Sum256([]byte(strings.ToLower(input.APNSToken)))
	attestationInput := AttestationInput{
		AttestationProof: input.Attestation,
		APNSTokenHash:    hex.EncodeToString(tokenDigest[:]),
		Environment:      input.Environment,
	}
	if err := handler.attestor.Verify(request.Context(), attestationInput); err != nil {
		writeRelayError(writer, ErrInvalidAttestation)
		return
	}
	routeID, err := handler.newID()
	if err != nil {
		writeRelayError(writer, err)
		return
	}
	capability, err := handler.newSecret()
	if err != nil {
		writeRelayError(writer, err)
		return
	}
	route := Route{
		ID:             routeID,
		APNSToken:      strings.ToLower(input.APNSToken),
		Environment:    input.Environment,
		CapabilityHash: hashCapability(capability),
		AttestationKey: input.Attestation.KeyID,
		CreatedAt:      now,
	}
	if err := handler.repository.CreateRoute(request.Context(), route); err != nil {
		writeRelayError(writer, err)
		return
	}
	writeRelayJSON(writer, http.StatusCreated, RegistrationResponse{RouteID: route.ID, Authorization: capability})
}

func (handler *Handler) updateRoute(writer http.ResponseWriter, request *http.Request) {
	route, ok := handler.authenticateRoute(writer, request)
	if !ok {
		return
	}
	input := struct {
		APNSToken string `json:"apns_token"`
	}{}
	if err := decodeRelayJSON(writer, request, &input); err != nil || !validAPNSToken(input.APNSToken) {
		writeRelayError(writer, ErrInvalidInput)
		return
	}
	if err := handler.repository.UpdateRouteToken(request.Context(), route.ID, strings.ToLower(input.APNSToken)); err != nil {
		writeRelayError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) deleteRoute(writer http.ResponseWriter, request *http.Request) {
	route, ok := handler.authenticateRoute(writer, request)
	if !ok {
		return
	}
	if err := handler.repository.DeleteRoute(request.Context(), route.ID); err != nil {
		writeRelayError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) sendNotification(writer http.ResponseWriter, request *http.Request) {
	route, ok := handler.authenticateRoute(writer, request)
	if !ok {
		return
	}
	if !handler.limiter.Allow("route:" + route.ID) {
		writeRelayError(writer, ErrRateLimited)
		return
	}
	input := struct {
		Kind       string              `json:"kind"`
		CollapseID string              `json:"collapse_id"`
		Envelope   pushcrypto.Envelope `json:"envelope"`
	}{}
	if err := decodeRelayJSON(writer, request, &input); err != nil || !validNotification(input.Kind, input.CollapseID, input.Envelope) {
		writeRelayError(writer, ErrInvalidInput)
		return
	}
	payload, pushType, err := notificationPayload(route.ID, input.Kind, input.Envelope)
	if err != nil {
		writeRelayError(writer, err)
		return
	}
	if len(payload) > 4096 {
		writeRelayError(writer, ErrInvalidInput)
		return
	}
	err = handler.dispatcher.Send(request.Context(), Notification{
		APNSToken:   route.APNSToken,
		Environment: route.Environment,
		PushType:    pushType,
		CollapseID:  input.CollapseID,
		Payload:     payload,
	})
	if err != nil {
		writeRelayError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusAccepted)
}

func (handler *Handler) authenticateRoute(writer http.ResponseWriter, request *http.Request) (Route, bool) {
	route, err := handler.repository.GetRoute(request.Context(), request.PathValue("id"))
	if err != nil {
		writeRelayError(writer, ErrInvalidCapability)
		return Route{}, false
	}
	header := request.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		writeRelayError(writer, ErrInvalidCapability)
		return Route{}, false
	}
	providedHash := hashCapability(strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
	if len(route.CapabilityHash) != len(providedHash) || subtle.ConstantTimeCompare(route.CapabilityHash, providedHash) != 1 {
		writeRelayError(writer, ErrInvalidCapability)
		return Route{}, false
	}
	return route, true
}

func notificationPayload(routeID string, kind string, envelope pushcrypto.Envelope) ([]byte, PushType, error) {
	aps := map[string]any{"content-available": 1}
	pushType := PushTypeBackground
	if kind == "reminder" {
		pushType = PushTypeAlert
		aps["mutable-content"] = 1
		aps["sound"] = "default"
		aps["category"] = "STATE_REMINDER"
		aps["alert"] = map[string]string{
			"title": "State",
			"body":  "New reminder",
		}
	}
	payload, err := json.Marshal(map[string]any{
		"aps": aps,
		"state": map[string]any{
			"route_id": routeID,
			"envelope": envelope,
			"fallback": map[string]string{
				"de": "Neue Erinnerung",
				"en": "New reminder",
			},
		},
	})
	return payload, pushType, err
}

func decodeRelayJSON(writer http.ResponseWriter, request *http.Request, output any) error {
	defer request.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maxRelayBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidInput
	}
	return nil
}

func validAPNSToken(token string) bool {
	if len(token) != 64 {
		return false
	}
	_, err := hex.DecodeString(token)
	return err == nil
}

func validEnvironment(environment Environment) bool {
	return environment == EnvironmentSandbox || environment == EnvironmentProduction
}

func validAttestation(proof AttestationProof) bool {
	return proof.KeyID != "" && proof.Object != "" && proof.Challenge != "" && proof.Assertion != ""
}

func validNotification(kind string, collapseID string, envelope pushcrypto.Envelope) bool {
	if kind != "sync" && kind != "reminder" {
		return false
	}
	if len(collapseID) > 64 || envelope.Version != 1 {
		return false
	}
	return len(envelope.EphemeralPublicKey) == 32 && len(envelope.Nonce) == 12 && len(envelope.Ciphertext) > 0 && len(envelope.Ciphertext) <= 3000
}

func hashCapability(capability string) []byte {
	digest := sha256.Sum256([]byte(capability))
	return digest[:]
}

func secureRandomValue() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func writeRelayError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	switch {
	case errors.Is(err, ErrInvalidInput):
		status, code = http.StatusBadRequest, "invalid_input"
	case errors.Is(err, ErrInvalidAttestation), errors.Is(err, ErrInvalidCapability):
		status, code = http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, ErrRouteNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, ErrRateLimited):
		status, code = http.StatusTooManyRequests, "rate_limited"
	}
	writeRelayJSON(writer, status, map[string]string{"code": code})
}

func writeRelayJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func relaySecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(writer, request)
	})
}

func validateConfig(config Config) error {
	if config.Repository == nil || config.Attestor == nil || config.Dispatcher == nil {
		return fmt.Errorf("relay dependencies are incomplete")
	}
	return nil
}
