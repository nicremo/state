package store

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nicremo/state/internal/state"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

var ErrAuditKeyMismatch = errors.New("audit signing key does not match stored public key")

// The audit table DDL, its immutability triggers and its reminder index are
// shared between ensureSchema (fresh databases) and migrateAuditReminderNullable
// (legacy rebuild), so both paths produce byte-identical objects.
const stateAuditEventsTableStatement = `CREATE TABLE IF NOT EXISTS state_audit_events (
	sequence INTEGER PRIMARY KEY AUTOINCREMENT,
	id TEXT NOT NULL UNIQUE,
	reminder_id TEXT NULL REFERENCES state_reminders(id),
	action TEXT NOT NULL,
	revision INTEGER NOT NULL,
	client_request_id TEXT NOT NULL UNIQUE,
	previous_hash TEXT NOT NULL,
	hash TEXT NOT NULL UNIQUE,
	signature TEXT NOT NULL,
	server_time TEXT NOT NULL,
	event_json TEXT NOT NULL CHECK(json_valid(event_json))
) STRICT`

const stateAuditEventsReminderIndexStatement = `CREATE INDEX IF NOT EXISTS state_audit_events_reminder_sequence_idx
	ON state_audit_events(reminder_id, sequence)`

const stateAuditEventsNoUpdateTriggerStatement = `CREATE TRIGGER IF NOT EXISTS state_audit_events_no_update
	BEFORE UPDATE ON state_audit_events
	BEGIN
		SELECT RAISE(ABORT, 'state audit events are immutable');
	END`

const stateAuditEventsNoDeleteTriggerStatement = `CREATE TRIGGER IF NOT EXISTS state_audit_events_no_delete
	BEFORE DELETE ON state_audit_events
	BEGIN
		SELECT RAISE(ABORT, 'state audit events are immutable');
	END`

type PocketBaseRepository struct {
	app        core.App
	signingKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
}

func NewPocketBaseRepository(app core.App, signingKey ed25519.PrivateKey) (*PocketBaseRepository, error) {
	if app == nil || len(signingKey) != ed25519.PrivateKeySize {
		return nil, state.ErrInvalidInput
	}
	repository := &PocketBaseRepository{
		app:        app,
		signingKey: append(ed25519.PrivateKey(nil), signingKey...),
		publicKey:  append(ed25519.PublicKey(nil), signingKey.Public().(ed25519.PublicKey)...),
	}
	if err := repository.ensureSchema(); err != nil {
		return nil, fmt.Errorf("initialize state schema: %w", err)
	}
	if err := repository.ensureAuditKey(); err != nil {
		return nil, err
	}
	return repository, nil
}

func (repository *PocketBaseRepository) CreateReminder(
	_ context.Context,
	reminder state.Reminder,
	event state.AuditEvent,
	clientRequestID string,
) (state.Reminder, error) {
	var result state.Reminder
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		existing, found, err := lookupIdempotentReminder(txApp, clientRequestID, event.Actor.ID)
		if err != nil {
			return err
		}
		if found {
			result = existing
			return nil
		}

		reminderJSON, err := json.Marshal(reminder)
		if err != nil {
			return fmt.Errorf("encode reminder: %w", err)
		}
		_, err = txApp.DB().NewQuery(`
			INSERT INTO state_reminders (
				id, data_json, title, description, revision, archived, created_at, updated_at
			) VALUES (
				{:id}, {:data_json}, {:title}, {:description}, {:revision}, {:archived}, {:created_at}, {:updated_at}
			)
		`).Bind(dbx.Params{
			"id":          reminder.ID,
			"data_json":   string(reminderJSON),
			"title":       reminder.Title,
			"description": reminder.Description,
			"revision":    reminder.Revision,
			"archived":    reminder.Archived,
			"created_at":  formatTime(reminder.CreatedAt),
			"updated_at":  formatTime(reminder.UpdatedAt),
		}).Execute()
		if err != nil {
			return fmt.Errorf("insert reminder: %w", err)
		}
		sealedEvent, err := repository.sealAuditEvent(txApp, event)
		if err != nil {
			return err
		}
		if err := insertAuditEvent(txApp, sealedEvent); err != nil {
			return err
		}
		if err := upsertReminderSearch(txApp, reminder); err != nil {
			return err
		}
		if err := insertAuditSearch(txApp, sealedEvent); err != nil {
			return err
		}
		if err := insertIdempotencyResult(txApp, clientRequestID, event.Actor.ID, reminder); err != nil {
			return err
		}
		if err := reconcileOccurrences(txApp, reminder); err != nil {
			return err
		}
		result = reminder
		return nil
	})
	if err != nil {
		return state.Reminder{}, err
	}
	return result, nil
}

