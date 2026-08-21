package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nicremo/state/internal/state"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// This file holds the execution-plane persistence: projects, execution
// policies, runners and agent runs. Every mutating method follows the
// CreateReminder archetype: idempotency lookup first, one transaction,
// RowsAffected == 1 CAS, sealAuditEvent + insertAuditEvent, and a search row
// where the content is useful.

func (repository *PocketBaseRepository) CreateProject(
	_ context.Context,
	project state.Project,
	event state.AuditEvent,
	clientRequestID string,
) (state.Project, error) {
	var result state.Project
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		existing, found, err := lookupIdempotentValue(txApp, clientRequestID, event.Actor.ID, "project", &state.Project{})
		if err != nil {
			return err
		}
		if found {
			result = *(existing.(*state.Project))
			return nil
		}
		projectJSON, err := json.Marshal(project)
		if err != nil {
			return fmt.Errorf("encode project: %w", err)
		}
		_, err = txApp.DB().NewQuery(`
			INSERT INTO state_projects (
				id, name, revision, created_at, updated_at, data_json
			) VALUES (
				{:id}, {:name}, {:revision}, {:created_at}, {:updated_at}, {:data_json}
			)
		`).Bind(dbx.Params{
			"id":         project.ID,
			"name":       project.Name,
			"revision":   project.Revision,
			"created_at": formatTime(project.CreatedAt),
			"updated_at": formatTime(project.UpdatedAt),
			"data_json":  string(projectJSON),
		}).Execute()
		if err != nil {
			return fmt.Errorf("insert project: %w", err)
		}
		sealedEvent, err := repository.sealAuditEvent(txApp, event)
		if err != nil {
			return err
		}
		if err := insertAuditEvent(txApp, sealedEvent); err != nil {
			return err
		}
		if err := insertAuditSearch(txApp, sealedEvent, project.Name); err != nil {
			return err
		}
		if err := insertIdempotencyValue(txApp, clientRequestID, event.Actor.ID, "project", project.ID, project); err != nil {
			return err
		}
		result = project
		return nil
	})
	if err != nil {
		return state.Project{}, err
	}
	return result, nil
}

func (repository *PocketBaseRepository) UpdateProject(
	_ context.Context,
	project state.Project,
	expectedRevision int64,
	event state.AuditEvent,
	clientRequestID string,
) (state.Project, error) {
	var result state.Project
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		existing, found, err := lookupIdempotentValue(txApp, clientRequestID, event.Actor.ID, "project", &state.Project{})
		if err != nil {
			return err
		}
		if found {
			result = *(existing.(*state.Project))
			return nil
		}
		current, err := getProject(txApp, project.ID)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return state.ErrRevisionConflict
		}
		projectJSON, err := json.Marshal(project)
		if err != nil {
			return fmt.Errorf("encode project: %w", err)
		}
		databaseResult, err := txApp.DB().NewQuery(`
			UPDATE state_projects
			SET name = {:name},
				revision = {:new_revision},
				updated_at = {:updated_at},
				data_json = {:data_json}
			WHERE id = {:id} AND revision = {:expected_revision}
		`).Bind(dbx.Params{
			"id":                project.ID,
			"name":              project.Name,
			"new_revision":      project.Revision,
			"updated_at":        formatTime(project.UpdatedAt),
			"data_json":         string(projectJSON),
			"expected_revision": expectedRevision,
		}).Execute()
		if err != nil {
			return fmt.Errorf("update project: %w", err)
		}
		if err := requireOneRow(databaseResult); err != nil {
			return err
		}
		sealedEvent, err := repository.sealAuditEvent(txApp, event)
		if err != nil {
			return err
		}
		if err := insertAuditEvent(txApp, sealedEvent); err != nil {
			return err
		}
		if err := insertAuditSearch(txApp, sealedEvent, project.Name); err != nil {
			return err
		}
		if err := insertIdempotencyValue(txApp, clientRequestID, event.Actor.ID, "project", project.ID, project); err != nil {
			return err
		}
		result = project
		return nil
	})
	if err != nil {
		return state.Project{}, err
	}
	return result, nil
}

func (repository *PocketBaseRepository) GetProject(_ context.Context, projectID string) (state.Project, error) {
	return getProject(repository.app, projectID)
}

func (repository *PocketBaseRepository) ListProjects(_ context.Context) ([]state.Project, error) {
	rows := make([]struct {
		DataJSON string `db:"data_json"`
	}, 0)
	err := repository.app.DB().NewQuery(`
		SELECT data_json FROM state_projects
		ORDER BY name ASC, id ASC
	`).All(&rows)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	projects := make([]state.Project, 0, len(rows))
	for _, row := range rows {
		var project state.Project
		if err := json.Unmarshal([]byte(row.DataJSON), &project); err != nil {
			return nil, fmt.Errorf("decode project: %w", err)
		}
		projects = append(projects, project)
	}
	return projects, nil
}

