package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nicremo/state/internal/state"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

var (
	ErrInvalidBootstrapToken = errors.New("invalid bootstrap token")
	ErrOwnerExists           = errors.New("owner already exists")
	ErrInvalidCredential     = errors.New("invalid credential")
	ErrPairingCodeExpired    = errors.New("pairing code expired")
	ErrPairingCodeUsed       = errors.New("pairing code already used")
	ErrInvalidPairingCode    = errors.New("invalid pairing code")
)

type OwnerBootstrapRequest struct {
	DisplayName string `json:"display_name"`
	DeviceName  string `json:"device_name"`
}

type PairingCodeRequest struct {
	Harness     string `json:"harness"`
	DisplayName string `json:"display_name"`
	DeviceName  string `json:"device_name"`
}

type PairingCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Credential struct {
	Actor state.Actor `json:"actor"`
	Token string      `json:"token"`
}

type Manager struct {
	app            core.App
	bootstrapToken string
	clock          func() time.Time
	newID          func() (string, error)
	newToken       func() (string, error)
	newCode        func() (string, error)
}

type ManagerOption func(*Manager)

func WithClock(clock func() time.Time) ManagerOption {
	return func(manager *Manager) {
		manager.clock = clock
	}
}

func NewManager(app core.App, bootstrapToken string, options ...ManagerOption) (*Manager, error) {
	if app == nil || bootstrapToken == "" {
		return nil, state.ErrInvalidInput
	}
	manager := &Manager{
		app:            app,
		bootstrapToken: bootstrapToken,
		clock:          func() time.Time { return time.Now().UTC() },
		newID: func() (string, error) {
			id, err := uuid.NewV7()
			return id.String(), err
		},
		newToken: secureToken,
		newCode:  securePairingCode,
	}
	for _, option := range options {
		option(manager)
	}
	if err := manager.ensureSchema(); err != nil {
		return nil, fmt.Errorf("initialize auth schema: %w", err)
	}
	return manager, nil
}

func (manager *Manager) BootstrapOwner(ctx context.Context, providedToken string, request OwnerBootstrapRequest) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	if strings.TrimSpace(request.DisplayName) == "" {
		return Credential{}, state.ErrInvalidInput
	}
	if subtle.ConstantTimeCompare([]byte(providedToken), []byte(manager.bootstrapToken)) != 1 {
		return Credential{}, ErrInvalidBootstrapToken
	}
	actorID, err := manager.newID()
	if err != nil {
		return Credential{}, fmt.Errorf("generate owner actor ID: %w", err)
	}
	credentialID, err := manager.newID()
	if err != nil {
		return Credential{}, fmt.Errorf("generate owner credential ID: %w", err)
	}
	token, err := manager.newToken()
	if err != nil {
		return Credential{}, fmt.Errorf("generate owner credential: %w", err)
	}
	now := manager.clock().UTC()
	actor := state.Actor{
		ID:          actorID,
		Kind:        state.ActorKindOwner,
		DisplayName: strings.TrimSpace(request.DisplayName),
		DeviceName:  strings.TrimSpace(request.DeviceName),
	}

	err = manager.app.RunInTransaction(func(txApp core.App) error {
		row := struct {
			Count int `db:"count"`
		}{}
		if err := txApp.DB().NewQuery(`
			SELECT COUNT(*) AS count FROM state_actors WHERE kind = 'owner'
		`).One(&row); err != nil {
			return err
		}
		if row.Count > 0 {
			return ErrOwnerExists
		}
		if err := insertActor(txApp, actor, now); err != nil {
			return err
		}
		return insertCredential(txApp, credentialID, actor.ID, token, now)
	})
	if err != nil {
		return Credential{}, err
	}
	return Credential{Actor: actor, Token: token}, nil
}

func (manager *Manager) Authenticate(ctx context.Context, token string) (state.Actor, error) {
	if err := ctx.Err(); err != nil {
		return state.Actor{}, err
	}
	if token == "" {
		return state.Actor{}, ErrInvalidCredential
	}
	row := struct {
		CredentialID string `db:"credential_id"`
		ActorID      string `db:"actor_id"`
		Kind         string `db:"kind"`
		DisplayName  string `db:"display_name"`
		Harness      string `db:"harness"`
		DeviceName   string `db:"device_name"`
	}{}
	err := manager.app.DB().NewQuery(`
		SELECT c.id AS credential_id, a.id AS actor_id, a.kind, a.display_name, a.harness, a.device_name
		FROM state_credentials c
		JOIN state_actors a ON a.id = c.actor_id
		WHERE c.token_hash = {:token_hash}
			AND c.revoked_at IS NULL
			AND a.revoked_at IS NULL
		LIMIT 1
	`).Bind(dbx.Params{"token_hash": hashSecret(token)}).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.Actor{}, ErrInvalidCredential
	}
	if err != nil {
		return state.Actor{}, fmt.Errorf("authenticate credential: %w", err)
	}
	_, _ = manager.app.DB().NewQuery(`
		UPDATE state_credentials SET last_used_at = {:last_used_at} WHERE id = {:id}
	`).Bind(dbx.Params{
		"id":           row.CredentialID,
		"last_used_at": formatTime(manager.clock()),
	}).Execute()
	return state.Actor{
		ID:          row.ActorID,
		Kind:        state.ActorKind(row.Kind),
		DisplayName: row.DisplayName,
		Harness:     row.Harness,
		DeviceName:  row.DeviceName,
	}, nil
}

