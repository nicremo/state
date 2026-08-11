package relay

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteRepository struct {
	database *sql.DB
	vault    cipher.AEAD
	random   io.Reader
}

func OpenSQLiteRepository(path string, encryptionKey []byte) (*SQLiteRepository, error) {
	if path == "" || len(encryptionKey) != 32 {
		return nil, ErrInvalidInput
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("create relay vault cipher: %w", err)
	}
	vault, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create relay vault: %w", err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open relay database: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	repository := &SQLiteRepository{database: database, vault: vault, random: rand.Reader}
	if err := repository.initialize(); err != nil {
		_ = database.Close()
		return nil, err
	}
	return repository, nil
}

func (repository *SQLiteRepository) initialize() error {
	statements := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = FULL`,
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS relay_challenges (
			value_hash BLOB PRIMARY KEY,
			expires_at INTEGER NOT NULL
		) STRICT`,
		`CREATE TABLE IF NOT EXISTS relay_routes (
			id TEXT PRIMARY KEY,
			apns_token_cipher BLOB NOT NULL,
			environment TEXT NOT NULL CHECK(environment IN ('sandbox', 'production')),
			capability_hash BLOB NOT NULL,
			attestation_key TEXT NOT NULL,
			created_at INTEGER NOT NULL
		) STRICT`,
	}
	for _, statement := range statements {
		if _, err := repository.database.Exec(statement); err != nil {
			return fmt.Errorf("initialize relay database: %w", err)
		}
	}
	return nil
}

func (repository *SQLiteRepository) Close() error {
	if repository == nil || repository.database == nil {
		return nil
	}
	return repository.database.Close()
}

func (repository *SQLiteRepository) CreateChallenge(ctx context.Context, challenge Challenge) error {
	if challenge.Value == "" || challenge.ExpiresAt.IsZero() {
		return ErrInvalidInput
	}
	_, err := repository.database.ExecContext(ctx, `
		INSERT INTO relay_challenges (value_hash, expires_at) VALUES (?, ?)
	`, digestString(challenge.Value), challenge.ExpiresAt.UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("create attestation challenge: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) ConsumeChallenge(ctx context.Context, value string, now time.Time) error {
	if value == "" {
		return ErrInvalidAttestation
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, `
		DELETE FROM relay_challenges WHERE value_hash = ? AND expires_at >= ?
	`, digestString(value), now.UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("consume attestation challenge: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected != 1 {
		return ErrInvalidAttestation
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	return nil
}

func (repository *SQLiteRepository) CreateRoute(ctx context.Context, route Route) error {
	if route.ID == "" || !validAPNSToken(route.APNSToken) || !validEnvironment(route.Environment) || len(route.CapabilityHash) != sha256.Size || route.AttestationKey == "" {
		return ErrInvalidInput
	}
	tokenCipher, err := repository.encryptToken(route.ID, route.APNSToken)
	if err != nil {
		return err
	}
	_, err = repository.database.ExecContext(ctx, `
		INSERT INTO relay_routes (
			id, apns_token_cipher, environment, capability_hash, attestation_key, created_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`, route.ID, tokenCipher, string(route.Environment), route.CapabilityHash, route.AttestationKey, route.CreatedAt.UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("create relay route: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) GetRoute(ctx context.Context, id string) (Route, error) {
	row := struct {
		TokenCipher    []byte
		Environment    string
		CapabilityHash []byte
		AttestationKey string
		CreatedAt      int64
	}{}
	err := repository.database.QueryRowContext(ctx, `
		SELECT apns_token_cipher, environment, capability_hash, attestation_key, created_at
		FROM relay_routes WHERE id = ?
	`, id).Scan(&row.TokenCipher, &row.Environment, &row.CapabilityHash, &row.AttestationKey, &row.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Route{}, ErrRouteNotFound
	}
	if err != nil {
		return Route{}, fmt.Errorf("get relay route: %w", err)
	}
	token, err := repository.decryptToken(id, row.TokenCipher)
	if err != nil {
		return Route{}, err
	}
	return Route{
		ID:             id,
		APNSToken:      token,
		Environment:    Environment(row.Environment),
		CapabilityHash: row.CapabilityHash,
		AttestationKey: row.AttestationKey,
		CreatedAt:      time.Unix(0, row.CreatedAt).UTC(),
	}, nil
}

func (repository *SQLiteRepository) UpdateRouteToken(ctx context.Context, id string, token string) error {
	if !validAPNSToken(token) {
		return ErrInvalidInput
	}
	tokenCipher, err := repository.encryptToken(id, token)
	if err != nil {
		return err
	}
	result, err := repository.database.ExecContext(ctx, `
		UPDATE relay_routes SET apns_token_cipher = ? WHERE id = ?
	`, tokenCipher, id)
	if err != nil {
		return fmt.Errorf("update relay route: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected != 1 {
		return ErrRouteNotFound
	}
	return nil
}

func (repository *SQLiteRepository) DeleteRoute(ctx context.Context, id string) error {
	result, err := repository.database.ExecContext(ctx, `DELETE FROM relay_routes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete relay route: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected != 1 {
		return ErrRouteNotFound
	}
	return nil
}

func (repository *SQLiteRepository) encryptToken(routeID string, token string) ([]byte, error) {
	nonce := make([]byte, repository.vault.NonceSize())
	if _, err := io.ReadFull(repository.random, nonce); err != nil {
		return nil, fmt.Errorf("generate relay token nonce: %w", err)
	}
	ciphertext := repository.vault.Seal(nil, nonce, []byte(token), []byte(routeID))
	return append(nonce, ciphertext...), nil
}

func (repository *SQLiteRepository) decryptToken(routeID string, value []byte) (string, error) {
	if len(value) < repository.vault.NonceSize()+repository.vault.Overhead() {
		return "", errors.New("invalid encrypted APNs token")
	}
	nonce := value[:repository.vault.NonceSize()]
	ciphertext := value[repository.vault.NonceSize():]
	plaintext, err := repository.vault.Open(nil, nonce, ciphertext, []byte(routeID))
	if err != nil {
		return "", errors.New("decrypt APNs token")
	}
	return string(plaintext), nil
}

func digestString(value string) []byte {
	digest := sha256.Sum256([]byte(value))
	return digest[:]
}