func (repository *PocketBaseRepository) CreatePolicy(
	_ context.Context,
	policy state.ExecutionPolicy,
	event state.AuditEvent,
	clientRequestID string,
) (state.ExecutionPolicy, error) {
	var result state.ExecutionPolicy
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		existing, found, err := lookupIdempotentValue(txApp, clientRequestID, event.Actor.ID, "policy", &state.ExecutionPolicy{})
		if err != nil {
			return err
		}
		if found {
			result = *(existing.(*state.ExecutionPolicy))
			return nil
		}
		if _, err := getProject(txApp, policy.ProjectID); err != nil {
			return err
		}
		if err := insertPolicyRow(txApp, policy); err != nil {
			return err
		}
		sealedEvent, err := repository.sealAuditEvent(txApp, event)
		if err != nil {
			return err
		}
		if err := insertAuditEvent(txApp, sealedEvent); err != nil {
			return err
		}
		if err := insertAuditSearch(txApp, sealedEvent, policySearchExtras(txApp, policy)...); err != nil {
			return err
		}
		if err := insertIdempotencyValue(txApp, clientRequestID, event.Actor.ID, "policy", policy.ID, policy); err != nil {
			return err
		}
		result = policy
		return nil
	})
	if err != nil {
		return state.ExecutionPolicy{}, err
	}
	return result, nil
}

func (repository *PocketBaseRepository) UpdatePolicy(
	_ context.Context,
	policy state.ExecutionPolicy,
	expectedRevision int64,
	event state.AuditEvent,
	clientRequestID string,
) (state.ExecutionPolicy, error) {
	var result state.ExecutionPolicy
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		existing, found, err := lookupIdempotentValue(txApp, clientRequestID, event.Actor.ID, "policy", &state.ExecutionPolicy{})
		if err != nil {
			return err
		}
		if found {
			result = *(existing.(*state.ExecutionPolicy))
			return nil
		}
		current, err := getPolicy(txApp, policy.ID)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return state.ErrRevisionConflict
		}
		policyJSON, err := json.Marshal(policy)
		if err != nil {
			return fmt.Errorf("encode policy: %w", err)
		}
		enabled := 0
		if policy.Enabled {
			enabled = 1
		}
		databaseResult, err := txApp.DB().NewQuery(`
			UPDATE state_policies
			SET name = {:name},
				adapter = {:adapter},
				mode = {:mode},
				enabled = {:enabled},
				revision = {:new_revision},
				updated_at = {:updated_at},
				data_json = {:data_json}
			WHERE id = {:id} AND revision = {:expected_revision}
		`).Bind(dbx.Params{
			"id":                policy.ID,
			"name":              policy.Name,
			"adapter":           policy.Adapter,
			"mode":              string(policy.Mode),
			"enabled":           enabled,
			"new_revision":      policy.Revision,
			"updated_at":        formatTime(policy.UpdatedAt),
			"data_json":         string(policyJSON),
			"expected_revision": expectedRevision,
		}).Execute()
		if err != nil {
			return fmt.Errorf("update policy: %w", err)
		}
		if err := requireOneRow(databaseResult); err != nil {
			return err
		}
		sealedEvent, err := repository.sealAuditEvent(txApp, event)
		if err != nil {
			return err
		}
		if err := insertAuditEvent(txApp, sealedEvent); err != nil {
			return err
		}
		if err := insertAuditSearch(txApp, sealedEvent, policySearchExtras(txApp, policy)...); err != nil {
			return err
		}
		if err := insertIdempotencyValue(txApp, clientRequestID, event.Actor.ID, "policy", policy.ID, policy); err != nil {
			return err
		}
		result = policy
		return nil
	})
	if err != nil {
		return state.ExecutionPolicy{}, err
	}
	return result, nil
}

func (repository *PocketBaseRepository) GetPolicy(_ context.Context, policyID string) (state.ExecutionPolicy, error) {
	return getPolicy(repository.app, policyID)
}

func (repository *PocketBaseRepository) ListPolicies(_ context.Context) ([]state.ExecutionPolicy, error) {
	rows := make([]struct {
		DataJSON string `db:"data_json"`
	}, 0)
	err := repository.app.DB().NewQuery(`
		SELECT data_json FROM state_policies
		ORDER BY name ASC, id ASC
	`).All(&rows)
	if err != nil {
		return nil, fmt.Errorf("list policies: %w", err)
	}
	policies := make([]state.ExecutionPolicy, 0, len(rows))
	for _, row := range rows {
		var policy state.ExecutionPolicy
		if err := json.Unmarshal([]byte(row.DataJSON), &policy); err != nil {
			return nil, fmt.Errorf("decode policy: %w", err)
		}
		policies = append(policies, policy)
	}
	return policies, nil
}