func (repository *PocketBaseRepository) UpdateReminder(
	_ context.Context,
	reminder state.Reminder,
	expectedRevision int64,
	event state.AuditEvent,
	clientRequestID string,
) (state.Reminder, error) {
	var result state.Reminder
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		existing, found, err := lookupIdempotentReminder(txApp, clientRequestID, event.Actor.ID)
		if err != nil {
			return err
		}
		if found {
			result = existing
			return nil
		}

		current, err := getReminder(txApp, reminder.ID)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return state.ErrRevisionConflict
		}
		reminderJSON, err := json.Marshal(reminder)
		if err != nil {
			return fmt.Errorf("encode reminder: %w", err)
		}
		databaseResult, err := txApp.DB().NewQuery(`
			UPDATE state_reminders
			SET data_json = {:data_json},
				title = {:title},
				description = {:description},
				revision = {:new_revision},
				archived = {:archived},
				updated_at = {:updated_at}
			WHERE id = {:id} AND revision = {:expected_revision}
		`).Bind(dbx.Params{
			"id":                reminder.ID,
			"data_json":         string(reminderJSON),
			"title":             reminder.Title,
			"description":       reminder.Description,
			"new_revision":      reminder.Revision,
			"archived":          reminder.Archived,
			"updated_at":        formatTime(reminder.UpdatedAt),
			"expected_revision": expectedRevision,
		}).Execute()
		if err != nil {
			return fmt.Errorf("update reminder: %w", err)
		}
		rowsAffected, err := databaseResult.RowsAffected()
		if err != nil {
			return fmt.Errorf("read update result: %w", err)
		}
		if rowsAffected != 1 {
			return state.ErrRevisionConflict
		}
		sealedEvent, err := repository.sealAuditEvent(txApp, event)
		if err != nil {
			return err
		}
		if err := insertAuditEvent(txApp, sealedEvent); err != nil {
			return err
		}
		if err := upsertReminderSearch(txApp, reminder); err != nil {
			return err
		}
		if err := insertAuditSearch(txApp, sealedEvent); err != nil {
			return err
		}
		if err := insertIdempotencyResult(txApp, clientRequestID, event.Actor.ID, reminder); err != nil {
			return err
		}
		if err := reconcileOccurrences(txApp, reminder); err != nil {
			return err
		}
		result = reminder
		return nil
	})
	if err != nil {
		return state.Reminder{}, err
	}
	return result, nil
}

func (repository *PocketBaseRepository) GetReminder(_ context.Context, reminderID string) (state.Reminder, error) {
	return getReminder(repository.app, reminderID)
}

func (repository *PocketBaseRepository) ListAuditEvents(_ context.Context, reminderID string) ([]state.AuditEvent, error) {
	rows := make([]struct {
		EventJSON string `db:"event_json"`
	}, 0)
	err := repository.app.DB().NewQuery(`
		SELECT event_json
		FROM state_audit_events
		WHERE reminder_id = {:reminder_id}
		ORDER BY sequence ASC
	`).Bind(dbx.Params{"reminder_id": reminderID}).All(&rows)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	events := make([]state.AuditEvent, 0, len(rows))
	for _, row := range rows {
		var event state.AuditEvent
		if err := json.Unmarshal([]byte(row.EventJSON), &event); err != nil {
			return nil, fmt.Errorf("decode audit event: %w", err)
		}
		events = append(events, event)
	}
	return events, nil
}

func (repository *PocketBaseRepository) ListReminders(_ context.Context, options state.ReminderListOptions) ([]state.Reminder, error) {
	where := "WHERE 1 = 1"
	params := dbx.Params{}
	if !options.IncludeArchived {
		where += " AND archived = 0"
	}
	if options.Status != nil {
		where += " AND json_extract(data_json, '$.status') = {:status}"
		params["status"] = string(*options.Status)
	}
	params["limit"] = normalizeLimit(options.Limit)
	rows := make([]struct {
		DataJSON string `db:"data_json"`
	}, 0)
	err := repository.app.DB().NewQuery(`
		SELECT data_json FROM state_reminders
		` + where + `
		ORDER BY updated_at DESC, id DESC
		LIMIT {:limit}
	`).Bind(params).All(&rows)
	if err != nil {
		return nil, fmt.Errorf("list reminders: %w", err)
	}
	return decodeReminders(rows)
}

func (repository *PocketBaseRepository) SearchReminders(_ context.Context, query string, limit int) ([]state.Reminder, error) {
	matchQuery := ftsQuery(query)
	if matchQuery == "" {
		return repository.ListReminders(context.Background(), state.ReminderListOptions{Limit: limit})
	}
	rows := make([]struct {
		ReminderID string  `db:"reminder_id"`
		Rank       float64 `db:"rank"`
	}, 0)
	err := repository.app.DB().NewQuery(`
		SELECT reminder_id, bm25(state_search) AS rank
		FROM state_search
		WHERE state_search MATCH {:query}
		ORDER BY rank ASC
		LIMIT {:limit}
	`).Bind(dbx.Params{
		"query": matchQuery,
		"limit": normalizeLimit(limit) * 4,
	}).All(&rows)
	if err != nil {
		return nil, fmt.Errorf("search reminders: %w", err)
	}
	seen := make(map[string]struct{})
	result := make([]state.Reminder, 0)
	for _, row := range rows {
		if row.ReminderID == "" {
			// Project, policy and runner audit documents are not linked to a
			// reminder and cannot be resolved into one.
			continue
		}
		if _, exists := seen[row.ReminderID]; exists {
			continue
		}
		reminder, err := getReminder(repository.app, row.ReminderID)
		if err != nil {
			return nil, err
		}
		seen[row.ReminderID] = struct{}{}
		result = append(result, reminder)
		if len(result) == normalizeLimit(limit) {
			break
		}
	}
	return result, nil
}

