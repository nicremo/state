package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nicremo/state/internal/state"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// Notes live in their own table and full-text index. They share the audit
// chain with reminders, but their events carry note_id instead of
// reminder_id, so they never enter the reminder search index.
var notesSchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS state_notes (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		archived INTEGER NOT NULL DEFAULT 0 CHECK(archived IN (0, 1)),
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		data_json TEXT NOT NULL CHECK(json_valid(data_json))
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS state_notes_archived_updated_idx
		ON state_notes(archived, updated_at DESC)`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS state_note_search USING fts5(
		note_id UNINDEXED,
		content,
		tokenize = 'unicode61 remove_diacritics 2'
	)`,
	`CREATE INDEX IF NOT EXISTS state_audit_events_note_idx
		ON state_audit_events(json_extract(event_json, '$.note_id'), sequence)`,
}

func (repository *PocketBaseRepository) ensureNotesSchema() error {
	return repository.app.RunInTransaction(func(txApp core.App) error {
		for _, statement := range notesSchemaStatements {
			if _, err := txApp.DB().NewQuery(statement).Execute(); err != nil {
				return fmt.Errorf("create notes schema: %w", err)
			}
		}
		return nil
	})
}

func (repository *PocketBaseRepository) CreateNote(_ context.Context, note state.Note, event state.AuditEvent, clientRequestID string) (state.Note, error) {
	var result state.Note
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		var existing state.Note
		if _, found, err := lookupIdempotentValue(txApp, clientRequestID, event.Actor.ID, "note", &existing); err != nil {
			return err
		} else if found {
			result = existing
			return nil
		}
		noteJSON, err := json.Marshal(note)
		if err != nil {
			return fmt.Errorf("encode note: %w", err)
		}
		if _, err := txApp.DB().NewQuery(`
			INSERT INTO state_notes (id, title, archived, revision, created_at, updated_at, data_json)
			VALUES ({:id}, {:title}, {:archived}, {:revision}, {:created_at}, {:updated_at}, {:data_json})
		`).Bind(noteParams(note, noteJSON)).Execute(); err != nil {
			return fmt.Errorf("insert note: %w", err)
		}
		if err := repository.recordNoteChange(txApp, note, event, clientRequestID); err != nil {
			return err
		}
		result = note
		return nil
	})
	if err != nil {
		return state.Note{}, err
	}
	return result, nil
}

func (repository *PocketBaseRepository) UpdateNote(_ context.Context, note state.Note, expectedRevision int64, event state.AuditEvent, clientRequestID string) (state.Note, error) {
	var result state.Note
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		var existing state.Note
		if _, found, err := lookupIdempotentValue(txApp, clientRequestID, event.Actor.ID, "note", &existing); err != nil {
			return err
		} else if found {
			result = existing
			return nil
		}
		noteJSON, err := json.Marshal(note)
		if err != nil {
			return fmt.Errorf("encode note: %w", err)
		}
		params := noteParams(note, noteJSON)
		params["expected_revision"] = expectedRevision
		outcome, err := txApp.DB().NewQuery(`
			UPDATE state_notes
			SET title = {:title}, archived = {:archived}, revision = {:revision},
				updated_at = {:updated_at}, data_json = {:data_json}
			WHERE id = {:id} AND revision = {:expected_revision}
		`).Bind(params).Execute()
		if err != nil {
			return fmt.Errorf("update note: %w", err)
		}
		if affected, _ := outcome.RowsAffected(); affected == 0 {
			if _, err := getNote(txApp, note.ID); err != nil {
				return err
			}
			return state.ErrRevisionConflict
		}
		if err := repository.recordNoteChange(txApp, note, event, clientRequestID); err != nil {
			return err
		}
		result = note
		return nil
	})
	if err != nil {
		return state.Note{}, err
	}
	return result, nil
}

func (repository *PocketBaseRepository) GetNote(_ context.Context, noteID string) (state.Note, error) {
	return getNote(repository.app, noteID)
}