func (repository *PocketBaseRepository) UpsertRunner(
	_ context.Context,
	runner state.Runner,
	event state.AuditEvent,
	clientRequestID string,
) (state.Runner, error) {
	var result state.Runner
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		existing, found, err := lookupIdempotentValue(txApp, clientRequestID, event.Actor.ID, "runner", &state.Runner{})
		if err != nil {
			return err
		}
		if found {
			result = *(existing.(*state.Runner))
			return nil
		}
		runnerJSON, err := json.Marshal(runner)
		if err != nil {
			return fmt.Errorf("encode runner: %w", err)
		}
		_, err = txApp.DB().NewQuery(`
			INSERT INTO state_runners (
				id, display_name, last_seen_at, revision, registered_at, data_json
			) VALUES (
				{:id}, {:display_name}, {:last_seen_at}, {:revision}, {:registered_at}, {:data_json}
			)
		`).Bind(dbx.Params{
			"id":            runner.ID,
			"display_name":  runner.DisplayName,
			"last_seen_at":  formatTime(runner.LastSeenAt),
			"revision":      runner.Revision,
			"registered_at": formatTime(runner.RegisteredAt),
			"data_json":     string(runnerJSON),
		}).Execute()
		if err != nil && !isUniqueViolation(err) {
			return fmt.Errorf("insert runner: %w", err)
		}
		if err != nil {
			databaseResult, updateErr := txApp.DB().NewQuery(`
				UPDATE state_runners
				SET display_name = {:display_name},
					last_seen_at = {:last_seen_at},
					revision = {:new_revision},
					data_json = {:data_json}
				WHERE id = {:id} AND revision = {:expected_revision}
			`).Bind(dbx.Params{
				"id":                runner.ID,
				"display_name":      runner.DisplayName,
				"last_seen_at":      formatTime(runner.LastSeenAt),
				"new_revision":      runner.Revision,
				"data_json":         string(runnerJSON),
				"expected_revision": runner.Revision - 1,
			}).Execute()
			if updateErr != nil {
				return fmt.Errorf("update runner: %w", updateErr)
			}
			if err := requireOneRow(databaseResult); err != nil {
				return err
			}
		}
		sealedEvent, err := repository.sealAuditEvent(txApp, event)
		if err != nil {
			return err
		}
		if err := insertAuditEvent(txApp, sealedEvent); err != nil {
			return err
		}
		if err := insertAuditSearch(txApp, sealedEvent, runner.DisplayName); err != nil {
			return err
		}
		if err := insertIdempotencyValue(txApp, clientRequestID, event.Actor.ID, "runner", runner.ID, runner); err != nil {
			return err
		}
		result = runner
		return nil
	})
	if err != nil {
		return state.Runner{}, err
	}
	return result, nil
}

func (repository *PocketBaseRepository) UpdateRunner(
	_ context.Context,
	runner state.Runner,
	expectedRevision int64,
	event state.AuditEvent,
	clientRequestID string,
) (state.Runner, error) {
	var result state.Runner
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		existing, found, err := lookupIdempotentValue(txApp, clientRequestID, event.Actor.ID, "runner", &state.Runner{})
		if err != nil {
			return err
		}
		if found {
			result = *(existing.(*state.Runner))
			return nil
		}
		current, err := getRunner(txApp, runner.ID)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return state.ErrRevisionConflict
		}
		runnerJSON, err := json.Marshal(runner)
		if err != nil {
			return fmt.Errorf("encode runner: %w", err)
		}
		databaseResult, err := txApp.DB().NewQuery(`
			UPDATE state_runners
			SET display_name = {:display_name},
				revision = {:new_revision},
				data_json = {:data_json}
			WHERE id = {:id} AND revision = {:expected_revision}
		`).Bind(dbx.Params{
			"id":                runner.ID,
			"display_name":      runner.DisplayName,
			"new_revision":      runner.Revision,
			"data_json":         string(runnerJSON),
			"expected_revision": expectedRevision,
		}).Execute()
		if err != nil {
			return fmt.Errorf("update runner: %w", err)
		}
		if err := requireOneRow(databaseResult); err != nil {
			return err
		}
		sealedEvent, err := repository.sealAuditEvent(txApp, event)
		if err != nil {
			return err
		}
		if err := insertAuditEvent(txApp, sealedEvent); err != nil {
			return err
		}
		if err := insertAuditSearch(txApp, sealedEvent, runner.DisplayName); err != nil {
			return err
		}
		if err := insertIdempotencyValue(txApp, clientRequestID, event.Actor.ID, "runner", runner.ID, runner); err != nil {
			return err
		}
		result = runner
		return nil
	})
	if err != nil {
		return state.Runner{}, err
	}
	return result, nil
}

func (repository *PocketBaseRepository) GetRunner(_ context.Context, runnerID string) (state.Runner, error) {
	return getRunner(repository.app, runnerID)
}

func (repository *PocketBaseRepository) ListRunners(_ context.Context) ([]state.Runner, error) {
	rows := make([]struct {
		DataJSON string `db:"data_json"`
	}, 0)
	err := repository.app.DB().NewQuery(`
		SELECT data_json FROM state_runners
		ORDER BY registered_at ASC, id ASC
	`).All(&rows)
	if err != nil {
		return nil, fmt.Errorf("list runners: %w", err)
	}
	runners := make([]state.Runner, 0, len(rows))
	for _, row := range rows {
		var runner state.Runner
		if err := json.Unmarshal([]byte(row.DataJSON), &runner); err != nil {
			return nil, fmt.Errorf("decode runner: %w", err)
		}
		runners = append(runners, runner)
	}
	return runners, nil
}

func (repository *PocketBaseRepository) TouchRunnerSeen(_ context.Context, runnerID string, seenAt time.Time) error {
	return repository.app.RunInTransaction(func(txApp core.App) error {
		runner, err := getRunner(txApp, runnerID)
		if err != nil {
			return err
		}
		runner.LastSeenAt = seenAt.UTC()
		runnerJSON, err := json.Marshal(runner)
		if err != nil {
			return fmt.Errorf("encode runner: %w", err)
		}
		databaseResult, err := txApp.DB().NewQuery(`
			UPDATE state_runners
			SET last_seen_at = {:last_seen_at},
				data_json = {:data_json}
			WHERE id = {:id}
		`).Bind(dbx.Params{
			"id":           runnerID,
			"last_seen_at": formatTime(runner.LastSeenAt),
			"data_json":    string(runnerJSON),
		}).Execute()
		if err != nil {
			return fmt.Errorf("touch runner: %w", err)
		}
		if err := requireOneRow(databaseResult); err != nil {
			return err
		}
		return nil
	})
}