func (repository *PocketBaseRepository) ListChanges(_ context.Context, afterCursor int64, limit int) ([]state.Change, error) {
	rows := make([]struct {
		Sequence  int64  `db:"sequence"`
		EventJSON string `db:"event_json"`
	}, 0)
	err := repository.app.DB().NewQuery(`
		SELECT sequence, event_json
		FROM state_audit_events
		WHERE sequence > {:after_cursor}
		ORDER BY sequence ASC
		LIMIT {:limit}
	`).Bind(dbx.Params{
		"after_cursor": afterCursor,
		"limit":        normalizeLimit(limit),
	}).All(&rows)
	if err != nil {
		return nil, fmt.Errorf("list changes: %w", err)
	}
	changes := make([]state.Change, 0, len(rows))
	for _, row := range rows {
		var event state.AuditEvent
		if err := json.Unmarshal([]byte(row.EventJSON), &event); err != nil {
			return nil, fmt.Errorf("decode change event: %w", err)
		}
		changes = append(changes, state.Change{Cursor: row.Sequence, Event: event})
	}
	return changes, nil
}

func (repository *PocketBaseRepository) AddComment(
	_ context.Context,
	comment state.Comment,
	event state.AuditEvent,
	clientRequestID string,
) (state.Comment, error) {
	var result state.Comment
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		existing, found, err := lookupIdempotentComment(txApp, clientRequestID, event.Actor.ID)
		if err != nil {
			return err
		}
		if found {
			result = existing
			return nil
		}
		if _, err := getReminder(txApp, comment.ReminderID); err != nil {
			return err
		}
		commentJSON, err := json.Marshal(comment)
		if err != nil {
			return fmt.Errorf("encode comment: %w", err)
		}
		_, err = txApp.DB().NewQuery(`
			INSERT INTO state_comments (
				id, reminder_id, body, revision, created_at, updated_at, data_json
			) VALUES (
				{:id}, {:reminder_id}, {:body}, {:revision}, {:created_at}, {:updated_at}, {:data_json}
			)
		`).Bind(dbx.Params{
			"id":          comment.ID,
			"reminder_id": comment.ReminderID,
			"body":        comment.Body,
			"revision":    comment.Revision,
			"created_at":  formatTime(comment.CreatedAt),
			"updated_at":  formatTime(comment.UpdatedAt),
			"data_json":   string(commentJSON),
		}).Execute()
		if err != nil {
			return fmt.Errorf("insert comment: %w", err)
		}
		sealedEvent, err := repository.sealAuditEvent(txApp, event)
		if err != nil {
			return err
		}
		if err := insertAuditEvent(txApp, sealedEvent); err != nil {
			return err
		}
		if err := insertCommentSearch(txApp, comment); err != nil {
			return err
		}
		if err := insertAuditSearch(txApp, sealedEvent); err != nil {
			return err
		}
		if err := insertIdempotencyValue(txApp, clientRequestID, event.Actor.ID, "comment", comment.ID, comment); err != nil {
			return err
		}
		result = comment
		return nil
	})
	if err != nil {
		return state.Comment{}, err
	}
	return result, nil
}

func (repository *PocketBaseRepository) ListComments(_ context.Context, reminderID string) ([]state.Comment, error) {
	rows := make([]struct {
		DataJSON string `db:"data_json"`
	}, 0)
	err := repository.app.DB().NewQuery(`
		SELECT data_json
		FROM state_comments
		WHERE reminder_id = {:reminder_id}
		ORDER BY created_at ASC, id ASC
	`).Bind(dbx.Params{"reminder_id": reminderID}).All(&rows)
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	comments := make([]state.Comment, 0, len(rows))
	for _, row := range rows {
		var comment state.Comment
		if err := json.Unmarshal([]byte(row.DataJSON), &comment); err != nil {
			return nil, fmt.Errorf("decode comment: %w", err)
		}
		comments = append(comments, comment)
	}
	return comments, nil
}

func (repository *PocketBaseRepository) GetOccurrence(_ context.Context, occurrenceID string) (state.Occurrence, error) {
	return getOccurrence(repository.app, occurrenceID)
}

func (repository *PocketBaseRepository) ListOccurrences(_ context.Context, reminderID string, options state.OccurrenceListOptions) ([]state.Occurrence, error) {
	where := "WHERE reminder_id = {:reminder_id}"
	params := dbx.Params{
		"reminder_id": reminderID,
		"limit":       normalizeLimit(options.Limit),
	}
	if options.Status != nil {
		where += " AND status = {:status}"
		params["status"] = string(*options.Status)
	}
	rows := make([]struct {
		DataJSON string `db:"data_json"`
	}, 0)
	err := repository.app.DB().NewQuery(`
		SELECT data_json
		FROM state_occurrences
		` + where + `
		ORDER BY local_date ASC, local_time ASC, id ASC
		LIMIT {:limit}
	`).Bind(params).All(&rows)
	if err != nil {
		return nil, fmt.Errorf("list occurrences: %w", err)
	}
	occurrences := make([]state.Occurrence, 0, len(rows))
	for _, row := range rows {
		var occurrence state.Occurrence
		if err := json.Unmarshal([]byte(row.DataJSON), &occurrence); err != nil {
			return nil, fmt.Errorf("decode occurrence: %w", err)
		}
		occurrences = append(occurrences, occurrence)
	}
	return occurrences, nil
}

