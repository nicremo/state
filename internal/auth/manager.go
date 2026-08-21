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

// The table DDL below is shared between the create-if-not-exists path in
// ensureSchema and the guarded rebuild migration (migrateActorKindRunner), so
// both always produce the same shape. SQLite cannot widen a CHECK constraint
// in place; the rebuild is how legacy databases pick up new actor kinds.
const createStateActorsTable = `CREATE TABLE IF NOT EXISTS state_actors (
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL CHECK(kind IN ('owner', 'device', 'harness', 'system', 'runner')),
	display_name TEXT NOT NULL,
	harness TEXT NOT NULL DEFAULT '',
	device_name TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	revoked_at TEXT
) STRICT`

const createStateSingleOwnerIndex = `CREATE UNIQUE INDEX IF NOT EXISTS state_single_owner_idx
	ON state_actors(kind) WHERE kind = 'owner'`

const createStatePairingCodesTable = `CREATE TABLE IF NOT EXISTS state_pairing_codes (
	id TEXT PRIMARY KEY,
	code_hash TEXT NOT NULL UNIQUE,
	actor_kind TEXT NOT NULL DEFAULT 'harness' CHECK(actor_kind IN ('device', 'harness', 'runner')),
	harness TEXT NOT NULL,
	display_name TEXT NOT NULL,
	device_name TEXT NOT NULL DEFAULT '',
	created_by TEXT NOT NULL REFERENCES state_actors(id),
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	used_at TEXT
) STRICT`

type OwnerBootstrapRequest struct {
	DisplayName string `json:"display_name"`
	DeviceName  string `json:"device_name"`
}

type PairingCodeRequest struct {
	Kind        state.ActorKind `json:"kind,omitempty"`
	Harness     string          `json:"harness"`
	DisplayName string          `json:"display_name"`
	DeviceName  string          `json:"device_name"`
}

type PairingCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Credential struct {
	Actor state.Actor `json:"actor"`
	Token string      `json:"token"`
}