// CreateAgentRun inserts a planned or eligible run. The
// UNIQUE(occurrence_id, policy_revision) key answers "already materialized"
// via INSERT OR IGNORE; manual runs (occurrence NULL) dedupe through the
// idempotency table instead. The audit event is only written when the row was
// actually created; the second return reports that.
func (repository *PocketBaseRepository) CreateAgentRun(
	_ context.Context,
	run state.AgentRun,
	event state.AuditEvent,
	clientRequestID string,
) (state.AgentRun, bool, error) {
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
		if _, err := getReminder(txApp, run.ReminderID); err != nil {
			return err
		}
		inserted, err := insertRunRow(txApp, run)
		if err != nil {
			return err
		}
		if !inserted {
			existing, err := getRunByOccurrence(txApp, run.OccurrenceID, run.PolicyRevision)
			if err != nil {
				return err
			}
			result = existing
			return nil
		}
		sealedEvent, err := repository.sealAuditEvent(txApp, event)
		if err != nil {
			return err
		}
		if err := insertAuditEvent(txApp, sealedEvent); err != nil {
			return err
		}
		if err := insertAuditSearch(txApp, sealedEvent, runSearchExtras(run)...); err != nil {
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

// ClaimAgentRun performs the atomic claim CAS: the run must still be eligible
// at the expected revision. A lost race reports ErrNotClaimable.
func (repository *PocketBaseRepository) ClaimAgentRun(
	_ context.Context,
	run state.AgentRun,
	expectedRevision int64,
	event state.AuditEvent,
) (state.AgentRun, error) {
	var result state.AgentRun
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		if _, err := getAgentRun(txApp, run.ID); err != nil {
			return err
		}
		if err := updateRunRow(txApp, run, expectedRevision, " AND status = 'eligible'"); err != nil {
			if errors.Is(err, state.ErrRunStateConflict) {
				return state.ErrNotClaimable
			}
			return err
		}
		sealedEvent, err := repository.sealAuditEvent(txApp, event)
		if err != nil {
			return err
		}
		if err := insertAuditEvent(txApp, sealedEvent); err != nil {
			return err
		}
		if err := insertAuditSearch(txApp, sealedEvent, runSearchExtras(run)...); err != nil {
			return err
		}
		result = run
		return nil
	})
	if err != nil {
		return state.AgentRun{}, err
	}
	return result, nil
}

func (repository *PocketBaseRepository) GetAgentRun(_ context.Context, runID string) (state.AgentRun, error) {
	return getAgentRun(repository.app, runID)
}

func (repository *PocketBaseRepository) ListAgentRuns(_ context.Context, filter state.AgentRunListFilter) ([]state.AgentRun, error) {
	where := "WHERE 1 = 1"
	params := dbx.Params{"limit": normalizeLimit(filter.Limit)}
	if filter.ReminderID != "" {
		where += " AND reminder_id = {:reminder_id}"
		params["reminder_id"] = filter.ReminderID
	}
	if filter.Status != nil {
		where += " AND status = {:status}"
		params["status"] = string(*filter.Status)
	}
	if filter.RunnerID != "" {
		where += " AND runner_id = {:runner_id}"
		params["runner_id"] = filter.RunnerID
	}
	rows := make([]struct {
		DataJSON string `db:"data_json"`
	}, 0)
	err := repository.app.DB().NewQuery(`
		SELECT data_json FROM state_runs
		` + where + `
		ORDER BY requested_at DESC, id DESC
		LIMIT {:limit}
	`).Bind(params).All(&rows)
	if err != nil {
		return nil, fmt.Errorf("list agent runs: %w", err)
	}
	return decodeRuns(rows)
}

// ListClaimableRuns returns the eligible runs the runner may serve (adapter
// and project both registered), oldest first.
func (repository *PocketBaseRepository) ListClaimableRuns(_ context.Context, runner state.Runner, now time.Time) ([]state.AgentRun, error) {
	if len(runner.Adapters) == 0 || len(runner.Projects) == 0 {
		return []state.AgentRun{}, nil
	}
	params := dbx.Params{
		"now":   formatTime(now),
		"limit": 100,
	}
	where := "WHERE status = 'eligible' AND (requested_at IS NULL OR requested_at <= {:now})"
	where += " AND adapter IN (" + stringListPlaceholders(params, "adapter", runner.Adapters) + ")"
	where += " AND project_id IN (" + stringListPlaceholders(params, "project", runner.Projects) + ")"
	rows := make([]struct {
		DataJSON string `db:"data_json"`
	}, 0)
	err := repository.app.DB().NewQuery(`
		SELECT data_json FROM state_runs
		` + where + `
		ORDER BY requested_at ASC, id ASC
		LIMIT {:limit}
	`).Bind(params).All(&rows)
	if err != nil {
		return nil, fmt.Errorf("list claimable runs: %w", err)
	}
	return decodeRuns(rows)
}