func (repository *PocketBaseRepository) UpdateOccurrence(
	_ context.Context,
	occurrence state.Occurrence,
	expectedRevision int64,
	event state.AuditEvent,
	clientRequestID string,
) (state.Occurrence, error) {
	var result state.Occurrence
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		existing, found, err := lookupIdempotentOccurrence(txApp, clientRequestID, event.Actor.ID)
		if err != nil {
			return err
		}
		if found {
			result = existing
			return nil
		}
		current, err := getOccurrence(txApp, occurrence.ID)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return state.ErrRevisionConflict
		}
		occurrenceJSON, err := json.Marshal(occurrence)
		if err != nil {
			return fmt.Errorf("encode occurrence: %w", err)
		}
		databaseResult, err := txApp.DB().NewQuery(`
			UPDATE state_occurrences
			SET status = {:status},
				revision = {:new_revision},
				updated_at = {:updated_at},
				data_json = {:data_json}
			WHERE id = {:id} AND revision = {:expected_revision}
		`).Bind(dbx.Params{
			"id":                occurrence.ID,
			"status":            string(occurrence.Status),
			"new_revision":      occurrence.Revision,
			"updated_at":        formatTime(occurrence.UpdatedAt),
			"data_json":         string(occurrenceJSON),
			"expected_revision": expectedRevision,
		}).Execute()
		if err != nil {
			return fmt.Errorf("update occurrence: %w", err)
		}
		rowsAffected, err := databaseResult.RowsAffected()
		if err != nil {
			return fmt.Errorf("read occurrence update result: %w", err)
		}
		if rowsAffected != 1 {
			return state.ErrRevisionConflict
		}
		sealedEvent, err := repository.sealAuditEvent(txApp, event)
		if err != nil {
			return err
		}
		if err := insertAuditEvent(txApp, sealedEvent); err != nil {
			return err
		}
		if err := insertAuditSearch(txApp, sealedEvent); err != nil {
			return err
		}
		if err := insertIdempotencyValue(txApp, clientRequestID, event.Actor.ID, "occurrence", occurrence.ID, occurrence); err != nil {
			return err
		}
		result = occurrence
		return nil
	})
	if err != nil {
		return state.Occurrence{}, err
	}
	return result, nil
}

func (repository *PocketBaseRepository) VerifyAuditChain(_ context.Context) error {
	rows := make([]struct {
		EventJSON string `db:"event_json"`
	}, 0)
	if err := repository.app.DB().NewQuery(`
		SELECT event_json
		FROM state_audit_events
		ORDER BY sequence ASC
	`).All(&rows); err != nil {
		return fmt.Errorf("list audit chain: %w", err)
	}
	previousHash := ""
	for _, row := range rows {
		var event state.AuditEvent
		if err := json.Unmarshal([]byte(row.EventJSON), &event); err != nil {
			return fmt.Errorf("decode audit event: %w", err)
		}
		if event.PreviousHash != previousHash {
			return fmt.Errorf("audit event %s has invalid previous hash", event.ID)
		}
		signature, err := base64.RawURLEncoding.DecodeString(event.Signature)
		if err != nil {
			return fmt.Errorf("decode signature for audit event %s: %w", event.ID, err)
		}
		storedHash := event.Hash
		event.Hash = ""
		event.Signature = ""
		encoded, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("encode audit event %s: %w", event.ID, err)
		}
		digest := sha256.Sum256(encoded)
		if hex.EncodeToString(digest[:]) != storedHash {
			return fmt.Errorf("audit event %s has invalid hash", event.ID)
		}
		if !ed25519.Verify(repository.publicKey, digest[:], signature) {
			return fmt.Errorf("audit event %s has invalid signature", event.ID)
		}
		previousHash = storedHash
	}
	return nil
}

