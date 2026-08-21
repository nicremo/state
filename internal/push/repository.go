package push

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/nicremo/state/internal/state"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

type DeviceRoute struct {
	ActorID       string    `json:"actor_id"`
	RelayURL      string    `json:"relay_url"`
	RouteID       string    `json:"route_id"`
	Authorization string    `json:"-"`
	PublicKey     []byte    `json:"public_key"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type RegisterDeviceInput struct {
	RelayURL      string `json:"relay_url"`
	RouteID       string `json:"route_id"`
	Authorization string `json:"authorization"`
	PublicKey     []byte `json:"public_key"`
}

type Repository struct {
	app    core.App
	vault  cipher.AEAD
	random io.Reader
	clock  func() time.Time
}

type DueOccurrence struct {
	Reminder   state.Reminder
	Occurrence state.Occurrence
	NotifyAt   time.Time
}

func NewRepository(app core.App, encryptionKey []byte) (*Repository, error) {
	if app == nil || len(encryptionKey) != 32 {
		return nil, state.ErrInvalidInput
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("create push vault cipher: %w", err)
	}
	vault, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create push vault: %w", err)
	}
	repository := &Repository{
		app:    app,
		vault:  vault,
		random: rand.Reader,
		clock:  func() time.Time { return time.Now().UTC() },
	}
	if err := repository.ensureSchema(); err != nil {
		return nil, err
	}
	return repository, nil
}

func (repository *Repository) RegisterDevice(ctx context.Context, actor state.Actor, input RegisterDeviceInput) (DeviceRoute, error) {
	if err := ctx.Err(); err != nil {
		return DeviceRoute{}, err
	}
	input.RelayURL = strings.TrimRight(strings.TrimSpace(input.RelayURL), "/")
	if (actor.Kind != state.ActorKindOwner && actor.Kind != state.ActorKindDevice) || actor.ID == "" || !validRelayURL(input.RelayURL) || input.RouteID == "" || input.Authorization == "" || len(input.PublicKey) != 32 {
		return DeviceRoute{}, state.ErrInvalidInput
	}
	ciphertext, err := repository.encryptAuthorization(actor.ID, input.RouteID, input.Authorization)
	if err != nil {
		return DeviceRoute{}, err
	}
	now := repository.clock().UTC()
	_, err = repository.app.DB().NewQuery(`
		INSERT INTO state_device_push (
			actor_id, relay_url, route_id, authorization_cipher, public_key, created_at, updated_at
		) VALUES (
			{:actor_id}, {:relay_url}, {:route_id}, {:authorization_cipher}, {:public_key}, {:created_at}, {:updated_at}
		)
		ON CONFLICT(actor_id) DO UPDATE SET
			relay_url = excluded.relay_url,
			route_id = excluded.route_id,
			authorization_cipher = excluded.authorization_cipher,
			public_key = excluded.public_key,
			updated_at = excluded.updated_at
	`).Bind(dbx.Params{
		"actor_id":             actor.ID,
		"relay_url":            input.RelayURL,
		"route_id":             input.RouteID,
		"authorization_cipher": ciphertext,
		"public_key":           input.PublicKey,
		"created_at":           now.Format(time.RFC3339Nano),
		"updated_at":           now.Format(time.RFC3339Nano),
	}).Execute()
	if err != nil {
		return DeviceRoute{}, fmt.Errorf("register push route: %w", err)
	}
	return DeviceRoute{
		ActorID:   actor.ID,
		RelayURL:  input.RelayURL,
		RouteID:   input.RouteID,
		PublicKey: append([]byte(nil), input.PublicKey...),
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (repository *Repository) ListRoutes(ctx context.Context, excludedActorID string) ([]DeviceRoute, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	where := ""
	params := dbx.Params{}
	if excludedActorID != "" {
		where = "WHERE actor_id != {:excluded_actor_id}"
		params["excluded_actor_id"] = excludedActorID
	}
	return repository.listRoutes(`
		SELECT actor_id, relay_url, route_id, authorization_cipher, public_key, created_at, updated_at
		FROM state_device_push `+where+`
		ORDER BY created_at, actor_id
	`, params)
}

// ListDeviceRoutes returns every registered push route (owner and devices)
// with decrypted relay authorization. Unlike ListUnconfirmedRoutes it is not
// occurrence-scoped: run lifecycle notifications fan out to all devices and
// carry no confirmation tracking.
func (repository *Repository) ListDeviceRoutes(ctx context.Context) ([]DeviceRoute, error) {
	return repository.ListRoutes(ctx, "")
}

func (repository *Repository) ListUnconfirmedRoutes(ctx context.Context, occurrenceID string) ([]DeviceRoute, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if occurrenceID == "" {
		return nil, state.ErrInvalidInput
	}
	return repository.listRoutes(`
		SELECT r.actor_id, r.relay_url, r.route_id, r.authorization_cipher, r.public_key, r.created_at, r.updated_at
		FROM state_device_push r
		WHERE NOT EXISTS (
			SELECT 1 FROM state_push_confirmations c
			WHERE c.actor_id = r.actor_id AND c.occurrence_id = {:occurrence_id}
		)
		AND NOT EXISTS (
			SELECT 1 FROM state_push_deliveries d
			WHERE d.actor_id = r.actor_id AND d.occurrence_id = {:occurrence_id}
		)
		ORDER BY r.created_at, r.actor_id
	`, dbx.Params{"occurrence_id": occurrenceID})
}

func (repository *Repository) ConfirmOccurrences(ctx context.Context, actor state.Actor, occurrenceIDs []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if (actor.Kind != state.ActorKindOwner && actor.Kind != state.ActorKindDevice) || actor.ID == "" || len(occurrenceIDs) == 0 || len(occurrenceIDs) > 500 {
		return state.ErrInvalidInput
	}
	now := repository.clock().UTC().Format(time.RFC3339Nano)
	return repository.app.RunInTransaction(func(txApp core.App) error {
		row := struct {
			Count int `db:"count"`
		}{}
		if err := txApp.DB().NewQuery(`
			SELECT COUNT(*) AS count FROM state_device_push WHERE actor_id = {:actor_id}
		`).Bind(dbx.Params{"actor_id": actor.ID}).One(&row); err != nil {
			return err
		}
		if row.Count != 1 {
			return state.ErrNotFound
		}
		seen := make(map[string]struct{}, len(occurrenceIDs))
		for _, occurrenceID := range occurrenceIDs {
			if occurrenceID == "" {
				return state.ErrInvalidInput
			}
			if _, exists := seen[occurrenceID]; exists {
				continue
			}
			seen[occurrenceID] = struct{}{}
			_, err := txApp.DB().NewQuery(`
				INSERT INTO state_push_confirmations (actor_id, occurrence_id, confirmed_at)
				VALUES ({:actor_id}, {:occurrence_id}, {:confirmed_at})
				ON CONFLICT(actor_id, occurrence_id) DO UPDATE SET confirmed_at = excluded.confirmed_at
			`).Bind(dbx.Params{
				"actor_id":      actor.ID,
				"occurrence_id": occurrenceID,
				"confirmed_at":  now,
			}).Execute()
			if err != nil {
				return fmt.Errorf("confirm occurrence: %w", err)
			}
		}
		return nil
	})
}

func (repository *Repository) MarkDelivered(ctx context.Context, actorID string, occurrenceID string) error {
	if actorID == "" || occurrenceID == "" {
		return state.ErrInvalidInput
	}
	_, err := repository.app.DB().NewQuery(`
		INSERT INTO state_push_deliveries (actor_id, occurrence_id, delivered_at)
		VALUES ({:actor_id}, {:occurrence_id}, {:delivered_at})
		ON CONFLICT(actor_id, occurrence_id) DO NOTHING
	`).Bind(dbx.Params{
		"actor_id":      actorID,
		"occurrence_id": occurrenceID,
		"delivered_at":  repository.clock().UTC().Format(time.RFC3339Nano),
	}).Execute()
	return err
}

func (repository *Repository) ListDueOccurrences(ctx context.Context, from time.Time, through time.Time) ([]DueOccurrence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if through.Before(from) {
		return nil, state.ErrInvalidInput
	}
	rows := []struct {
		ReminderJSON   string `db:"reminder_json"`
		OccurrenceJSON string `db:"occurrence_json"`
	}{}
	err := repository.app.DB().NewQuery(`
		SELECT r.data_json AS reminder_json, o.data_json AS occurrence_json
		FROM state_occurrences o
		JOIN state_reminders r ON r.id = o.reminder_id
		WHERE o.status IN ('pending', 'snoozed') AND r.archived = 0
		ORDER BY o.local_date, o.local_time, o.id
		LIMIT 2000
	`).All(&rows)
	if err != nil {
		return nil, fmt.Errorf("list due occurrences: %w", err)
	}
	result := make([]DueOccurrence, 0)
	for _, row := range rows {
		var reminder state.Reminder
		var occurrence state.Occurrence
		if err := json.Unmarshal([]byte(row.ReminderJSON), &reminder); err != nil {
			return nil, fmt.Errorf("decode due reminder: %w", err)
		}
		if err := json.Unmarshal([]byte(row.OccurrenceJSON), &occurrence); err != nil {
			return nil, fmt.Errorf("decode due occurrence: %w", err)
		}
		var notifyAt time.Time
		if occurrence.Status == state.OccurrenceStatusSnoozed && occurrence.SnoozedUntil != nil {
			notifyAt = occurrence.SnoozedUntil.UTC()
		} else if occurrence.ScheduledAt != nil {
			notifyAt = occurrence.ScheduledAt.Add(-time.Duration(occurrence.PrewarningMinutes) * time.Minute).UTC()
		} else {
			continue
		}
		if notifyAt.Before(from) || notifyAt.After(through) {
			continue
		}
		result = append(result, DueOccurrence{Reminder: reminder, Occurrence: occurrence, NotifyAt: notifyAt})
	}
	return result, nil
}

func (repository *Repository) DeleteDevice(ctx context.Context, actor state.Actor) error {
	if actor.ID == "" || (actor.Kind != state.ActorKindOwner && actor.Kind != state.ActorKindDevice) {
		return state.ErrForbidden
	}
	result, err := repository.app.DB().NewQuery(`DELETE FROM state_device_push WHERE actor_id = {:actor_id}`).Bind(dbx.Params{"actor_id": actor.ID}).Execute()
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
	return nil
}

func (repository *Repository) listRoutes(query string, params dbx.Params) ([]DeviceRoute, error) {
	rows := []struct {
		ActorID             string `db:"actor_id"`
		RelayURL            string `db:"relay_url"`
		RouteID             string `db:"route_id"`
		AuthorizationCipher []byte `db:"authorization_cipher"`
		PublicKey           []byte `db:"public_key"`
		CreatedAt           string `db:"created_at"`
		UpdatedAt           string `db:"updated_at"`
	}{}
	if err := repository.app.DB().NewQuery(query).Bind(params).All(&rows); err != nil {
		return nil, fmt.Errorf("list push routes: %w", err)
	}
	routes := make([]DeviceRoute, 0, len(rows))
	for _, row := range rows {
		authorization, err := repository.decryptAuthorization(row.ActorID, row.RouteID, row.AuthorizationCipher)
		if err != nil {
			return nil, err
		}
		createdAt, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
		if err != nil {
			return nil, err
		}
		updatedAt, err := time.Parse(time.RFC3339Nano, row.UpdatedAt)
		if err != nil {
			return nil, err
		}
		routes = append(routes, DeviceRoute{
			ActorID:       row.ActorID,
			RelayURL:      row.RelayURL,
			RouteID:       row.RouteID,
			Authorization: authorization,
			PublicKey:     append([]byte(nil), row.PublicKey...),
			CreatedAt:     createdAt,
			UpdatedAt:     updatedAt,
		})
	}
	return routes, nil
}

func (repository *Repository) encryptAuthorization(actorID string, routeID string, authorization string) ([]byte, error) {
	nonce := make([]byte, repository.vault.NonceSize())
	if _, err := io.ReadFull(repository.random, nonce); err != nil {
		return nil, err
	}
	ciphertext := repository.vault.Seal(nil, nonce, []byte(authorization), routeAdditionalData(actorID, routeID))
	return append(nonce, ciphertext...), nil
}

func (repository *Repository) decryptAuthorization(actorID string, routeID string, value []byte) (string, error) {
	if len(value) < repository.vault.NonceSize()+repository.vault.Overhead() {
		return "", errors.New("invalid encrypted relay authorization")
	}
	nonce := value[:repository.vault.NonceSize()]
	ciphertext := value[repository.vault.NonceSize():]
	plaintext, err := repository.vault.Open(nil, nonce, ciphertext, routeAdditionalData(actorID, routeID))
	if err != nil {
		return "", errors.New("decrypt relay authorization")
	}
	return string(plaintext), nil
}

func routeAdditionalData(actorID string, routeID string) []byte {
	return []byte(actorID + "\x00" + routeID)
}

func validRelayURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme != "http" {
		return false
	}
	host := parsed.Hostname()
	return strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
}

func (repository *Repository) ensureSchema() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS state_device_push (
			actor_id TEXT PRIMARY KEY REFERENCES state_actors(id) ON DELETE CASCADE,
			relay_url TEXT NOT NULL,
			route_id TEXT NOT NULL UNIQUE,
			authorization_cipher BLOB NOT NULL,
			public_key BLOB NOT NULL CHECK(length(public_key) = 32),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		) STRICT`,
		`CREATE TABLE IF NOT EXISTS state_push_confirmations (
			actor_id TEXT NOT NULL REFERENCES state_device_push(actor_id) ON DELETE CASCADE,
			occurrence_id TEXT NOT NULL REFERENCES state_occurrences(id) ON DELETE CASCADE,
			confirmed_at TEXT NOT NULL,
			PRIMARY KEY(actor_id, occurrence_id)
		) STRICT`,
		`CREATE TABLE IF NOT EXISTS state_push_deliveries (
			actor_id TEXT NOT NULL REFERENCES state_device_push(actor_id) ON DELETE CASCADE,
			occurrence_id TEXT NOT NULL REFERENCES state_occurrences(id) ON DELETE CASCADE,
			delivered_at TEXT NOT NULL,
			PRIMARY KEY(actor_id, occurrence_id)
		) STRICT`,
	}
	return repository.app.RunInTransaction(func(txApp core.App) error {
		for _, statement := range statements {
			if _, err := txApp.DB().NewQuery(statement).Execute(); err != nil {
				return fmt.Errorf("initialize push schema: %w", err)
			}
		}
		return nil
	})
}