// UpdateAgentRunTransition is the generic run CAS transition used by report,
// complete, approve and cancel. A nil event extends the lease without growing
// the audit chain (heartbeat). When occurrence is set, the originating
// occurrence is completed in the same transaction; a vanished occurrence
// makes that step a no-op.
func (repository *PocketBaseRepository) UpdateAgentRunTransition(
	_ context.Context,
	run state.AgentRun,
	expectedRevision int64,
	event *state.AuditEvent,
	occurrence *state.Occurrence,
	occurrenceEvent *state.AuditEvent,
	clientRequestID string,
) (state.AgentRun, bool, error) {
	var result state.AgentRun
	applied := false
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		actorID := ""
		if event != nil {
			actorID = event.Actor.ID
		}
		if clientRequestID != "" {
			existing, found, err := lookupIdempotentValue(txApp, clientRequestID, actorID, "agent_run", &state.AgentRun{})
			if err != nil {
				return err
			}
			if found {
				result = *(existing.(*state.AgentRun))
				return nil
			}
		}
		current, err := getAgentRun(txApp, run.ID)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return state.ErrRunStateConflict
		}
		if err := updateRunRow(txApp, run, expectedRevision, ""); err != nil {
			return err
		}
		if event != nil {
			sealedEvent, err := repository.sealAuditEvent(txApp, *event)
			if err != nil {
				return err
			}
			if err := insertAuditEvent(txApp, sealedEvent); err != nil {
				return err
			}
			if err := insertAuditSearch(txApp, sealedEvent, runSearchExtras(run)...); err != nil {
				return err
			}
		}
		if occurrence != nil {
			currentOccurrence, err := getOccurrence(txApp, occurrence.ID)
			if err != nil && !errors.Is(err, state.ErrNotFound) {
				return err
			}
			if err == nil {
				if currentOccurrence.Revision != occurrence.Revision-1 {
					return state.ErrRevisionConflict
				}
				if err := updateOccurrenceRow(txApp, *occurrence, currentOccurrence.Revision); err != nil {
					return err
				}
				if occurrenceEvent != nil {
					sealedOccurrenceEvent, err := repository.sealAuditEvent(txApp, *occurrenceEvent)
					if err != nil {
						return err
					}
					if err := insertAuditEvent(txApp, sealedOccurrenceEvent); err != nil {
						return err
					}
					if err := insertAuditSearch(txApp, sealedOccurrenceEvent); err != nil {
						return err
					}
				}
			}
		}
		if clientRequestID != "" {
			if err := insertIdempotencyValue(txApp, clientRequestID, actorID, "agent_run", run.ID, run); err != nil {
				return err
			}
		}
		result = run
		applied = true
		return nil
	})
	if err != nil {
		return state.AgentRun{}, false, err
	}
	return result, applied, nil
}