func (repository *PocketBaseRepository) ListNotes(_ context.Context, options state.NoteListOptions) ([]state.Note, error) {
	rows := make([]struct {
		DataJSON string `db:"data_json"`
	}, 0)
	params := dbx.Params{
		"include_archived": options.IncludeArchived,
		"limit":            normalizeLimit(options.Limit),
	}
	query := `
		SELECT n.data_json
		FROM state_notes n
		WHERE ({:include_archived} OR n.archived = 0)
		ORDER BY n.updated_at DESC, n.id DESC
		LIMIT {:limit}`
	if match := ftsQuery(options.Query); match != "" {
		params["match"] = match
		query = `
			SELECT n.data_json
			FROM state_notes n
			WHERE ({:include_archived} OR n.archived = 0)
				AND n.id IN (SELECT note_id FROM state_note_search WHERE state_note_search MATCH {:match})
			ORDER BY n.updated_at DESC, n.id DESC
			LIMIT {:limit}`
	}
	if err := repository.app.DB().NewQuery(query).Bind(params).All(&rows); err != nil {
		return nil, fmt.Errorf("list notes: %w", err)
	}
	notes := make([]state.Note, 0, len(rows))
	for _, row := range rows {
		var note state.Note
		if err := json.Unmarshal([]byte(row.DataJSON), &note); err != nil {
			return nil, fmt.Errorf("decode note: %w", err)
		}
		notes = append(notes, note)
	}
	return notes, nil
}

func (repository *PocketBaseRepository) ListNoteAuditEvents(_ context.Context, noteID string) ([]state.AuditEvent, error) {
	rows := make([]struct {
		EventJSON string `db:"event_json"`
	}, 0)
	err := repository.app.DB().NewQuery(`
		SELECT event_json
		FROM state_audit_events
		WHERE json_extract(event_json, '$.note_id') = {:note_id}
		ORDER BY sequence ASC
	`).Bind(dbx.Params{"note_id": noteID}).All(&rows)
	if err != nil {
		return nil, fmt.Errorf("list note audit events: %w", err)
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

// recordNoteChange writes the audit event, refreshes the search document and
// stores the idempotent result, all inside the caller's transaction.
func (repository *PocketBaseRepository) recordNoteChange(txApp core.App, note state.Note, event state.AuditEvent, clientRequestID string) error {
	sealed, err := repository.sealAuditEvent(txApp, event)
	if err != nil {
		return err
	}
	if err := insertAuditEvent(txApp, sealed); err != nil {
		return err
	}
	if _, err := txApp.DB().NewQuery(`DELETE FROM state_note_search WHERE note_id = {:note_id}`).
		Bind(dbx.Params{"note_id": note.ID}).Execute(); err != nil {
		return fmt.Errorf("remove note search document: %w", err)
	}
	if _, err := txApp.DB().NewQuery(`
		INSERT INTO state_note_search (note_id, content) VALUES ({:note_id}, {:content})
	`).Bind(dbx.Params{
		"note_id": note.ID,
		"content": note.Title + "\n" + note.Summary + "\n" + note.PlainText,
	}).Execute(); err != nil {
		return fmt.Errorf("insert note search document: %w", err)
	}
	return insertIdempotencyValue(txApp, clientRequestID, event.Actor.ID, "note", note.ID, note)
}

func getNote(app core.App, noteID string) (state.Note, error) {
	row := struct {
		DataJSON string `db:"data_json"`
	}{}
	err := app.DB().NewQuery(`SELECT data_json FROM state_notes WHERE id = {:id}`).
		Bind(dbx.Params{"id": noteID}).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.Note{}, state.ErrNotFound
	}
	if err != nil {
		return state.Note{}, fmt.Errorf("read note: %w", err)
	}
	var note state.Note
	if err := json.Unmarshal([]byte(row.DataJSON), &note); err != nil {
		return state.Note{}, fmt.Errorf("decode note: %w", err)
	}
	return note, nil
}

func noteParams(note state.Note, noteJSON []byte) dbx.Params {
	return dbx.Params{
		"id":         note.ID,
		"title":      note.Title,
		"archived":   note.Archived,
		"revision":   note.Revision,
		"created_at": formatTime(note.CreatedAt),
		"updated_at": formatTime(note.UpdatedAt),
		"data_json":  string(noteJSON),
	}
}