type ActorRecord struct {
	Actor      state.Actor `json:"actor"`
	CreatedAt  time.Time   `json:"created_at"`
	LastUsedAt *time.Time  `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time  `json:"revoked_at,omitempty"`
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

func (manager *Manager) RotateCredential(ctx context.Context, currentToken string) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	credentialID, err := manager.newID()
	if err != nil {
		return Credential{}, fmt.Errorf("generate credential ID: %w", err)
	}
	newToken, err := manager.newToken()
	if err != nil {
		return Credential{}, fmt.Errorf("generate credential: %w", err)
	}
	now := manager.clock().UTC()
	var actor state.Actor
	err = manager.app.RunInTransaction(func(txApp core.App) error {
		currentCredentialID, currentActor, err := findCredential(txApp, currentToken)
		if err != nil {
			return err
		}
		actor = currentActor
		result, err := txApp.DB().NewQuery(`
			UPDATE state_credentials SET revoked_at = {:revoked_at}
			WHERE id = {:id} AND revoked_at IS NULL
		`).Bind(dbx.Params{"revoked_at": formatTime(now), "id": currentCredentialID}).Execute()
		if err != nil {
			return err
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rowsAffected != 1 {
			return ErrInvalidCredential
		}
		return insertCredential(txApp, credentialID, actor.ID, newToken, now)
	})
	if err != nil {
		return Credential{}, err
	}
	return Credential{Actor: actor, Token: newToken}, nil
}

func (manager *Manager) RevokeCredential(ctx context.Context, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if token == "" {
		return ErrInvalidCredential
	}
	result, err := manager.app.DB().NewQuery(`
		UPDATE state_credentials SET revoked_at = {:revoked_at}
		WHERE token_hash = {:token_hash} AND revoked_at IS NULL
	`).Bind(dbx.Params{
		"revoked_at": formatTime(manager.clock()),
		"token_hash": hashSecret(token),
	}).Execute()
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected != 1 {
		return ErrInvalidCredential
	}
	return nil
}

func (manager *Manager) ListActors(ctx context.Context, actor state.Actor, kind state.ActorKind) ([]ActorRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if actor.Kind != state.ActorKindOwner {
		return nil, state.ErrForbidden
	}
	if kind != state.ActorKindDevice && kind != state.ActorKindHarness && kind != state.ActorKindRunner {
		return nil, state.ErrInvalidInput
	}
	where := "a.kind = 'harness'"
	switch kind {
	case state.ActorKindDevice:
		where = "a.kind IN ('owner', 'device')"
	case state.ActorKindRunner:
		where = "a.kind = 'runner'"
	}
	rows := []struct {
		ID          string  `db:"id"`
		Kind        string  `db:"kind"`
		DisplayName string  `db:"display_name"`
		Harness     string  `db:"harness"`
		DeviceName  string  `db:"device_name"`
		CreatedAt   string  `db:"created_at"`
		LastUsedAt  *string `db:"last_used_at"`
		RevokedAt   *string `db:"revoked_at"`
	}{}
	query := fmt.Sprintf(`
		SELECT a.id, a.kind, a.display_name, a.harness, a.device_name, a.created_at,
			MAX(c.last_used_at) AS last_used_at, a.revoked_at
		FROM state_actors a
		LEFT JOIN state_credentials c ON c.actor_id = a.id
		WHERE %s
		GROUP BY a.id, a.kind, a.display_name, a.harness, a.device_name, a.created_at, a.revoked_at
		ORDER BY CASE WHEN a.kind = 'owner' THEN 0 ELSE 1 END, a.created_at, a.id
	`, where)
	if err := manager.app.DB().NewQuery(query).All(&rows); err != nil {
		return nil, fmt.Errorf("list actors: %w", err)
	}
	records := make([]ActorRecord, 0, len(rows))
	for _, row := range rows {
		createdAt, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse actor creation time: %w", err)
		}
		lastUsedAt, err := parseOptionalTime(row.LastUsedAt)
		if err != nil {
			return nil, fmt.Errorf("parse actor last used time: %w", err)
		}
		revokedAt, err := parseOptionalTime(row.RevokedAt)
		if err != nil {
			return nil, fmt.Errorf("parse actor revocation time: %w", err)
		}
		records = append(records, ActorRecord{
			Actor: state.Actor{
				ID:          row.ID,
				Kind:        state.ActorKind(row.Kind),
				DisplayName: row.DisplayName,
				Harness:     row.Harness,
				DeviceName:  row.DeviceName,
			},
			CreatedAt:  createdAt,
			LastUsedAt: lastUsedAt,
			RevokedAt:  revokedAt,
		})
	}
	return records, nil
}

func (manager *Manager) CreatePairingCode(ctx context.Context, actor state.Actor, request PairingCodeRequest) (PairingCode, error) {
	if err := ctx.Err(); err != nil {
		return PairingCode{}, err
	}
	if actor.Kind != state.ActorKindOwner {
		return PairingCode{}, state.ErrForbidden
	}
	actorKind := request.Kind
	if actorKind == "" && request.Harness != "" {
		actorKind = state.ActorKindHarness
	}
	validDevice := actorKind == state.ActorKindDevice && request.Harness == "" && strings.TrimSpace(request.DeviceName) != ""
	validAgent := actorKind == state.ActorKindHarness && state.ValidHarness(request.Harness)
	// Runners carry neither a harness label nor a device name.
	validRunner := actorKind == state.ActorKindRunner && request.Harness == "" && strings.TrimSpace(request.DeviceName) == ""
	if (!validDevice && !validAgent && !validRunner) || strings.TrimSpace(request.DisplayName) == "" {
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
			id, code_hash, actor_kind, harness, display_name, device_name, created_by, created_at, expires_at
		) VALUES (
			{:id}, {:code_hash}, {:actor_kind}, {:harness}, {:display_name}, {:device_name}, {:created_by}, {:created_at}, {:expires_at}
		)
	`).Bind(dbx.Params{
		"id":           id,
		"code_hash":    hashSecret(normalizePairingCode(code)),
		"actor_kind":   string(actorKind),
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
			ActorKind   string  `db:"actor_kind"`
			Harness     string  `db:"harness"`
			DisplayName string  `db:"display_name"`
			DeviceName  string  `db:"device_name"`
			ExpiresAt   string  `db:"expires_at"`
			UsedAt      *string `db:"used_at"`
		}{}
		err := txApp.DB().NewQuery(`
			SELECT id, actor_kind, harness, display_name, device_name, expires_at, used_at
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
			Kind:        state.ActorKind(row.ActorKind),
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
		createStateActorsTable,
		createStateSingleOwnerIndex,
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
		createStatePairingCodesTable,
	}
	err := manager.app.RunInTransaction(func(txApp core.App) error {
		for _, statement := range statements {
			if _, err := txApp.DB().NewQuery(statement).Execute(); err != nil {
				return err
			}
		}
		columns := []struct {
			Name string `db:"name"`
		}{}
		if err := txApp.DB().NewQuery(`PRAGMA table_info(state_pairing_codes)`).All(&columns); err != nil {
			return err
		}
		hasActorKind := false
		for _, column := range columns {
			if column.Name == "actor_kind" {
				hasActorKind = true
				break
			}
		}
		if !hasActorKind {
			if _, err := txApp.DB().NewQuery(`
				ALTER TABLE state_pairing_codes
				ADD COLUMN actor_kind TEXT NOT NULL DEFAULT 'harness' CHECK(actor_kind IN ('device', 'harness'))
			`).Execute(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Runs after the column-add migration above so even the oldest pairing
	// codes tables carry every column the rebuild's INSERT ... SELECT expects.
	return manager.migrateActorKindRunner()
}

// migrateActorKindRunner widens the kind CHECK constraints on state_actors and
// state_pairing_codes so they admit runner actors. SQLite cannot alter a CHECK
// constraint in place, so legacy databases get a guarded table rebuild:
// create the replacement under a temporary name, copy all rows, drop the old
// table, rename the replacement, and recreate indexes. Fresh databases already
// carry the widened DDL and skip the rebuild entirely via the sqlite_master
// detection.
func (manager *Manager) migrateActorKindRunner() error {
	rebuildActors, err := manager.tableCheckLacksRunner("state_actors")
	if err != nil {
		return err
	}
	rebuildPairingCodes, err := manager.tableCheckLacksRunner("state_pairing_codes")
	if err != nil {
		return err
	}
	if !rebuildActors && !rebuildPairingCodes {
		return nil
	}
	ctx := context.Background()
	// Dropping a table while live rows in state_credentials /
	// state_pairing_codes / state_runners reference it fails under enforced
	// foreign keys, and PRAGMA foreign_keys is a no-op inside a transaction —
	// so the rebuild runs on one dedicated connection with enforcement paused
	// for exactly the duration of the transaction.
	builder, ok := manager.app.ConcurrentDB().(*dbx.DB)
	if !ok {
		return fmt.Errorf("migrate actor kind runner: unexpected database handle %T", manager.app.ConcurrentDB())
	}
	conn, err := builder.DB().Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("pause foreign key enforcement: %w", err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin actor kind migration: %w", err)
	}
	if rebuildActors {
		err = rebuildKindTable(ctx, tx, "state_actors", createStateActorsTable,
			"id, kind, display_name, harness, device_name, created_at, revoked_at",
			[]string{createStateSingleOwnerIndex})
	}
	if err == nil && rebuildPairingCodes {
		err = rebuildKindTable(ctx, tx, "state_pairing_codes", createStatePairingCodesTable,
			"id, code_hash, actor_kind, harness, display_name, device_name, created_by, created_at, expires_at, used_at",
			nil)
	}
	if err != nil {
		_ = tx.Rollback()
	} else {
		err = tx.Commit()
	}
	if _, resumeErr := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err == nil && resumeErr != nil {
		err = fmt.Errorf("resume foreign key enforcement: %w", resumeErr)
	}
	if err != nil {
		return fmt.Errorf("migrate actor kind runner: %w", err)
	}
	// Safety net: the rebuild must not leave dangling references behind.
	for _, table := range []string{"state_credentials", "state_pairing_codes"} {
		rows, err := conn.QueryContext(ctx, `PRAGMA foreign_key_check(`+table+`)`)
		if err != nil {
			return fmt.Errorf("verify %s references after actor kind migration: %w", table, err)
		}
		violation := rows.Next()
		closeErr := rows.Close()
		if violation {
			return fmt.Errorf("actor kind migration left foreign key violations in %s", table)
		}
		if closeErr != nil {
			return fmt.Errorf("verify %s references after actor kind migration: %w", table, closeErr)
		}
	}
	return nil
}

// tableCheckLacksRunner reports whether the table's stored DDL predates the
// runner actor kind, i.e. its kind CHECK constraint needs widening.
func (manager *Manager) tableCheckLacksRunner(table string) (bool, error) {
	row := struct {
		SQL string `db:"sql"`
	}{}
	err := manager.app.DB().NewQuery(`
		SELECT sql FROM sqlite_master WHERE type = 'table' AND name = {:table}
	`).Bind(dbx.Params{"table": table}).One(&row)
	if err != nil {
		return false, fmt.Errorf("read %s schema: %w", table, err)
	}
	return !strings.Contains(row.SQL, "'runner'"), nil
}

// rebuildKindTable replaces table with a fresh copy built from createDDL,
// carrying the listed columns over and recreating the given indexes. The
// replacement is created under a temporary name first: renaming the old table
// would rewrite REFERENCES clauses in other tables to the temporary name (and
// break them when the old table is dropped), while renaming the temporary
// table to the original name leaves those references untouched. Table and
// column names are fixed schema constants, never user input.
func rebuildKindTable(ctx context.Context, tx *sql.Tx, table string, createDDL string, columns string, indexes []string) error {
	tmpTable := table + "_new"
	tmpDDL := strings.Replace(createDDL, "CREATE TABLE IF NOT EXISTS "+table+" (", "CREATE TABLE "+tmpTable+" (", 1)
	if tmpDDL == createDDL {
		return fmt.Errorf("rebuild %s: shared DDL does not match the table name", table)
	}
	statements := []string{
		tmpDDL,
		`INSERT INTO ` + tmpTable + ` (` + columns + `) SELECT ` + columns + ` FROM ` + table,
		`DROP TABLE ` + table,
		`ALTER TABLE ` + tmpTable + ` RENAME TO ` + table,
	}
	statements = append(statements, indexes...)
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("rebuild %s: %w", table, err)
		}
	}
	return nil
}

func findCredential(app core.App, token string) (string, state.Actor, error) {
	if token == "" {
		return "", state.Actor{}, ErrInvalidCredential
	}
	row := struct {
		CredentialID string `db:"credential_id"`
		ActorID      string `db:"actor_id"`
		Kind         string `db:"kind"`
		DisplayName  string `db:"display_name"`
		Harness      string `db:"harness"`
		DeviceName   string `db:"device_name"`
	}{}
	err := app.DB().NewQuery(`
		SELECT c.id AS credential_id, a.id AS actor_id, a.kind, a.display_name, a.harness, a.device_name
		FROM state_credentials c
		JOIN state_actors a ON a.id = c.actor_id
		WHERE c.token_hash = {:token_hash}
			AND c.revoked_at IS NULL
			AND a.revoked_at IS NULL
		LIMIT 1
	`).Bind(dbx.Params{"token_hash": hashSecret(token)}).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return "", state.Actor{}, ErrInvalidCredential
	}
	if err != nil {
		return "", state.Actor{}, fmt.Errorf("find credential: %w", err)
	}
	return row.CredentialID, state.Actor{
		ID:          row.ActorID,
		Kind:        state.ActorKind(row.Kind),
		DisplayName: row.DisplayName,
		Harness:     row.Harness,
		DeviceName:  row.DeviceName,
	}, nil
}

func parseOptionalTime(value *string) (*time.Time, error) {
	if value == nil || *value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, *value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
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

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