// RequeueExpiredLeases returns claimed runs whose lease expired to eligible,
// clearing the runner assignment and writing a run.requeued audit event per
// run.
func (repository *PocketBaseRepository) RequeueExpiredLeases(_ context.Context, now time.Time) ([]state.AgentRun, error) {
	requeued := make([]state.AgentRun, 0)
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		rows := make([]struct {
			DataJSON string `db:"data_json"`
		}, 0)
		err := txApp.DB().NewQuery(`
			SELECT data_json FROM state_runs
			WHERE status = 'claimed' AND lease_expires_at IS NOT NULL AND lease_expires_at < {:now}
			ORDER BY requested_at ASC, id ASC
		`).Bind(dbx.Params{"now": formatTime(now)}).All(&rows)
		if err != nil {
			return fmt.Errorf("list expired leases: %w", err)
		}
		runs, err := decodeRuns(rows)
		if err != nil {
			return err
		}
		for _, run := range runs {
			updated := run
			updated.Status = state.AgentRunStatusEligible
			updated.RunnerID = nil
			updated.ClaimedAt = nil
			updated.LeaseExpiresAt = nil
			updated.Revision++
			updated.UpdatedAt = now.UTC()
			if err := updateRunRow(txApp, updated, run.Revision, " AND status = 'claimed'"); err != nil {
				return err
			}
			event, err := newSweepAuditEvent(run, updated, state.AuditActionRunRequeued, []string{"claimed_at", "lease_expires_at", "runner_id", "status"}, fmt.Sprintf("requeue:%s:%d", run.ID, updated.Revision), now.UTC())
			if err != nil {
				return err
			}
			sealedEvent, err := repository.sealAuditEvent(txApp, event)
			if err != nil {
				return err
			}
			if err := insertAuditEvent(txApp, sealedEvent); err != nil {
				return err
			}
			if err := insertAuditSearch(txApp, sealedEvent, runSearchExtras(run)...); err != nil {
				return err
			}
			requeued = append(requeued, updated)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return requeued, nil
}

// ExpireStaleRuns retires planned or eligible runs whose request outlived its
// window, whose occurrence vanished (e.g. through a reschedule), or whose
// policy is gone or disabled.
func (repository *PocketBaseRepository) ExpireStaleRuns(_ context.Context, now time.Time) ([]state.AgentRun, error) {
	expired := make([]state.AgentRun, 0)
	err := repository.app.RunInTransaction(func(txApp core.App) error {
		rows := make([]struct {
			DataJSON string `db:"data_json"`
		}, 0)
		err := txApp.DB().NewQuery(`
			SELECT data_json FROM state_runs r
			WHERE r.status IN ('planned', 'eligible') AND (
				(r.requested_at IS NOT NULL AND r.requested_at < {:cutoff})
				OR (r.occurrence_id IS NOT NULL AND NOT EXISTS (
					SELECT 1 FROM state_occurrences o WHERE o.id = r.occurrence_id
				))
				OR NOT EXISTS (
					SELECT 1 FROM state_policies p WHERE p.id = r.policy_id AND p.enabled = 1
				)
			)
			ORDER BY r.requested_at ASC, r.id ASC
		`).Bind(dbx.Params{"cutoff": formatTime(now.Add(-state.StaleRunMaxAge))}).All(&rows)
		if err != nil {
			return fmt.Errorf("list stale runs: %w", err)
		}
		runs, err := decodeRuns(rows)
		if err != nil {
			return err
		}
		for _, run := range runs {
			updated := run
			updated.Status = state.AgentRunStatusExpired
			updated.FinishedAt = &now
			updated.Revision++
			updated.UpdatedAt = now.UTC()
			if err := updateRunRow(txApp, updated, run.Revision, ""); err != nil {
				return err
			}
			event, err := newSweepAuditEvent(run, updated, state.AuditActionRunExpired, []string{"finished_at", "status"}, fmt.Sprintf("expire:%s:%d", run.ID, updated.Revision), now.UTC())
			if err != nil {
				return err
			}
			sealedEvent, err := repository.sealAuditEvent(txApp, event)
			if err != nil {
				return err
			}
			if err := insertAuditEvent(txApp, sealedEvent); err != nil {
				return err
			}
			if err := insertAuditSearch(txApp, sealedEvent, runSearchExtras(run)...); err != nil {
				return err
			}
			expired = append(expired, updated)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return expired, nil
}

// ListDueOccurrences mirrors the push due-window semantics: pending or
// snoozed occurrences of unarchived reminders whose fire time (snoozed_until,
// else scheduled minus prewarning) is at or before now.
func (repository *PocketBaseRepository) ListDueOccurrences(_ context.Context, now time.Time) ([]state.DueOccurrence, error) {
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
	result := make([]state.DueOccurrence, 0)
	for _, row := range rows {
		var reminder state.Reminder
		var occurrence state.Occurrence
		if err := json.Unmarshal([]byte(row.ReminderJSON), &reminder); err != nil {
			return nil, fmt.Errorf("decode due reminder: %w", err)
		}
		if err := json.Unmarshal([]byte(row.OccurrenceJSON), &occurrence); err != nil {
			return nil, fmt.Errorf("decode due occurrence: %w", err)
		}
		var dueAt time.Time
		if occurrence.Status == state.OccurrenceStatusSnoozed && occurrence.SnoozedUntil != nil {
			dueAt = occurrence.SnoozedUntil.UTC()
		} else if occurrence.ScheduledAt != nil {
			dueAt = occurrence.ScheduledAt.Add(-time.Duration(occurrence.PrewarningMinutes) * time.Minute).UTC()
		} else {
			continue
		}
		if dueAt.After(now) {
			continue
		}
		result = append(result, state.DueOccurrence{Reminder: reminder, Occurrence: occurrence})
	}
	return result, nil
}

func (repository *PocketBaseRepository) LatestChangeCursor(_ context.Context) (int64, error) {
	row := struct {
		Cursor int64 `db:"cursor"`
	}{}
	err := repository.app.DB().NewQuery(`
		SELECT COALESCE(MAX(sequence), 0) AS cursor FROM state_audit_events
	`).One(&row)
	if err != nil {
		return 0, fmt.Errorf("read latest change cursor: %w", err)
	}
	return row.Cursor, nil
}

func getProject(app core.App, projectID string) (state.Project, error) {
	row := struct {
		DataJSON string `db:"data_json"`
	}{}
	err := app.DB().NewQuery(`
		SELECT data_json FROM state_projects WHERE id = {:id}
	`).Bind(dbx.Params{"id": projectID}).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.Project{}, state.ErrNotFound
	}
	if err != nil {
		return state.Project{}, fmt.Errorf("get project: %w", err)
	}
	var project state.Project
	if err := json.Unmarshal([]byte(row.DataJSON), &project); err != nil {
		return state.Project{}, fmt.Errorf("decode project: %w", err)
	}
	return project, nil
}

func getPolicy(app core.App, policyID string) (state.ExecutionPolicy, error) {
	row := struct {
		DataJSON string `db:"data_json"`
	}{}
	err := app.DB().NewQuery(`
		SELECT data_json FROM state_policies WHERE id = {:id}
	`).Bind(dbx.Params{"id": policyID}).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.ExecutionPolicy{}, state.ErrNotFound
	}
	if err != nil {
		return state.ExecutionPolicy{}, fmt.Errorf("get policy: %w", err)
	}
	var policy state.ExecutionPolicy
	if err := json.Unmarshal([]byte(row.DataJSON), &policy); err != nil {
		return state.ExecutionPolicy{}, fmt.Errorf("decode policy: %w", err)
	}
	return policy, nil
}

func getRunner(app core.App, runnerID string) (state.Runner, error) {
	row := struct {
		DataJSON string `db:"data_json"`
	}{}
	err := app.DB().NewQuery(`
		SELECT data_json FROM state_runners WHERE id = {:id}
	`).Bind(dbx.Params{"id": runnerID}).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.Runner{}, state.ErrNotFound
	}
	if err != nil {
		return state.Runner{}, fmt.Errorf("get runner: %w", err)
	}
	var runner state.Runner
	if err := json.Unmarshal([]byte(row.DataJSON), &runner); err != nil {
		return state.Runner{}, fmt.Errorf("decode runner: %w", err)
	}
	return runner, nil
}

func getAgentRun(app core.App, runID string) (state.AgentRun, error) {
	row := struct {
		DataJSON string `db:"data_json"`
	}{}
	err := app.DB().NewQuery(`
		SELECT data_json FROM state_runs WHERE id = {:id}
	`).Bind(dbx.Params{"id": runID}).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.AgentRun{}, state.ErrNotFound
	}
	if err != nil {
		return state.AgentRun{}, fmt.Errorf("get agent run: %w", err)
	}
	var run state.AgentRun
	if err := json.Unmarshal([]byte(row.DataJSON), &run); err != nil {
		return state.AgentRun{}, fmt.Errorf("decode agent run: %w", err)
	}
	return run, nil
}