func (manager *Manager) CreatePairingCode(ctx context.Context, actor state.Actor, request PairingCodeRequest) (PairingCode, error) {
	if err := ctx.Err(); err != nil {
		return PairingCode{}, err
	}
	if actor.Kind != state.ActorKindOwner {
		return PairingCode{}, state.ErrForbidden
	}
	if !validHarness(request.Harness) || strings.TrimSpace(request.DisplayName) == "" {
		return PairingCode{}, state.ErrInvalidInput
	}
	id, err := manager.newID()
	if err != nil {
		return PairingCode{}, fmt.Errorf("generate pairing code ID: %w", err)
	}
	code, err := manager.newCode()
	if err != nil {
		return PairingCode{}, fmt.Errorf("generate pairing code: %w", err)
	}
	now := manager.clock().UTC()
	expiresAt := now.Add(10 * time.Minute)
	_, err = manager.app.DB().NewQuery(`
		INSERT INTO state_pairing_codes (
			id, code_hash, harness, display_name, device_name, created_by, created_at, expires_at
		) VALUES (
			{:id}, {:code_hash}, {:harness}, {:display_name}, {:device_name}, {:created_by}, {:created_at}, {:expires_at}
		)
	`).Bind(dbx.Params{
		"id":           id,
		"code_hash":    hashSecret(normalizePairingCode(code)),
		"harness":      request.Harness,
		"display_name": strings.TrimSpace(request.DisplayName),
		"device_name":  strings.TrimSpace(request.DeviceName),
		"created_by":   actor.ID,
		"created_at":   formatTime(now),
		"expires_at":   formatTime(expiresAt),
	}).Execute()
	if err != nil {
		return PairingCode{}, fmt.Errorf("insert pairing code: %w", err)
	}
	return PairingCode{Code: code, ExpiresAt: expiresAt}, nil
}

func (manager *Manager) ExchangePairingCode(ctx context.Context, code string) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	code = normalizePairingCode(code)
	if code == "" {
		return Credential{}, ErrInvalidPairingCode
	}
	actorID, err := manager.newID()
	if err != nil {
		return Credential{}, fmt.Errorf("generate harness actor ID: %w", err)
	}
	credentialID, err := manager.newID()
	if err != nil {
		return Credential{}, fmt.Errorf("generate harness credential ID: %w", err)
	}
	token, err := manager.newToken()
	if err != nil {
		return Credential{}, fmt.Errorf("generate harness credential: %w", err)
	}
	var actor state.Actor
	now := manager.clock().UTC()
	err = manager.app.RunInTransaction(func(txApp core.App) error {
		row := struct {
			ID          string  `db:"id"`
			Harness     string  `db:"harness"`
			DisplayName string  `db:"display_name"`
			DeviceName  string  `db:"device_name"`
			ExpiresAt   string  `db:"expires_at"`
			UsedAt      *string `db:"used_at"`
		}{}
		err := txApp.DB().NewQuery(`
			SELECT id, harness, display_name, device_name, expires_at, used_at
			FROM state_pairing_codes
			WHERE code_hash = {:code_hash}
			LIMIT 1
		`).Bind(dbx.Params{"code_hash": hashSecret(code)}).One(&row)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidPairingCode
		}
		if err != nil {
			return err
		}
		if row.UsedAt != nil {
			return ErrPairingCodeUsed
		}
		expiresAt, err := time.Parse(time.RFC3339Nano, row.ExpiresAt)
		if err != nil {
			return fmt.Errorf("parse pairing expiry: %w", err)
		}
		if now.After(expiresAt) {
			return ErrPairingCodeExpired
		}
		actor = state.Actor{
			ID:          actorID,
			Kind:        state.ActorKindHarness,
			DisplayName: row.DisplayName,
			Harness:     row.Harness,
			DeviceName:  row.DeviceName,
		}
		if err := insertActor(txApp, actor, now); err != nil {
			return err
		}
		if err := insertCredential(txApp, credentialID, actor.ID, token, now); err != nil {
			return err
		}
		result, err := txApp.DB().NewQuery(`
			UPDATE state_pairing_codes
			SET used_at = {:used_at}
			WHERE id = {:id} AND used_at IS NULL
		`).Bind(dbx.Params{"used_at": formatTime(now), "id": row.ID}).Execute()
		if err != nil {
			return err
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rowsAffected != 1 {
			return ErrPairingCodeUsed
		}
		return nil
	})
	if err != nil {
		return Credential{}, err
	}
	return Credential{Actor: actor, Token: token}, nil
}