func (repository *PocketBaseRepository) ensureSchema() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS state_metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		) STRICT`,
		`CREATE TABLE IF NOT EXISTS state_reminders (
			id TEXT PRIMARY KEY,
			data_json TEXT NOT NULL CHECK(json_valid(data_json)),
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			revision INTEGER NOT NULL CHECK(revision > 0),
			archived INTEGER NOT NULL DEFAULT 0 CHECK(archived IN (0, 1)),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		) STRICT`,
		`CREATE TABLE IF NOT EXISTS state_comments (
			id TEXT PRIMARY KEY,
			reminder_id TEXT NOT NULL REFERENCES state_reminders(id),
			body TEXT NOT NULL,
			revision INTEGER NOT NULL CHECK(revision > 0),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			data_json TEXT NOT NULL CHECK(json_valid(data_json))
		) STRICT`,
		`CREATE INDEX IF NOT EXISTS state_comments_reminder_created_idx
			ON state_comments(reminder_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS state_occurrences (
			id TEXT PRIMARY KEY,
			reminder_id TEXT NOT NULL REFERENCES state_reminders(id),
			local_date TEXT NOT NULL,
			local_time TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL CHECK(status IN ('pending', 'completed', 'snoozed')),
			revision INTEGER NOT NULL CHECK(revision > 0),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			data_json TEXT NOT NULL CHECK(json_valid(data_json)),
			UNIQUE(reminder_id, local_date, local_time)
		) STRICT`,
		`CREATE INDEX IF NOT EXISTS state_occurrences_reminder_schedule_idx
			ON state_occurrences(reminder_id, local_date, local_time)`,
		`CREATE INDEX IF NOT EXISTS state_occurrences_status_schedule_idx
			ON state_occurrences(status, local_date, local_time)`,
		stateAuditEventsTableStatement,
		`CREATE TABLE IF NOT EXISTS state_idempotency (
			client_request_id TEXT PRIMARY KEY,
			actor_id TEXT NOT NULL,
			result_type TEXT NOT NULL,
			result_id TEXT NOT NULL,
			result_json TEXT NOT NULL CHECK(json_valid(result_json)),
			created_at TEXT NOT NULL
		) STRICT`,
		`CREATE TABLE IF NOT EXISTS state_projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			revision INTEGER NOT NULL CHECK(revision > 0),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			data_json TEXT NOT NULL CHECK(json_valid(data_json))
		) STRICT`,
		`CREATE TABLE IF NOT EXISTS state_policies (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			project_id TEXT NOT NULL REFERENCES state_projects(id),
			adapter TEXT NOT NULL,
			mode TEXT NOT NULL CHECK(mode IN ('supervised', 'unattended-low-risk')),
			enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
			revision INTEGER NOT NULL CHECK(revision > 0),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			data_json TEXT NOT NULL CHECK(json_valid(data_json))
		) STRICT`,
		`CREATE INDEX IF NOT EXISTS state_policies_project_idx
			ON state_policies(project_id)`,
		`CREATE TABLE IF NOT EXISTS state_runners (
			id TEXT PRIMARY KEY REFERENCES state_actors(id),
			display_name TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			revision INTEGER NOT NULL CHECK(revision > 0),
			registered_at TEXT NOT NULL,
			data_json TEXT NOT NULL CHECK(json_valid(data_json))
		) STRICT`,
		`CREATE TABLE IF NOT EXISTS state_runs (
			id TEXT PRIMARY KEY,
			reminder_id TEXT NOT NULL REFERENCES state_reminders(id),
			occurrence_id TEXT REFERENCES state_occurrences(id),
			policy_id TEXT NOT NULL REFERENCES state_policies(id),
			policy_revision INTEGER NOT NULL,
			project_id TEXT NOT NULL REFERENCES state_projects(id),
			adapter TEXT NOT NULL,
			runner_id TEXT,
			status TEXT NOT NULL CHECK(status IN ('planned', 'eligible', 'claimed', 'running', 'needs_approval', 'succeeded', 'failed', 'cancelled', 'expired')),
			idempotency_key TEXT NOT NULL,
			lease_expires_at TEXT,
			requested_at TEXT,
			claimed_at TEXT,
			started_at TEXT,
			finished_at TEXT,
			revision INTEGER NOT NULL CHECK(revision > 0),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			data_json TEXT NOT NULL CHECK(json_valid(data_json)),
			UNIQUE(occurrence_id, policy_revision)
		) STRICT`,
		`CREATE INDEX IF NOT EXISTS state_runs_status_lease_idx
			ON state_runs(status, lease_expires_at)`,
		`CREATE INDEX IF NOT EXISTS state_runs_reminder_idx
			ON state_runs(reminder_id)`,
		`CREATE INDEX IF NOT EXISTS state_runs_runner_status_idx
			ON state_runs(runner_id, status)`,
		stateAuditEventsReminderIndexStatement,
		`CREATE INDEX IF NOT EXISTS state_reminders_updated_at_idx
			ON state_reminders(updated_at DESC)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS state_search USING fts5(
			reminder_id UNINDEXED,
			kind UNINDEXED,
			content,
			tokenize = 'unicode61 remove_diacritics 2'
		)`,
		stateAuditEventsNoUpdateTriggerStatement,
		stateAuditEventsNoDeleteTriggerStatement,
	}
	if err := repository.app.RunInTransaction(func(txApp core.App) error {
		for _, statement := range statements {
			if _, err := txApp.DB().NewQuery(statement).Execute(); err != nil {
				return err
			}
		}
		if _, err := txApp.DB().NewQuery(`
			INSERT INTO state_search (reminder_id, kind, content)
			SELECT id, 'reminder', title || char(10) || description
			FROM state_reminders r
			WHERE NOT EXISTS (
				SELECT 1 FROM state_search s WHERE s.reminder_id = r.id AND s.kind = 'reminder'
			)
		`).Execute(); err != nil {
			return err
		}
		if _, err := txApp.DB().NewQuery(`
			INSERT INTO state_search (reminder_id, kind, content)
			SELECT reminder_id, 'comment', body
			FROM state_comments c
			WHERE NOT EXISTS (
				SELECT 1 FROM state_search s
				WHERE s.reminder_id = c.reminder_id AND s.kind = 'comment' AND s.content = c.body
			)
		`).Execute(); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	return repository.migrateAuditReminderNullable()
}

// migrateAuditReminderNullable rebuilds legacy state_audit_events tables whose
// reminder_id column was created NOT NULL, so that policy, project and runner
// events (which carry no reminder) can be recorded. Fresh databases already
// have the nullable DDL and are skipped. The hash chain, the immutability
// triggers and all indexes are preserved.
func (repository *PocketBaseRepository) migrateAuditReminderNullable() error {
	row := struct {
		SQL string `db:"sql"`
	}{}
	err := repository.app.DB().NewQuery(`
		SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'state_audit_events'
	`).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect audit events schema: %w", err)
	}
	if !strings.Contains(row.SQL, "reminder_id TEXT NOT NULL") {
		return nil
	}
	return repository.app.RunInTransaction(func(txApp core.App) error {
		for _, statement := range []string{
			`DROP TRIGGER IF EXISTS state_audit_events_no_update`,
			`DROP TRIGGER IF EXISTS state_audit_events_no_delete`,
			`ALTER TABLE state_audit_events RENAME TO state_audit_events_old`,
			stateAuditEventsTableStatement,
			`INSERT INTO state_audit_events (
				sequence, id, reminder_id, action, revision, client_request_id, previous_hash, hash, signature, server_time, event_json
			) SELECT
				sequence, id, reminder_id, action, revision, client_request_id, previous_hash, hash, signature, server_time, event_json
			FROM state_audit_events_old
			ORDER BY sequence ASC`,
			`DROP TABLE state_audit_events_old`,
			stateAuditEventsReminderIndexStatement,
			stateAuditEventsNoUpdateTriggerStatement,
			stateAuditEventsNoDeleteTriggerStatement,
		} {
			if _, err := txApp.DB().NewQuery(statement).Execute(); err != nil {
				return err
			}
		}
		return nil
	})
}

func (repository *PocketBaseRepository) ensureAuditKey() error {
	encodedPublicKey := base64.RawURLEncoding.EncodeToString(repository.publicKey)
	return repository.app.RunInTransaction(func(txApp core.App) error {
		_, err := txApp.DB().NewQuery(`
			INSERT OR IGNORE INTO state_metadata (key, value)
			VALUES ('audit_public_key', {:value})
		`).Bind(dbx.Params{"value": encodedPublicKey}).Execute()
		if err != nil {
			return err
		}
		row := struct {
			Value string `db:"value"`
		}{}
		if err := txApp.DB().NewQuery(`
			SELECT value FROM state_metadata WHERE key = 'audit_public_key'
		`).One(&row); err != nil {
			return err
		}
		if row.Value != encodedPublicKey {
			return ErrAuditKeyMismatch
		}
		return nil
	})
}

func (repository *PocketBaseRepository) sealAuditEvent(app core.App, event state.AuditEvent) (state.AuditEvent, error) {
	row := struct {
		Hash string `db:"hash"`
	}{}
	err := app.DB().NewQuery(`
		SELECT hash FROM state_audit_events ORDER BY sequence DESC LIMIT 1
	`).One(&row)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return state.AuditEvent{}, fmt.Errorf("read audit chain head: %w", err)
	}
	event.PreviousHash = row.Hash
	event.Hash = ""
	event.Signature = ""
	encoded, err := json.Marshal(event)
	if err != nil {
		return state.AuditEvent{}, fmt.Errorf("encode audit event: %w", err)
	}
	digest := sha256.Sum256(encoded)
	event.Hash = hex.EncodeToString(digest[:])
	event.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(repository.signingKey, digest[:]))
	return event, nil
}

func insertAuditEvent(app core.App, event state.AuditEvent) error {
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode sealed audit event: %w", err)
	}
	var reminderID any
	if event.ReminderID != "" {
		reminderID = event.ReminderID
	}
	_, err = app.DB().NewQuery(`
		INSERT INTO state_audit_events (
			id, reminder_id, action, revision, client_request_id, previous_hash, hash, signature, server_time, event_json
		) VALUES (
			{:id}, {:reminder_id}, {:action}, {:revision}, {:client_request_id}, {:previous_hash}, {:hash}, {:signature}, {:server_time}, {:event_json}
		)
	`).Bind(dbx.Params{
		"id":                event.ID,
		"reminder_id":       reminderID,
		"action":            string(event.Action),
		"revision":          event.Revision,
		"client_request_id": event.ClientRequestID,
		"previous_hash":     event.PreviousHash,
		"hash":              event.Hash,
		"signature":         event.Signature,
		"server_time":       formatTime(event.ServerTime),
		"event_json":        string(eventJSON),
	}).Execute()
	if err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}

func insertIdempotencyResult(app core.App, clientRequestID string, actorID string, reminder state.Reminder) error {
	return insertIdempotencyValue(app, clientRequestID, actorID, "reminder", reminder.ID, reminder)
}

func insertIdempotencyValue(app core.App, clientRequestID string, actorID string, resultType string, resultID string, value any) error {
	resultJSON, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode idempotency result: %w", err)
	}
	_, err = app.DB().NewQuery(`
		INSERT INTO state_idempotency (
			client_request_id, actor_id, result_type, result_id, result_json, created_at
		) VALUES (
			{:client_request_id}, {:actor_id}, {:result_type}, {:result_id}, {:result_json}, {:created_at}
		)
	`).Bind(dbx.Params{
		"client_request_id": clientRequestID,
		"actor_id":          actorID,
		"result_type":       resultType,
		"result_id":         resultID,
		"result_json":       string(resultJSON),
		"created_at":        formatTime(time.Now().UTC()),
	}).Execute()
	if err != nil {
		return fmt.Errorf("insert idempotency result: %w", err)
	}
	return nil
}

func lookupIdempotentReminder(app core.App, clientRequestID string, actorID string) (state.Reminder, bool, error) {
	row := struct {
		ActorID    string `db:"actor_id"`
		ResultType string `db:"result_type"`
		ResultJSON string `db:"result_json"`
	}{}
	err := app.DB().NewQuery(`
		SELECT actor_id, result_type, result_json
		FROM state_idempotency
		WHERE client_request_id = {:client_request_id}
	`).Bind(dbx.Params{"client_request_id": clientRequestID}).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.Reminder{}, false, nil
	}
	if err != nil {
		return state.Reminder{}, false, fmt.Errorf("read idempotency result: %w", err)
	}
	if row.ActorID != actorID {
		return state.Reminder{}, false, state.ErrForbidden
	}
	if row.ResultType != "reminder" {
		return state.Reminder{}, false, state.ErrInvalidInput
	}
	var reminder state.Reminder
	if err := json.Unmarshal([]byte(row.ResultJSON), &reminder); err != nil {
		return state.Reminder{}, false, fmt.Errorf("decode idempotency result: %w", err)
	}
	return reminder, true, nil
}

func lookupIdempotentComment(app core.App, clientRequestID string, actorID string) (state.Comment, bool, error) {
	row := struct {
		ActorID    string `db:"actor_id"`
		ResultType string `db:"result_type"`
		ResultJSON string `db:"result_json"`
	}{}
	err := app.DB().NewQuery(`
		SELECT actor_id, result_type, result_json
		FROM state_idempotency
		WHERE client_request_id = {:client_request_id}
	`).Bind(dbx.Params{"client_request_id": clientRequestID}).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.Comment{}, false, nil
	}
	if err != nil {
		return state.Comment{}, false, fmt.Errorf("read comment idempotency result: %w", err)
	}
	if row.ActorID != actorID {
		return state.Comment{}, false, state.ErrForbidden
	}
	if row.ResultType != "comment" {
		return state.Comment{}, false, state.ErrInvalidInput
	}
	var comment state.Comment
	if err := json.Unmarshal([]byte(row.ResultJSON), &comment); err != nil {
		return state.Comment{}, false, fmt.Errorf("decode comment idempotency result: %w", err)
	}
	return comment, true, nil
}

func lookupIdempotentOccurrence(app core.App, clientRequestID string, actorID string) (state.Occurrence, bool, error) {
	row := struct {
		ActorID    string `db:"actor_id"`
		ResultType string `db:"result_type"`
		ResultJSON string `db:"result_json"`
	}{}
	err := app.DB().NewQuery(`
		SELECT actor_id, result_type, result_json
		FROM state_idempotency
		WHERE client_request_id = {:client_request_id}
	`).Bind(dbx.Params{"client_request_id": clientRequestID}).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.Occurrence{}, false, nil
	}
	if err != nil {
		return state.Occurrence{}, false, fmt.Errorf("read occurrence idempotency result: %w", err)
	}
	if row.ActorID != actorID {
		return state.Occurrence{}, false, state.ErrForbidden
	}
	if row.ResultType != "occurrence" {
		return state.Occurrence{}, false, state.ErrInvalidInput
	}
	var occurrence state.Occurrence
	if err := json.Unmarshal([]byte(row.ResultJSON), &occurrence); err != nil {
		return state.Occurrence{}, false, fmt.Errorf("decode occurrence idempotency result: %w", err)
	}
	return occurrence, true, nil
}

func getReminder(app core.App, reminderID string) (state.Reminder, error) {
	row := struct {
		DataJSON string `db:"data_json"`
	}{}
	err := app.DB().NewQuery(`
		SELECT data_json FROM state_reminders WHERE id = {:id}
	`).Bind(dbx.Params{"id": reminderID}).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.Reminder{}, state.ErrNotFound
	}
	if err != nil {
		return state.Reminder{}, fmt.Errorf("get reminder: %w", err)
	}
	var reminder state.Reminder
	if err := json.Unmarshal([]byte(row.DataJSON), &reminder); err != nil {
		return state.Reminder{}, fmt.Errorf("decode reminder: %w", err)
	}
	return reminder, nil
}

func getOccurrence(app core.App, occurrenceID string) (state.Occurrence, error) {
	row := struct {
		DataJSON string `db:"data_json"`
	}{}
	err := app.DB().NewQuery(`
		SELECT data_json FROM state_occurrences WHERE id = {:id}
	`).Bind(dbx.Params{"id": occurrenceID}).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.Occurrence{}, state.ErrNotFound
	}
	if err != nil {
		return state.Occurrence{}, fmt.Errorf("get occurrence: %w", err)
	}
	var occurrence state.Occurrence
	if err := json.Unmarshal([]byte(row.DataJSON), &occurrence); err != nil {
		return state.Occurrence{}, fmt.Errorf("decode occurrence: %w", err)
	}
	return occurrence, nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func reconcileOccurrences(app core.App, reminder state.Reminder) error {
	if _, err := app.DB().NewQuery(`
		DELETE FROM state_occurrences
		WHERE reminder_id = {:reminder_id} AND status = 'pending'
	`).Bind(dbx.Params{"reminder_id": reminder.ID}).Execute(); err != nil {
		return fmt.Errorf("remove pending occurrences: %w", err)
	}
	if reminder.Schedule == nil || reminder.Archived {
		return nil
	}
	fromDate := reminder.Schedule.LocalDate
	if reminder.Recurrence != nil {
		location, err := time.LoadLocation(reminder.Schedule.TimeZone)
		if err != nil {
			return err
		}
		fromDate = reminder.UpdatedAt.In(location).Format("2006-01-02")
	}
	throughDate := reminder.UpdatedAt.AddDate(1, 1, 0).Format("2006-01-02")
	seeds, err := state.ExpandOccurrenceSeeds(reminder, fromDate, throughDate)
	if err != nil {
		return err
	}
	for _, seed := range seeds {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate occurrence ID: %w", err)
		}
		occurrence := state.Occurrence{
			ID:                id.String(),
			ReminderID:        reminder.ID,
			LocalDate:         seed.LocalDate,
			LocalTime:         seed.LocalTime,
			TimeZone:          seed.TimeZone,
			TimeZoneMode:      seed.TimeZoneMode,
			PrewarningMinutes: seed.PrewarningMinutes,
			ScheduledAt:       seed.ScheduledAt,
			Status:            state.OccurrenceStatusPending,
			Revision:          1,
			CreatedAt:         reminder.UpdatedAt,
			UpdatedAt:         reminder.UpdatedAt,
		}
		occurrenceJSON, err := json.Marshal(occurrence)
		if err != nil {
			return fmt.Errorf("encode occurrence: %w", err)
		}
		_, err = app.DB().NewQuery(`
			INSERT OR IGNORE INTO state_occurrences (
				id, reminder_id, local_date, local_time, status, revision, created_at, updated_at, data_json
			) VALUES (
				{:id}, {:reminder_id}, {:local_date}, {:local_time}, {:status}, {:revision}, {:created_at}, {:updated_at}, {:data_json}
			)
		`).Bind(dbx.Params{
			"id":          occurrence.ID,
			"reminder_id": occurrence.ReminderID,
			"local_date":  occurrence.LocalDate,
			"local_time":  occurrence.LocalTime,
			"status":      string(occurrence.Status),
			"revision":    occurrence.Revision,
			"created_at":  formatTime(occurrence.CreatedAt),
			"updated_at":  formatTime(occurrence.UpdatedAt),
			"data_json":   string(occurrenceJSON),
		}).Execute()
		if err != nil {
			return fmt.Errorf("insert occurrence: %w", err)
		}
	}
	return nil
}

func upsertReminderSearch(app core.App, reminder state.Reminder) error {
	if _, err := app.DB().NewQuery(`
		DELETE FROM state_search WHERE reminder_id = {:reminder_id} AND kind = 'reminder'
	`).Bind(dbx.Params{"reminder_id": reminder.ID}).Execute(); err != nil {
		return fmt.Errorf("remove reminder search document: %w", err)
	}
	_, err := app.DB().NewQuery(`
		INSERT INTO state_search (reminder_id, kind, content)
		VALUES ({:reminder_id}, 'reminder', {:content})
	`).Bind(dbx.Params{
		"reminder_id": reminder.ID,
		"content":     reminder.Title + "\n" + reminder.Description,
	}).Execute()
	if err != nil {
		return fmt.Errorf("insert reminder search document: %w", err)
	}
	return nil
}

func insertAuditSearch(app core.App, event state.AuditEvent, extraContent ...string) error {
	contentParts := []string{
		string(event.Action),
		event.Actor.DisplayName,
		event.Actor.Harness,
		event.Actor.DeviceName,
		event.SourceExcerpt,
		strings.Join(event.ChangedFields, " "),
	}
	contentParts = append(contentParts, extraContent...)
	_, err := app.DB().NewQuery(`
		INSERT INTO state_search (reminder_id, kind, content)
		VALUES ({:reminder_id}, 'audit', {:content})
	`).Bind(dbx.Params{
		"reminder_id": event.ReminderID,
		"content":     strings.Join(contentParts, "\n"),
	}).Execute()
	if err != nil {
		return fmt.Errorf("insert audit search document: %w", err)
	}
	return nil
}

func insertCommentSearch(app core.App, comment state.Comment) error {
	_, err := app.DB().NewQuery(`
		INSERT INTO state_search (reminder_id, kind, content)
		VALUES ({:reminder_id}, 'comment', {:content})
	`).Bind(dbx.Params{
		"reminder_id": comment.ReminderID,
		"content":     comment.Body,
	}).Execute()
	if err != nil {
		return fmt.Errorf("insert comment search document: %w", err)
	}
	return nil
}

func ftsQuery(query string) string {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return ""
	}
	encoded := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.ReplaceAll(term, `"`, `""`)
		encoded = append(encoded, `"`+term+`"*`)
	}
	return strings.Join(encoded, " AND ")
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func decodeReminders(rows []struct {
	DataJSON string `db:"data_json"`
}) ([]state.Reminder, error) {
	reminders := make([]state.Reminder, 0, len(rows))
	for _, row := range rows {
		var reminder state.Reminder
		if err := json.Unmarshal([]byte(row.DataJSON), &reminder); err != nil {
			return nil, fmt.Errorf("decode reminder list item: %w", err)
		}
		reminders = append(reminders, reminder)
	}
	sort.SliceStable(reminders, func(left int, right int) bool {
		return reminders[left].UpdatedAt.After(reminders[right].UpdatedAt)
	})
	return reminders, nil
}
