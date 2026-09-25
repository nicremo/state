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

// Agent sessions group rounds; each round is a row in state_runs whose
// data_json carries the session ID.
var agentSessionSchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS state_agent_sessions (
		id TEXT PRIMARY KEY,
		reminder_id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		data_json TEXT NOT NULL CHECK(json_valid(data_json))
	) STRICT`,
	`CREATE INDEX IF NOT EXISTS state_agent_sessions_created_idx ON state_agent_sessions(created_at)`,
	`CREATE INDEX IF NOT EXISTS state_runs_session_idx ON state_runs(json_extract(data_json, '$.session_id'))`,
}

func (repository *PocketBaseRepository) ensureAgentSessionSchema() error {
	return repository.app.RunInTransaction(func(txApp core.App) error {
		for _, statement := range agentSessionSchemaStatements {
			if _, err := txApp.DB().NewQuery(statement).Execute(); err != nil {
				return fmt.Errorf("create agent session schema: %w", err)
			}
		}
		return nil
	})
}

func (repository *PocketBaseRepository) CreateAgentSession(_ context.Context, session state.AgentSession, event state.AuditEvent, first state.AgentRun, firstEvent state.AuditEvent, clientRequestID string) (state.AgentSession, bool, error) {
	var result state.AgentSession
	created := false
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		if clientRequestID != "" {
			existing, found, err := lookupIdempotentValue(txApp, clientRequestID, event.Actor.ID, "agent_session", &state.AgentSession{})
			if err != nil {
				return err
			}
			if found {
				result = *(existing.(*state.AgentSession))
				return nil
			}
		}
		if _, err := getReminder(txApp, session.ReminderID); err != nil {
			return err
		}
		encoded, err := json.Marshal(session)
		if err != nil {
			return fmt.Errorf("encode agent session: %w", err)
		}
		if _, err := txApp.DB().NewQuery(`
			INSERT INTO state_agent_sessions (id, reminder_id, created_at, data_json)
			VALUES ({:id}, {:reminder_id}, {:created_at}, {:data_json})
		`).Bind(dbx.Params{"id": session.ID, "reminder_id": session.ReminderID, "created_at": formatTime(session.CreatedAt), "data_json": string(encoded)}).Execute(); err != nil {
			return fmt.Errorf("insert agent session: %w", err)
		}
		if _, err := insertRunRow(txApp, first); err != nil {
			return err
		}
		if err := repository.appendSessionEvent(txApp, event); err != nil {
			return err
		}
		if err := repository.appendSessionEvent(txApp, firstEvent, runSearchExtras(first)...); err != nil {
			return err
		}
		if clientRequestID != "" {
			if err := insertIdempotencyValue(txApp, clientRequestID, event.Actor.ID, "agent_session", session.ID, session); err != nil {
				return err
			}
		}
		result = session
		created = true
		return nil
	})
	if err != nil {
		return state.AgentSession{}, false, err
	}
	return result, created, nil
}

// CreateAgentSessionTurn checks inside the transaction that the session is
// open and has no round in progress, so two devices cannot start two rounds.
func (repository *PocketBaseRepository) CreateAgentSessionTurn(_ context.Context, run state.AgentRun, event state.AuditEvent, clientRequestID string) (state.AgentRun, bool, error) {
	var result state.AgentRun
	created := false
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		if clientRequestID != "" {
			existing, found, err := lookupIdempotentValue(txApp, clientRequestID, event.Actor.ID, "agent_run", &state.AgentRun{})
			if err != nil {
				return err
			}
			if found {
				result = *(existing.(*state.AgentRun))
				return nil
			}
		}
		session, err := getAgentSession(txApp, run.SessionID)
		if err != nil {
			return err
		}
		if session.Closed {
			return state.ErrRunStateConflict
		}
		active := struct {
			Count int `db:"count"`
		}{}
		if err := txApp.DB().NewQuery(`
			SELECT COUNT(*) AS count FROM state_runs
			WHERE json_extract(data_json, '$.session_id') = {:session_id}
			AND status NOT IN ('succeeded', 'failed', 'cancelled', 'expired')
		`).Bind(dbx.Params{"session_id": run.SessionID}).One(&active); err != nil {
			return fmt.Errorf("count active session rounds: %w", err)
		}
		if active.Count > 0 {
			return state.ErrRunStateConflict
		}
		if _, err := insertRunRow(txApp, run); err != nil {
			return err
		}
		if err := repository.appendSessionEvent(txApp, event, runSearchExtras(run)...); err != nil {
			return err
		}
		if clientRequestID != "" {
			if err := insertIdempotencyValue(txApp, clientRequestID, event.Actor.ID, "agent_run", run.ID, run); err != nil {
				return err
			}
		}
		result = run
		created = true
		return nil
	})
	if err != nil {
		return state.AgentRun{}, false, err
	}
	return result, created, nil
}

func (repository *PocketBaseRepository) appendSessionEvent(txApp core.App, event state.AuditEvent, extras ...string) error {
	sealed, err := repository.sealAuditEvent(txApp, event)
	if err != nil {
		return err
	}
	if err := insertAuditEvent(txApp, sealed); err != nil {
		return err
	}
	return insertAuditSearch(txApp, sealed, extras...)
}

func (repository *PocketBaseRepository) GetAgentSession(_ context.Context, sessionID string) (state.AgentSession, error) {
	return getAgentSession(repository.app, sessionID)
}

func getAgentSession(app core.App, sessionID string) (state.AgentSession, error) {
	row := struct {
		DataJSON string `db:"data_json"`
	}{}
	err := app.DB().NewQuery(`SELECT data_json FROM state_agent_sessions WHERE id = {:id}`).Bind(dbx.Params{"id": sessionID}).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.AgentSession{}, state.ErrNotFound
	}
	if err != nil {
		return state.AgentSession{}, fmt.Errorf("read agent session: %w", err)
	}
	var session state.AgentSession
	if err := json.Unmarshal([]byte(row.DataJSON), &session); err != nil {
		return state.AgentSession{}, fmt.Errorf("decode agent session: %w", err)
	}
	return session, nil
}

func (repository *PocketBaseRepository) ListAgentSessions(_ context.Context, limit int) ([]state.AgentSession, error) {
	rows := make([]struct {
		DataJSON string `db:"data_json"`
	}, 0)
	if err := repository.app.DB().NewQuery(`
		SELECT data_json FROM state_agent_sessions ORDER BY created_at DESC, id DESC LIMIT {:limit}
	`).Bind(dbx.Params{"limit": normalizeLimit(limit)}).All(&rows); err != nil {
		return nil, fmt.Errorf("list agent sessions: %w", err)
	}
	sessions := make([]state.AgentSession, 0, len(rows))
	for _, row := range rows {
		var session state.AgentSession
		if err := json.Unmarshal([]byte(row.DataJSON), &session); err != nil {
			return nil, fmt.Errorf("decode agent session: %w", err)
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func (repository *PocketBaseRepository) UpdateAgentSession(_ context.Context, session state.AgentSession, expectedRevision int64, event state.AuditEvent, clientRequestID string) (state.AgentSession, error) {
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		current, err := getAgentSession(txApp, session.ID)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return state.ErrRevisionConflict
		}
		encoded, err := json.Marshal(session)
		if err != nil {
			return fmt.Errorf("encode agent session: %w", err)
		}
		if _, err := txApp.DB().NewQuery(`UPDATE state_agent_sessions SET data_json = {:data_json} WHERE id = {:id}`).
			Bind(dbx.Params{"id": session.ID, "data_json": string(encoded)}).Execute(); err != nil {
			return fmt.Errorf("update agent session: %w", err)
		}
		return repository.appendSessionEvent(txApp, event)
	})
	if err != nil {
		return state.AgentSession{}, err
	}
	return session, nil
}