func (manager *Manager) RevokeActor(ctx context.Context, actor state.Actor, targetActorID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if actor.Kind != state.ActorKindOwner || targetActorID == "" || targetActorID == actor.ID {
		return state.ErrForbidden
	}
	now := formatTime(manager.clock())
	return manager.app.RunInTransaction(func(txApp core.App) error {
		result, err := txApp.DB().NewQuery(`
			UPDATE state_actors SET revoked_at = {:revoked_at}
			WHERE id = {:id} AND kind != 'owner' AND revoked_at IS NULL
		`).Bind(dbx.Params{"revoked_at": now, "id": targetActorID}).Execute()
		if err != nil {
			return err
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rowsAffected != 1 {
			return state.ErrNotFound
		}
		_, err = txApp.DB().NewQuery(`
			UPDATE state_credentials SET revoked_at = {:revoked_at}
			WHERE actor_id = {:actor_id} AND revoked_at IS NULL
		`).Bind(dbx.Params{"revoked_at": now, "actor_id": targetActorID}).Execute()
		return err
	})
}

func (manager *Manager) ensureSchema() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS state_actors (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL CHECK(kind IN ('owner', 'device', 'harness', 'system')),
			display_name TEXT NOT NULL,
			harness TEXT NOT NULL DEFAULT '',
			device_name TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			revoked_at TEXT
		) STRICT`,
		`CREATE UNIQUE INDEX IF NOT EXISTS state_single_owner_idx
			ON state_actors(kind) WHERE kind = 'owner'`,
		`CREATE TABLE IF NOT EXISTS state_credentials (
			id TEXT PRIMARY KEY,
			actor_id TEXT NOT NULL REFERENCES state_actors(id),
			token_hash TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL,
			last_used_at TEXT,
			revoked_at TEXT
		) STRICT`,
		`CREATE INDEX IF NOT EXISTS state_credentials_actor_idx
			ON state_credentials(actor_id)`,
		`CREATE TABLE IF NOT EXISTS state_pairing_codes (
			id TEXT PRIMARY KEY,
			code_hash TEXT NOT NULL UNIQUE,
			harness TEXT NOT NULL,
			display_name TEXT NOT NULL,
			device_name TEXT NOT NULL DEFAULT '',
			created_by TEXT NOT NULL REFERENCES state_actors(id),
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			used_at TEXT
		) STRICT`,
	}
	return manager.app.RunInTransaction(func(txApp core.App) error {
		for _, statement := range statements {
			if _, err := txApp.DB().NewQuery(statement).Execute(); err != nil {
				return err
			}
		}
		return nil
	})
}

func insertActor(app core.App, actor state.Actor, createdAt time.Time) error {
	_, err := app.DB().NewQuery(`
		INSERT INTO state_actors (
			id, kind, display_name, harness, device_name, created_at
		) VALUES (
			{:id}, {:kind}, {:display_name}, {:harness}, {:device_name}, {:created_at}
		)
	`).Bind(dbx.Params{
		"id":           actor.ID,
		"kind":         string(actor.Kind),
		"display_name": actor.DisplayName,
		"harness":      actor.Harness,
		"device_name":  actor.DeviceName,
		"created_at":   formatTime(createdAt),
	}).Execute()
	if err != nil {
		return fmt.Errorf("insert actor: %w", err)
	}
	return nil
}

func insertCredential(app core.App, credentialID string, actorID string, token string, createdAt time.Time) error {
	_, err := app.DB().NewQuery(`
		INSERT INTO state_credentials (
			id, actor_id, token_hash, created_at
		) VALUES (
			{:id}, {:actor_id}, {:token_hash}, {:created_at}
		)
	`).Bind(dbx.Params{
		"id":         credentialID,
		"actor_id":   actorID,
		"token_hash": hashSecret(token),
		"created_at": formatTime(createdAt),
	}).Execute()
	if err != nil {
		return fmt.Errorf("insert credential: %w", err)
	}
	return nil
}

func secureToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return "state_" + base64.RawURLEncoding.EncodeToString(buffer), nil
}

func securePairingCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buffer := make([]byte, 10)
	random := make([]byte, len(buffer))
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	for index, value := range random {
		buffer[index] = alphabet[int(value)%len(alphabet)]
	}
	return string(buffer[:5]) + "-" + string(buffer[5:]), nil
}

func hashSecret(secret string) string {
	digest := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(digest[:])
}

func normalizePairingCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
}

func validHarness(harness string) bool {
	switch harness {
	case "codex", "claude-code", "opencode":
		return true
	default:
		return false
	}
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