func getRunByOccurrence(app core.App, occurrenceID *string, policyRevision int64) (state.AgentRun, error) {
	if occurrenceID == nil {
		return state.AgentRun{}, fmt.Errorf("run insert ignored without occurrence key")
	}
	row := struct {
		DataJSON string `db:"data_json"`
	}{}
	err := app.DB().NewQuery(`
		SELECT data_json FROM state_runs
		WHERE occurrence_id = {:occurrence_id} AND policy_revision = {:policy_revision}
	`).Bind(dbx.Params{
		"occurrence_id":   *occurrenceID,
		"policy_revision": policyRevision,
	}).One(&row)
	if errors.Is(err, sql.ErrNoRows) {
		return state.AgentRun{}, state.ErrNotFound
	}
	if err != nil {
		return state.AgentRun{}, fmt.Errorf("get agent run by occurrence: %w", err)
	}
	var run state.AgentRun
	if err := json.Unmarshal([]byte(row.DataJSON), &run); err != nil {
		return state.AgentRun{}, fmt.Errorf("decode agent run: %w", err)
	}
	return run, nil
}

func insertPolicyRow(app core.App, policy state.ExecutionPolicy) error {
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return fmt.Errorf("encode policy: %w", err)
	}
	enabled := 0
	if policy.Enabled {
		enabled = 1
	}
	_, err = app.DB().NewQuery(`
		INSERT INTO state_policies (
			id, name, project_id, adapter, mode, enabled, revision, created_at, updated_at, data_json
		) VALUES (
			{:id}, {:name}, {:project_id}, {:adapter}, {:mode}, {:enabled}, {:revision}, {:created_at}, {:updated_at}, {:data_json}
		)
	`).Bind(dbx.Params{
		"id":         policy.ID,
		"name":       policy.Name,
		"project_id": policy.ProjectID,
		"adapter":    policy.Adapter,
		"mode":       string(policy.Mode),
		"enabled":    enabled,
		"revision":   policy.Revision,
		"created_at": formatTime(policy.CreatedAt),
		"updated_at": formatTime(policy.UpdatedAt),
		"data_json":  string(policyJSON),
	}).Execute()
	if err != nil {
		return fmt.Errorf("insert policy: %w", err)
	}
	return nil
}

// insertRunRow performs the INSERT OR IGNORE and reports whether the row was
// written.
func insertRunRow(app core.App, run state.AgentRun) (bool, error) {
	runJSON, err := json.Marshal(run)
	if err != nil {
		return false, fmt.Errorf("encode run: %w", err)
	}
	databaseResult, err := app.DB().NewQuery(`
		INSERT OR IGNORE INTO state_runs (
			id, reminder_id, occurrence_id, policy_id, policy_revision, project_id, adapter, runner_id,
			status, idempotency_key, lease_expires_at, requested_at, claimed_at, started_at, finished_at,
			revision, created_at, updated_at, data_json
		) VALUES (
			{:id}, {:reminder_id}, {:occurrence_id}, {:policy_id}, {:policy_revision}, {:project_id}, {:adapter}, {:runner_id},
			{:status}, {:idempotency_key}, {:lease_expires_at}, {:requested_at}, {:claimed_at}, {:started_at}, {:finished_at},
			{:revision}, {:created_at}, {:updated_at}, {:data_json}
		)
	`).Bind(runParams(run, runJSON)).Execute()
	if err != nil {
		return false, fmt.Errorf("insert run: %w", err)
	}
	rowsAffected, err := databaseResult.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read run insert result: %w", err)
	}
	return rowsAffected == 1, nil
}

// updateRunRow is the shared run CAS update. extraCondition appends a raw
// status precondition such as " AND status = 'eligible'" for claims.
func updateRunRow(app core.App, run state.AgentRun, expectedRevision int64, extraCondition string) error {
	runJSON, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("encode run: %w", err)
	}
	params := runParams(run, runJSON)
	params["expected_revision"] = expectedRevision
	databaseResult, err := app.DB().NewQuery(`
		UPDATE state_runs
		SET status = {:status},
			runner_id = {:runner_id},
			lease_expires_at = {:lease_expires_at},
			claimed_at = {:claimed_at},
			started_at = {:started_at},
			finished_at = {:finished_at},
			revision = {:revision},
			updated_at = {:updated_at},
			data_json = {:data_json}
		WHERE id = {:id} AND revision = {:expected_revision}` + extraCondition + `
	`).Bind(params).Execute()
	if err != nil {
		return fmt.Errorf("update run: %w", err)
	}
	rowsAffected, err := databaseResult.RowsAffected()
	if err != nil {
		return fmt.Errorf("read run update result: %w", err)
	}
	if rowsAffected != 1 {
		return state.ErrRunStateConflict
	}
	return nil
}

