package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nicremo/state/internal/state"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// The notes AI keeps its results next to the notes: attachments with their
// OCR or transcript, the AI's title and summary, related-note links,
// reminder proposals, processing jobs, settings and monthly spending. None of
// it changes a note's revision.
var notesAISchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS state_note_attachments (
		id TEXT PRIMARY KEY,
		note_id TEXT NOT NULL,
		ordinal INTEGER NOT NULL,
		sha256 TEXT NOT NULL,
		data_json TEXT NOT NULL CHECK(json_valid(data_json))
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS state_note_attachments_note_idx ON state_note_attachments(note_id, ordinal)`,
	`CREATE TABLE IF NOT EXISTS state_note_ai (
		note_id TEXT PRIMARY KEY,
		data_json TEXT NOT NULL CHECK(json_valid(data_json))
	) STRICT`,
	`CREATE TABLE IF NOT EXISTS state_note_relations (
		id TEXT PRIMARY KEY,
		note_id TEXT NOT NULL,
		related_note_id TEXT NOT NULL,
		data_json TEXT NOT NULL CHECK(json_valid(data_json))
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS state_note_relations_note_idx ON state_note_relations(note_id)`,
	`CREATE INDEX IF NOT EXISTS state_note_relations_related_idx ON state_note_relations(related_note_id)`,
	`CREATE TABLE IF NOT EXISTS state_note_proposals (
		id TEXT PRIMARY KEY,
		note_id TEXT NOT NULL,
		data_json TEXT NOT NULL CHECK(json_valid(data_json))
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS state_note_proposals_note_idx ON state_note_proposals(note_id)`,
	`CREATE TABLE IF NOT EXISTS state_note_jobs (
		id TEXT PRIMARY KEY,
		note_id TEXT NOT NULL,
		status TEXT NOT NULL,
		not_before INTEGER NOT NULL,
		data_json TEXT NOT NULL CHECK(json_valid(data_json))
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS state_note_jobs_status_idx ON state_note_jobs(status, not_before)`,
	`CREATE TABLE IF NOT EXISTS state_ai_settings (
		id INTEGER PRIMARY KEY CHECK(id = 1),
		data_json TEXT NOT NULL CHECK(json_valid(data_json))
	) STRICT`,
	`CREATE TABLE IF NOT EXISTS state_notes_dictionary (
		id INTEGER PRIMARY KEY CHECK(id = 1),
		data_json TEXT NOT NULL CHECK(json_valid(data_json))
	) STRICT`,
	`CREATE TABLE IF NOT EXISTS state_ai_usage (
		month TEXT PRIMARY KEY,
		cost_usd REAL NOT NULL,
		requests INTEGER NOT NULL
	) STRICT`,
}

// noteAIRecord is what state_note_ai stores per note.
type noteAIRecord struct {
	Processing state.NoteProcessing `json:"processing"`
	Result     *state.NoteAIResult  `json:"result,omitempty"`
}

func (repository *PocketBaseRepository) ensureNotesAISchema() error {
	return repository.app.RunInTransaction(func(txApp core.App) error {
		for _, statement := range notesAISchemaStatements {
			if _, err := txApp.DB().NewQuery(statement).Execute(); err != nil {
				return fmt.Errorf("create notes AI schema: %w", err)
			}
		}
		return nil
	})
}

func (repository *PocketBaseRepository) GetNoteAIState(_ context.Context, noteID string) (state.NoteAIState, error) {
	if _, err := getNote(repository.app, noteID); err != nil {
		return state.NoteAIState{}, err
	}
	return getNoteAIState(repository.app, noteID)
}

func getNoteAIState(app core.App, noteID string) (state.NoteAIState, error) {
	var result state.NoteAIState
	record, err := getNoteAIRecord(app, noteID)
	if err != nil {
		return state.NoteAIState{}, err
	}
	result.Processing = record.Processing
	result.Result = record.Result
	if err := decodeRows(app, `SELECT data_json FROM state_note_attachments WHERE note_id = {:id} ORDER BY ordinal, id`, noteID, &result.Attachments); err != nil {
		return state.NoteAIState{}, err
	}
	if err := decodeRows(app, `SELECT data_json FROM state_note_relations WHERE note_id = {:id} OR related_note_id = {:id} ORDER BY id`, noteID, &result.Relations); err != nil {
		return state.NoteAIState{}, err
	}
	if err := decodeRows(app, `SELECT data_json FROM state_note_proposals WHERE note_id = {:id} ORDER BY id`, noteID, &result.Proposals); err != nil {
		return state.NoteAIState{}, err
	}
	return result, nil
}

func getNoteAIRecord(app core.App, noteID string) (noteAIRecord, error) {
	row := struct {
		DataJSON string `db:"data_json"`
	}{}
	err := app.DB().NewQuery(`SELECT data_json FROM state_note_ai WHERE note_id = {:id}`).Bind(dbx.Params{"id": noteID}).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return noteAIRecord{}, nil
	}
	if err != nil {
		return noteAIRecord{}, fmt.Errorf("read note AI data: %w", err)
	}
	var record noteAIRecord
	if err := json.Unmarshal([]byte(row.DataJSON), &record); err != nil {
		return noteAIRecord{}, fmt.Errorf("decode note AI data: %w", err)
	}
	return record, nil
}

// decodeRows reads data_json rows into a slice of T.
func decodeRows[T any](app core.App, query string, noteID string, target *[]T) error {
	rows := make([]struct {
		DataJSON string `db:"data_json"`
	}, 0)
	if err := app.DB().NewQuery(query).Bind(dbx.Params{"id": noteID}).All(&rows); err != nil {
		return fmt.Errorf("read note AI rows: %w", err)
	}
	for _, row := range rows {
		var value T
		if err := json.Unmarshal([]byte(row.DataJSON), &value); err != nil {
			return fmt.Errorf("decode note AI row: %w", err)
		}
		*target = append(*target, value)
	}
	return nil
}

func (repository *PocketBaseRepository) AddNoteAttachment(_ context.Context, attachment state.NoteAttachment, event state.AuditEvent, clientRequestID string) (state.NoteAttachment, error) {
	var result state.NoteAttachment
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		var existing state.NoteAttachment
		if _, found, err := lookupIdempotentValue(txApp, clientRequestID, event.Actor.ID, "note_attachment", &existing); err != nil {
			return err
		} else if found {
			result = existing
			return nil
		}
		if _, err := getNote(txApp, attachment.NoteID); err != nil {
			return err
		}
		encoded, err := json.Marshal(attachment)
		if err != nil {
			return fmt.Errorf("encode attachment: %w", err)
		}
		if _, err := txApp.DB().NewQuery(`
			INSERT INTO state_note_attachments (id, note_id, ordinal, sha256, data_json)
			VALUES ({:id}, {:note_id}, {:ordinal}, {:sha256}, {:data_json})
		`).Bind(dbx.Params{
			"id": attachment.ID, "note_id": attachment.NoteID, "ordinal": attachment.Ordinal,
			"sha256": attachment.SHA256, "data_json": string(encoded),
		}).Execute(); err != nil {
			return fmt.Errorf("insert attachment: %w", err)
		}
		if err := repository.appendNoteEvent(txApp, attachment.NoteID, event); err != nil {
			return err
		}
		result = attachment
		return insertIdempotencyValue(txApp, clientRequestID, event.Actor.ID, "note_attachment", attachment.ID, attachment)
	})
	if err != nil {
		return state.NoteAttachment{}, err
	}
	return result, nil
}

func (repository *PocketBaseRepository) LookupNoteAttachmentRequest(_ context.Context, clientRequestID string, actorID string) (state.NoteAttachment, bool, error) {
	var attachment state.NoteAttachment
	_, found, err := lookupIdempotentValue(repository.app, clientRequestID, actorID, "note_attachment", &attachment)
	if err != nil || !found {
		return state.NoteAttachment{}, false, err
	}
	return attachment, true, nil
}

func (repository *PocketBaseRepository) SaveNoteAIChange(_ context.Context, noteID string, change state.NoteAIChange, event state.AuditEvent) error {
	return repository.app.RunInTransaction(func(txApp core.App) error {
		if _, err := getNote(txApp, noteID); err != nil {
			return err
		}
		if change.Processing != nil || change.Result != nil {
			record, err := getNoteAIRecord(txApp, noteID)
			if err != nil {
				return err
			}
			if change.Processing != nil {
				record.Processing = *change.Processing
			}
			if change.Result != nil {
				result := *change.Result
				record.Result = &result
			}
			encoded, err := json.Marshal(record)
			if err != nil {
				return fmt.Errorf("encode note AI data: %w", err)
			}
			if _, err := txApp.DB().NewQuery(`
				INSERT INTO state_note_ai (note_id, data_json) VALUES ({:id}, {:data_json})
				ON CONFLICT(note_id) DO UPDATE SET data_json = excluded.data_json
			`).Bind(dbx.Params{"id": noteID, "data_json": string(encoded)}).Execute(); err != nil {
				return fmt.Errorf("store note AI data: %w", err)
			}
		}
		if len(change.AttachmentTexts) > 0 {
			var attachments []state.NoteAttachment
			if err := decodeRows(txApp, `SELECT data_json FROM state_note_attachments WHERE note_id = {:id}`, noteID, &attachments); err != nil {
				return err
			}
			for _, attachment := range attachments {
				text, ok := change.AttachmentTexts[attachment.ID]
				if !ok {
					continue
				}
				attachment.DerivedText, attachment.DerivedKind, attachment.DerivedModel = text.Text, text.Kind, text.Model
				if err := updateDataJSON(txApp, "state_note_attachments", attachment.ID, attachment); err != nil {
					return err
				}
			}
		}
		for _, relation := range change.AddRelations {
			encoded, err := json.Marshal(relation)
			if err != nil {
				return fmt.Errorf("encode relation: %w", err)
			}
			if _, err := txApp.DB().NewQuery(`
				INSERT INTO state_note_relations (id, note_id, related_note_id, data_json)
				VALUES ({:id}, {:note_id}, {:related_note_id}, {:data_json})
			`).Bind(dbx.Params{"id": relation.ID, "note_id": relation.NoteID, "related_note_id": relation.RelatedNoteID, "data_json": string(encoded)}).Execute(); err != nil {
				return fmt.Errorf("insert relation: %w", err)
			}
		}
		for _, relationID := range change.ArchiveRelationIDs {
			if _, err := txApp.DB().NewQuery(`
				UPDATE state_note_relations SET data_json = json_set(data_json, '$.archived', json('true')) WHERE id = {:id}
			`).Bind(dbx.Params{"id": relationID}).Execute(); err != nil {
				return fmt.Errorf("archive relation: %w", err)
			}
		}
		for _, proposal := range change.AddProposals {
			encoded, err := json.Marshal(proposal)
			if err != nil {
				return fmt.Errorf("encode proposal: %w", err)
			}
			if _, err := txApp.DB().NewQuery(`
				INSERT INTO state_note_proposals (id, note_id, data_json) VALUES ({:id}, {:note_id}, {:data_json})
			`).Bind(dbx.Params{"id": proposal.ID, "note_id": proposal.NoteID, "data_json": string(encoded)}).Execute(); err != nil {
				return fmt.Errorf("insert proposal: %w", err)
			}
		}
		for _, proposal := range change.UpdateProposals {
			if err := updateDataJSON(txApp, "state_note_proposals", proposal.ID, proposal); err != nil {
				return err
			}
		}
		return repository.appendNoteEvent(txApp, noteID, event)
	})
}

func updateDataJSON(app core.App, table string, id string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s row: %w", table, err)
	}
	// The table name is one of this file's constants, never user input.
	if _, err := app.DB().NewQuery(`UPDATE ` + table + ` SET data_json = {:data_json} WHERE id = {:id}`).
		Bind(dbx.Params{"id": id, "data_json": string(encoded)}).Execute(); err != nil {
		return fmt.Errorf("update %s row: %w", table, err)
	}
	return nil
}

// appendNoteEvent seals the audit event and refreshes the note's search
// document, so OCR text and transcripts become searchable at once.
func (repository *PocketBaseRepository) appendNoteEvent(txApp core.App, noteID string, event state.AuditEvent) error {
	sealed, err := repository.sealAuditEvent(txApp, event)
	if err != nil {
		return err
	}
	if err := insertAuditEvent(txApp, sealed); err != nil {
		return err
	}
	note, err := getNote(txApp, noteID)
	if err != nil {
		return err
	}
	return refreshNoteSearch(txApp, note)
}

// refreshNoteSearch rewrites a note's search document from its own text and
// everything the AI derived from it.
func refreshNoteSearch(txApp core.App, note state.Note) error {
	aiState, err := getNoteAIState(txApp, note.ID)
	if err != nil {
		return err
	}
	parts := []string{note.Title, note.Summary, note.PlainText}
	if aiState.Result != nil {
		parts = append(parts, aiState.Result.Title, aiState.Result.Summary)
	}
	for _, attachment := range aiState.Attachments {
		parts = append(parts, attachment.DerivedText)
	}
	if _, err := txApp.DB().NewQuery(`DELETE FROM state_note_search WHERE note_id = {:note_id}`).
		Bind(dbx.Params{"note_id": note.ID}).Execute(); err != nil {
		return fmt.Errorf("remove note search document: %w", err)
	}
	if _, err := txApp.DB().NewQuery(`INSERT INTO state_note_search (note_id, content) VALUES ({:note_id}, {:content})`).
		Bind(dbx.Params{"note_id": note.ID, "content": strings.Join(parts, "\n")}).Execute(); err != nil {
		return fmt.Errorf("insert note search document: %w", err)
	}
	return nil
}

// EnqueueNoteJob keeps one waiting job per note: a processing request
// replaces a waiting organize pass, another organize pass moves the waiting
// one later (typing is debounced), and nothing ever runs twice at once.
func (repository *PocketBaseRepository) EnqueueNoteJob(_ context.Context, job state.NoteJob) error {
	return repository.app.RunInTransaction(func(txApp core.App) error {
		var waiting []state.NoteJob
		if err := decodeRows(txApp, `SELECT data_json FROM state_note_jobs WHERE note_id = {:id} AND status = 'queued'`, job.NoteID, &waiting); err != nil {
			return err
		}
		if len(waiting) > 0 {
			existing := waiting[0]
			if existing.Kind == state.NoteJobProcess && job.Kind == state.NoteJobOrganize {
				return nil
			}
			existing.Kind = job.Kind
			existing.NotBefore = job.NotBefore
			return storeNoteJob(txApp, existing)
		}
		return storeNoteJob(txApp, job)
	})
}

func storeNoteJob(app core.App, job state.NoteJob) error {
	encoded, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("encode note job: %w", err)
	}
	if _, err := app.DB().NewQuery(`
		INSERT INTO state_note_jobs (id, note_id, status, not_before, data_json)
		VALUES ({:id}, {:note_id}, {:status}, {:not_before}, {:data_json})
		ON CONFLICT(id) DO UPDATE SET status = excluded.status, not_before = excluded.not_before, data_json = excluded.data_json
	`).Bind(dbx.Params{
		"id": job.ID, "note_id": job.NoteID, "status": job.Status,
		"not_before": job.NotBefore.UTC().UnixNano(), "data_json": string(encoded),
	}).Execute(); err != nil {
		return fmt.Errorf("store note job: %w", err)
	}
	return nil
}

func (repository *PocketBaseRepository) ClaimNoteJob(_ context.Context, now time.Time) (state.NoteJob, bool, error) {
	var claimed state.NoteJob
	found := false
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		row := struct {
			DataJSON string `db:"data_json"`
		}{}
		err := txApp.DB().NewQuery(`
			SELECT data_json FROM state_note_jobs
			WHERE status = 'queued' AND not_before <= {:now}
			ORDER BY not_before, id LIMIT 1
		`).Bind(dbx.Params{"now": now.UTC().UnixNano()}).One(&row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("claim note job: %w", err)
		}
		if err := json.Unmarshal([]byte(row.DataJSON), &claimed); err != nil {
			return fmt.Errorf("decode note job: %w", err)
		}
		claimed.Status = state.NoteJobRunning
		found = true
		return storeNoteJob(txApp, claimed)
	})
	if err != nil || !found {
		return state.NoteJob{}, false, err
	}
	return claimed, true, nil
}

func (repository *PocketBaseRepository) FinishNoteJob(_ context.Context, job state.NoteJob) error {
	return storeNoteJob(repository.app, job)
}

func (repository *PocketBaseRepository) RequeueRunningNoteJobs(_ context.Context) error {
	return repository.app.RunInTransaction(func(txApp core.App) error {
		rows := make([]struct {
			DataJSON string `db:"data_json"`
		}, 0)
		if err := txApp.DB().NewQuery(`SELECT data_json FROM state_note_jobs WHERE status = 'running'`).All(&rows); err != nil {
			return fmt.Errorf("list running note jobs: %w", err)
		}
		for _, row := range rows {
			var job state.NoteJob
			if err := json.Unmarshal([]byte(row.DataJSON), &job); err != nil {
				return fmt.Errorf("decode note job: %w", err)
			}
			job.Status = state.NoteJobQueued
			if err := storeNoteJob(txApp, job); err != nil {
				return err
			}
		}
		return nil
	})
}

func (repository *PocketBaseRepository) GetNoteAISettings(_ context.Context) (state.NoteAISettings, bool, error) {
	row := struct {
		DataJSON string `db:"data_json"`
	}{}
	err := repository.app.DB().NewQuery(`SELECT data_json FROM state_ai_settings WHERE id = 1`).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.NoteAISettings{}, false, nil
	}
	if err != nil {
		return state.NoteAISettings{}, false, fmt.Errorf("read AI settings: %w", err)
	}
	var settings state.NoteAISettings
	if err := json.Unmarshal([]byte(row.DataJSON), &settings); err != nil {
		return state.NoteAISettings{}, false, fmt.Errorf("decode AI settings: %w", err)
	}
	return settings, true, nil
}

func (repository *PocketBaseRepository) SaveNoteAISettings(_ context.Context, settings state.NoteAISettings, event state.AuditEvent) error {
	return repository.app.RunInTransaction(func(txApp core.App) error {
		encoded, err := json.Marshal(settings)
		if err != nil {
			return fmt.Errorf("encode AI settings: %w", err)
		}
		if _, err := txApp.DB().NewQuery(`
			INSERT INTO state_ai_settings (id, data_json) VALUES (1, {:data_json})
			ON CONFLICT(id) DO UPDATE SET data_json = excluded.data_json
		`).Bind(dbx.Params{"data_json": string(encoded)}).Execute(); err != nil {
			return fmt.Errorf("store AI settings: %w", err)
		}
		sealed, err := repository.sealAuditEvent(txApp, event)
		if err != nil {
			return err
		}
		return insertAuditEvent(txApp, sealed)
	})
}

func (repository *PocketBaseRepository) AddNoteAIUsage(_ context.Context, month string, costUSD float64) error {
	if _, err := repository.app.DB().NewQuery(`
		INSERT INTO state_ai_usage (month, cost_usd, requests) VALUES ({:month}, {:cost}, 1)
		ON CONFLICT(month) DO UPDATE SET cost_usd = cost_usd + excluded.cost_usd, requests = requests + 1
	`).Bind(dbx.Params{"month": month, "cost": costUSD}).Execute(); err != nil {
		return fmt.Errorf("record AI usage: %w", err)
	}
	return nil
}

func (repository *PocketBaseRepository) NoteAIUsage(_ context.Context, month string) (float64, error) {
	row := struct {
		CostUSD float64 `db:"cost_usd"`
	}{}
	err := repository.app.DB().NewQuery(`SELECT cost_usd FROM state_ai_usage WHERE month = {:month}`).
		Bind(dbx.Params{"month": month}).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read AI usage: %w", err)
	}
	return row.CostUSD, nil
}

func (repository *PocketBaseRepository) GetNotesDictionary(_ context.Context) (state.NotesDictionary, bool, error) {
	row := struct {
		DataJSON string `db:"data_json"`
	}{}
	err := repository.app.DB().NewQuery(`SELECT data_json FROM state_notes_dictionary WHERE id = 1`).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.NotesDictionary{}, false, nil
	}
	if err != nil {
		return state.NotesDictionary{}, false, fmt.Errorf("read notes dictionary: %w", err)
	}
	var dictionary state.NotesDictionary
	if err := json.Unmarshal([]byte(row.DataJSON), &dictionary); err != nil {
		return state.NotesDictionary{}, false, fmt.Errorf("decode notes dictionary: %w", err)
	}
	return dictionary, true, nil
}

// SaveNotesDictionary stores the dictionary with its audit event. The
// revision check runs inside the transaction, so two devices saving at once
// cannot both win.
func (repository *PocketBaseRepository) SaveNotesDictionary(_ context.Context, dictionary state.NotesDictionary, event state.AuditEvent) error {
	return repository.app.RunInTransaction(func(txApp core.App) error {
		row := struct {
			DataJSON string `db:"data_json"`
		}{}
		var stored state.NotesDictionary
		err := txApp.DB().NewQuery(`SELECT data_json FROM state_notes_dictionary WHERE id = 1`).One(&row)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return fmt.Errorf("read notes dictionary: %w", err)
		default:
			if err := json.Unmarshal([]byte(row.DataJSON), &stored); err != nil {
				return fmt.Errorf("decode notes dictionary: %w", err)
			}
		}
		if stored.Revision != dictionary.Revision-1 {
			return state.ErrRevisionConflict
		}
		encoded, err := json.Marshal(dictionary)
		if err != nil {
			return fmt.Errorf("encode notes dictionary: %w", err)
		}
		if _, err := txApp.DB().NewQuery(`
			INSERT INTO state_notes_dictionary (id, data_json) VALUES (1, {:data_json})
			ON CONFLICT(id) DO UPDATE SET data_json = excluded.data_json
		`).Bind(dbx.Params{"data_json": string(encoded)}).Execute(); err != nil {
			return fmt.Errorf("store notes dictionary: %w", err)
		}
		sealed, err := repository.sealAuditEvent(txApp, event)
		if err != nil {
			return err
		}
		return insertAuditEvent(txApp, sealed)
	})
}