// runParams maps a run onto the shared bind parameters of insertRunRow and
// updateRunRow.
func runParams(run state.AgentRun, runJSON []byte) dbx.Params {
	return dbx.Params{
		"id":               run.ID,
		"reminder_id":      run.ReminderID,
		"occurrence_id":    optionalString(run.OccurrenceID),
		"policy_id":        run.PolicyID,
		"policy_revision":  run.PolicyRevision,
		"project_id":       run.ProjectID,
		"adapter":          run.Adapter,
		"runner_id":        optionalString(run.RunnerID),
		"status":           string(run.Status),
		"idempotency_key":  run.IdempotencyKey,
		"lease_expires_at": optionalTime(run.LeaseExpiresAt),
		"requested_at":     optionalTime(run.RequestedAt),
		"claimed_at":       optionalTime(run.ClaimedAt),
		"started_at":       optionalTime(run.StartedAt),
		"finished_at":      optionalTime(run.FinishedAt),
		"revision":         run.Revision,
		"created_at":       formatTime(run.CreatedAt),
		"updated_at":       formatTime(run.UpdatedAt),
		"data_json":        string(runJSON),
	}
}

// updateOccurrenceRow applies the completed-occurrence CAS inside a run
// completion transaction.
func updateOccurrenceRow(app core.App, occurrence state.Occurrence, expectedRevision int64) error {
	occurrenceJSON, err := json.Marshal(occurrence)
	if err != nil {
		return fmt.Errorf("encode occurrence: %w", err)
	}
	databaseResult, err := app.DB().NewQuery(`
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
	return nil
}

// lookupIdempotentValue is the generic form of the per-type idempotency
// lookups: actor mismatch is forbidden, a result of another type means the
// client request ID was reused across mutation kinds.
func lookupIdempotentValue(app core.App, clientRequestID string, actorID string, resultType string, target any) (any, bool, error) {
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
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read idempotency result: %w", err)
	}
	if row.ActorID != actorID {
		return nil, false, state.ErrForbidden
	}
	if row.ResultType != resultType {
		return nil, false, state.ErrInvalidInput
	}
	if err := json.Unmarshal([]byte(row.ResultJSON), target); err != nil {
		return nil, false, fmt.Errorf("decode idempotency result: %w", err)
	}
	return target, true, nil
}

// newSweepAuditEvent builds the system-actor audit event for a
// scheduler-driven run transition (requeue, expire).
func newSweepAuditEvent(before state.AgentRun, after state.AgentRun, action state.AuditAction, changedFields []string, clientRequestID string, now time.Time) (state.AuditEvent, error) {
	eventID, err := uuid.NewV7()
	if err != nil {
		return state.AuditEvent{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	beforeSnapshot, err := json.Marshal(before)
	if err != nil {
		return state.AuditEvent{}, fmt.Errorf("encode previous run snapshot: %w", err)
	}
	afterSnapshot, err := json.Marshal(after)
	if err != nil {
		return state.AuditEvent{}, fmt.Errorf("encode updated run snapshot: %w", err)
	}
	return state.AuditEvent{
		ID:              eventID.String(),
		ReminderID:      before.ReminderID,
		Action:          action,
		Actor:           state.SystemActor(),
		ServerTime:      now,
		BeforeSnapshot:  beforeSnapshot,
		AfterSnapshot:   afterSnapshot,
		ChangedFields:   changedFields,
		Revision:        after.Revision,
		CorrelationID:   before.TaskContract.CorrelationID,
		ClientRequestID: clientRequestID,
	}, nil
}

// runSearchExtras contributes the run/policy audit text to the search index:
// adapter, project name and the redacted summary. Never log content.
func runSearchExtras(run state.AgentRun) []string {
	return []string{run.Adapter, run.TaskContract.ProjectName, run.ResultSummary}
}

func policySearchExtras(app core.App, policy state.ExecutionPolicy) []string {
	extras := []string{policy.Name, policy.Adapter}
	project, err := getProject(app, policy.ProjectID)
	if err == nil {
		extras = append(extras, project.Name)
	}
	return extras
}

func decodeRuns(rows []struct {
	DataJSON string `db:"data_json"`
}) ([]state.AgentRun, error) {
	runs := make([]state.AgentRun, 0, len(rows))
	for _, row := range rows {
		var run state.AgentRun
		if err := json.Unmarshal([]byte(row.DataJSON), &run); err != nil {
			return nil, fmt.Errorf("decode run list item: %w", err)
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func requireOneRow(result sql.Result) error {
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read update result: %w", err)
	}
	if rowsAffected != 1 {
		return state.ErrRevisionConflict
	}
	return nil
}

func optionalString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func optionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

// stringListPlaceholders expands a string list into "{:prefix0}, {:prefix1}"
// bind placeholders, registering the values in params.
func stringListPlaceholders(params dbx.Params, prefix string, values []string) string {
	placeholders := make([]string, 0, len(values))
	for index, value := range values {
		key := fmt.Sprintf("%s%d", prefix, index)
		params[key] = value
		placeholders = append(placeholders, "{:"+key+"}")
	}
	return strings.Join(placeholders, ", ")
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
