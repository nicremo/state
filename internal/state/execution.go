package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// maxClaimWaitSeconds caps the server-side long-poll budget of a claim.
	maxClaimWaitSeconds = 25
	// claimPollInterval is the pause between claimable-run polls while
	// long-polling.
	claimPollInterval = 250 * time.Millisecond
	// maxResultSummaryLength bounds the redacted result summary stored on a
	// run and indexed for search.
	maxResultSummaryLength = 2000
)

// secretLinePattern matches lines that look like they carry a credential
// assignment; redactSummary strips them before anything is persisted or
// indexed.
var secretLinePattern = regexp.MustCompile(`(?i)(key|token|secret|password)\s*[:=]\s*\S+`)

func (service *Service) CreateProject(ctx context.Context, actor Actor, input CreateProjectInput) (Project, error) {
	if actor.ID == "" || input.ClientRequestID == "" {
		return Project{}, ErrInvalidInput
	}
	if actor.Kind != ActorKindOwner {
		return Project{}, ErrForbidden
	}
	name := strings.TrimSpace(input.Name)
	if !ValidProjectSlug(name) {
		return Project{}, ErrInvalidInput
	}

	projectID, err := service.newID()
	if err != nil {
		return Project{}, fmt.Errorf("generate project ID: %w", err)
	}
	eventID, err := service.newID()
	if err != nil {
		return Project{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	now := service.clock().UTC()
	project := Project{
		ID:           projectID,
		Name:         name,
		Description:  input.Description,
		RootPathHint: input.RootPathHint,
		Revision:     1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	event, err := service.buildAuditEvent(eventID, "", AuditActionProjectCreated, actor, now, input.ClientTime, input.Source, input.SourceExcerpt, nil, project, []string{"description", "name", "root_path_hint"}, project.Revision, input.CorrelationID, input.ClientRequestID)
	if err != nil {
		return Project{}, err
	}
	return service.repository.CreateProject(ctx, project, event, input.ClientRequestID)
}

func (service *Service) UpdateProject(ctx context.Context, actor Actor, projectID string, input UpdateProjectInput) (Project, error) {
	if actor.ID == "" || projectID == "" || input.ClientRequestID == "" {
		return Project{}, ErrInvalidInput
	}
	if actor.Kind != ActorKindOwner {
		return Project{}, ErrForbidden
	}

	current, err := service.repository.GetProject(ctx, projectID)
	if err != nil {
		return Project{}, err
	}
	if current.Revision != input.ExpectedRevision {
		return Project{}, ErrRevisionConflict
	}

	updated := current
	changed := make([]string, 0, 3)
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if !ValidProjectSlug(name) {
			return Project{}, ErrInvalidInput
		}
		if updated.Name != name {
			updated.Name = name
			changed = append(changed, "name")
		}
	}
	if input.Description != nil && updated.Description != *input.Description {
		updated.Description = *input.Description
		changed = append(changed, "description")
	}
	if input.RootPathHint != nil && updated.RootPathHint != *input.RootPathHint {
		updated.RootPathHint = *input.RootPathHint
		changed = append(changed, "root_path_hint")
	}
	if len(changed) == 0 {
		return current, nil
	}
	sort.Strings(changed)
	updated.Revision++
	updated.UpdatedAt = service.clock().UTC()

	eventID, err := service.newID()
	if err != nil {
		return Project{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	event, err := service.buildAuditEvent(eventID, "", AuditActionProjectUpdated, actor, updated.UpdatedAt, input.ClientTime, input.Source, input.SourceExcerpt, &current, updated, changed, updated.Revision, input.CorrelationID, input.ClientRequestID)
	if err != nil {
		return Project{}, err
	}
	return service.repository.UpdateProject(ctx, updated, input.ExpectedRevision, event, input.ClientRequestID)
}

func (service *Service) GetProject(ctx context.Context, projectID string) (Project, error) {
	if projectID == "" {
		return Project{}, ErrInvalidInput
	}
	return service.repository.GetProject(ctx, projectID)
}

func (service *Service) ListProjects(ctx context.Context) ([]Project, error) {
	return service.repository.ListProjects(ctx)
}

func (service *Service) CreatePolicy(ctx context.Context, actor Actor, input CreatePolicyInput) (ExecutionPolicy, error) {
	if actor.ID == "" || input.ClientRequestID == "" {
		return ExecutionPolicy{}, ErrInvalidInput
	}
	if actor.Kind != ActorKindOwner {
		return ExecutionPolicy{}, ErrForbidden
	}
	if _, err := service.repository.GetProject(ctx, input.ProjectID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return ExecutionPolicy{}, ErrInvalidInput
		}
		return ExecutionPolicy{}, err
	}

	policyID, err := service.newID()
	if err != nil {
		return ExecutionPolicy{}, fmt.Errorf("generate policy ID: %w", err)
	}
	eventID, err := service.newID()
	if err != nil {
		return ExecutionPolicy{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	now := service.clock().UTC()
	policy := ExecutionPolicy{
		ID:                          policyID,
		Name:                        strings.TrimSpace(input.Name),
		ProjectID:                   input.ProjectID,
		Adapter:                     input.Adapter,
		Mode:                        input.Mode,
		AllowedCapabilities:         append([]string(nil), input.AllowedCapabilities...),
		MarkOccurrenceDoneOnSuccess: input.MarkOccurrenceDoneOnSuccess,
		NotifyOnStart:               input.NotifyOnStart,
		NotifyOnCompletion:          input.NotifyOnCompletion,
		NotifyOnFailure:             input.NotifyOnFailure,
		TimeoutMinutes:              input.TimeoutMinutes,
		Enabled:                     true,
		Revision:                    1,
		CreatedAt:                   now,
		UpdatedAt:                   now,
	}
	if err := ValidPolicyConfiguration(policy); err != nil {
		return ExecutionPolicy{}, err
	}
	event, err := service.buildAuditEvent(eventID, "", AuditActionPolicyCreated, actor, now, input.ClientTime, input.Source, input.SourceExcerpt, nil, policy, []string{"adapter", "allowed_capabilities", "mark_occurrence_done_on_success", "mode", "name", "notify_on_completion", "notify_on_failure", "notify_on_start", "project_id", "timeout_minutes"}, policy.Revision, input.CorrelationID, input.ClientRequestID)
	if err != nil {
		return ExecutionPolicy{}, err
	}
	return service.repository.CreatePolicy(ctx, policy, event, input.ClientRequestID)
}

func (service *Service) UpdatePolicy(ctx context.Context, actor Actor, policyID string, input UpdatePolicyInput) (ExecutionPolicy, error) {
	if actor.ID == "" || policyID == "" || input.ClientRequestID == "" {
		return ExecutionPolicy{}, ErrInvalidInput
	}
	if actor.Kind != ActorKindOwner {
		return ExecutionPolicy{}, ErrForbidden
	}

	current, err := service.repository.GetPolicy(ctx, policyID)
	if err != nil {
		return ExecutionPolicy{}, err
	}
	if current.Revision != input.ExpectedRevision {
		return ExecutionPolicy{}, ErrRevisionConflict
	}

	updated := clonePolicy(current)
	changed := make([]string, 0, 10)
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if updated.Name != name {
			updated.Name = name
			changed = append(changed, "name")
		}
	}
	if input.Adapter != nil && updated.Adapter != *input.Adapter {
		updated.Adapter = *input.Adapter
		changed = append(changed, "adapter")
	}
	if input.Mode != nil && updated.Mode != *input.Mode {
		updated.Mode = *input.Mode
		changed = append(changed, "mode")
	}
	if input.AllowedCapabilities != nil {
		capabilities := append([]string(nil), (*input.AllowedCapabilities)...)
		if !equalStrings(updated.AllowedCapabilities, capabilities) {
			updated.AllowedCapabilities = capabilities
			changed = append(changed, "allowed_capabilities")
		}
	}
	if input.MarkOccurrenceDoneOnSuccess != nil && updated.MarkOccurrenceDoneOnSuccess != *input.MarkOccurrenceDoneOnSuccess {
		updated.MarkOccurrenceDoneOnSuccess = *input.MarkOccurrenceDoneOnSuccess
		changed = append(changed, "mark_occurrence_done_on_success")
	}
	if input.NotifyOnStart != nil && updated.NotifyOnStart != *input.NotifyOnStart {
		updated.NotifyOnStart = *input.NotifyOnStart
		changed = append(changed, "notify_on_start")
	}
	if input.NotifyOnCompletion != nil && updated.NotifyOnCompletion != *input.NotifyOnCompletion {
		updated.NotifyOnCompletion = *input.NotifyOnCompletion
		changed = append(changed, "notify_on_completion")
	}
	if input.NotifyOnFailure != nil && updated.NotifyOnFailure != *input.NotifyOnFailure {
		updated.NotifyOnFailure = *input.NotifyOnFailure
		changed = append(changed, "notify_on_failure")
	}
	if input.TimeoutMinutes != nil && updated.TimeoutMinutes != *input.TimeoutMinutes {
		updated.TimeoutMinutes = *input.TimeoutMinutes
		changed = append(changed, "timeout_minutes")
	}
	enabledChanged := false
	if input.Enabled != nil && updated.Enabled != *input.Enabled {
		updated.Enabled = *input.Enabled
		enabledChanged = true
	}
	if len(changed) == 0 && !enabledChanged {
		return current, nil
	}
	if err := ValidPolicyConfiguration(updated); err != nil {
		return ExecutionPolicy{}, err
	}
	sort.Strings(changed)
	updated.Revision++
	updated.UpdatedAt = service.clock().UTC()

	action := AuditActionPolicyUpdated
	if enabledChanged {
		changed = append(changed, "enabled")
		sort.Strings(changed)
		if updated.Enabled {
			action = AuditActionPolicyEnabled
		} else {
			action = AuditActionPolicyDisabled
		}
	}
	eventID, err := service.newID()
	if err != nil {
		return ExecutionPolicy{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	event, err := service.buildAuditEvent(eventID, "", action, actor, updated.UpdatedAt, input.ClientTime, input.Source, input.SourceExcerpt, &current, updated, changed, updated.Revision, input.CorrelationID, input.ClientRequestID)
	if err != nil {
		return ExecutionPolicy{}, err
	}
	return service.repository.UpdatePolicy(ctx, updated, input.ExpectedRevision, event, input.ClientRequestID)
}

func (service *Service) GetPolicy(ctx context.Context, policyID string) (ExecutionPolicy, error) {
	if policyID == "" {
		return ExecutionPolicy{}, ErrInvalidInput
	}
	return service.repository.GetPolicy(ctx, policyID)
}

func (service *Service) ListPolicies(ctx context.Context) ([]ExecutionPolicy, error) {
	return service.repository.ListPolicies(ctx)
}

func (service *Service) RegisterRunner(ctx context.Context, actor Actor, input RegisterRunnerInput) (Runner, error) {
	if actor.ID == "" || input.ClientRequestID == "" {
		return Runner{}, ErrInvalidInput
	}
	if actor.Kind != ActorKindRunner {
		return Runner{}, ErrForbidden
	}
	if strings.TrimSpace(input.DisplayName) == "" {
		return Runner{}, ErrInvalidInput
	}
	if err := service.validateRunnerScopes(ctx, input.Projects, input.Adapters); err != nil {
		return Runner{}, err
	}

	now := service.clock().UTC()
	existing, err := service.repository.GetRunner(ctx, actor.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Runner{}, err
	}
	registered := errors.Is(err, ErrNotFound)
	runner := Runner{
		ID:           actor.ID,
		DisplayName:  strings.TrimSpace(input.DisplayName),
		Projects:     dedupeStrings(input.Projects),
		Adapters:     dedupeStrings(input.Adapters),
		RegisteredAt: now,
		LastSeenAt:   now,
		Revision:     1,
	}
	action := AuditActionRunnerRegistered
	if !registered {
		runner.RegisteredAt = existing.RegisteredAt
		runner.Revision = existing.Revision + 1
		action = AuditActionRunnerUpdated
	}
	eventID, err := service.newID()
	if err != nil {
		return Runner{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	event, err := service.buildAuditEvent(eventID, "", action, actor, now, input.ClientTime, input.Source, input.SourceExcerpt, nil, runner, []string{"adapters", "display_name", "projects"}, runner.Revision, input.CorrelationID, input.ClientRequestID)
	if err != nil {
		return Runner{}, err
	}
	return service.repository.UpsertRunner(ctx, runner, event, input.ClientRequestID)
}

func (service *Service) UpdateRunner(ctx context.Context, actor Actor, runnerID string, input UpdateRunnerInput) (Runner, error) {
	if actor.ID == "" || runnerID == "" || input.ClientRequestID == "" {
		return Runner{}, ErrInvalidInput
	}
	if actor.Kind != ActorKindOwner {
		return Runner{}, ErrForbidden
	}

	current, err := service.repository.GetRunner(ctx, runnerID)
	if err != nil {
		return Runner{}, err
	}
	if current.Revision != input.ExpectedRevision {
		return Runner{}, ErrRevisionConflict
	}

	updated := current
	if input.DisplayName != nil {
		name := strings.TrimSpace(*input.DisplayName)
		if name == "" {
			return Runner{}, ErrInvalidInput
		}
		updated.DisplayName = name
	}
	if input.Projects != nil {
		updated.Projects = dedupeStrings(*input.Projects)
	}
	if input.Adapters != nil {
		updated.Adapters = dedupeStrings(*input.Adapters)
	}
	if err := service.validateRunnerScopes(ctx, updated.Projects, updated.Adapters); err != nil {
		return Runner{}, err
	}
	updated.Revision++

	eventID, err := service.newID()
	if err != nil {
		return Runner{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	event, err := service.buildAuditEvent(eventID, "", AuditActionRunnerUpdated, actor, service.clock().UTC(), input.ClientTime, input.Source, input.SourceExcerpt, &current, updated, []string{"adapters", "display_name", "projects"}, updated.Revision, input.CorrelationID, input.ClientRequestID)
	if err != nil {
		return Runner{}, err
	}
	return service.repository.UpdateRunner(ctx, updated, input.ExpectedRevision, event, input.ClientRequestID)
}

func (service *Service) GetRunner(ctx context.Context, runnerID string) (Runner, error) {
	if runnerID == "" {
		return Runner{}, ErrInvalidInput
	}
	return service.repository.GetRunner(ctx, runnerID)
}

func (service *Service) ListRunners(ctx context.Context) ([]Runner, error) {
	return service.repository.ListRunners(ctx)
}

func (service *Service) validateRunnerScopes(ctx context.Context, projects []string, adapters []string) error {
	for _, projectID := range projects {
		if projectID == "" {
			return ErrInvalidInput
		}
		if _, err := service.repository.GetProject(ctx, projectID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return ErrInvalidInput
			}
			return err
		}
	}
	for _, adapter := range adapters {
		if !ValidHarness(adapter) {
			return ErrInvalidInput
		}
	}
	return nil
}

func (service *Service) CreateManualRun(ctx context.Context, actor Actor, input CreateManualRunInput) (AgentRun, error) {
	if actor.ID == "" || input.ReminderID == "" || input.PolicyID == "" || input.ClientRequestID == "" {
		return AgentRun{}, ErrInvalidInput
	}
	if actor.Kind != ActorKindOwner {
		return AgentRun{}, ErrForbidden
	}
	reminder, err := service.repository.GetReminder(ctx, input.ReminderID)
	if err != nil {
		return AgentRun{}, err
	}
	policy, err := service.repository.GetPolicy(ctx, input.PolicyID)
	if err != nil {
		return AgentRun{}, err
	}
	project, err := service.repository.GetProject(ctx, policy.ProjectID)
	if err != nil {
		return AgentRun{}, err
	}

	runID, err := service.newID()
	if err != nil {
		return AgentRun{}, fmt.Errorf("generate run ID: %w", err)
	}
	eventID, err := service.newID()
	if err != nil {
		return AgentRun{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	now := service.clock().UTC()
	cursor, err := service.repository.LatestChangeCursor(ctx)
	if err != nil {
		return AgentRun{}, err
	}
	run := AgentRun{
		ID:             runID,
		ReminderID:     reminder.ID,
		PolicyID:       policy.ID,
		PolicyRevision: policy.Revision,
		ProjectID:      project.ID,
		Adapter:        policy.Adapter,
		Status:         AgentRunStatusEligible,
		IdempotencyKey: input.ClientRequestID,
		TaskContract:   buildTaskContract(runID, reminder, policy, project),
		ContextCursor:  cursor,
		RequestedAt:    &now,
		CreatedByActor: actor,
		Revision:       1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	event, err := service.buildRunEvent(eventID, run, AuditActionRunEligible, actor, now, input.ClientTime, input.Source, input.SourceExcerpt, nil, run, []string{"adapter", "context_cursor", "occurrence_id", "policy_id", "policy_revision", "project_id", "reminder_id", "status", "task_contract"}, input.ClientRequestID)
	if err != nil {
		return AgentRun{}, err
	}
	created, _, err := service.repository.CreateAgentRun(ctx, run, event, input.ClientRequestID)
	if err != nil {
		return AgentRun{}, err
	}
	return created, nil
}

// MaterializeEligibleRuns creates one eligible run per due occurrence whose
// reminder carries an enabled execution policy. It is idempotent through the
// (occurrence_id, policy_revision) unique key.
func (service *Service) MaterializeEligibleRuns(ctx context.Context, now time.Time) ([]AgentRun, error) {
	now = now.UTC()
	due, err := service.repository.ListDueOccurrences(ctx, now)
	if err != nil {
		return nil, err
	}
	created := make([]AgentRun, 0)
	for _, item := range due {
		reminder := item.Reminder
		occurrence := item.Occurrence
		if reminder.ExecutionPolicyID == nil || *reminder.ExecutionPolicyID == "" {
			continue
		}
		policy, err := service.repository.GetPolicy(ctx, *reminder.ExecutionPolicyID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !policy.Enabled {
			continue
		}
		project, err := service.repository.GetProject(ctx, policy.ProjectID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		runID, err := service.newID()
		if err != nil {
			return nil, fmt.Errorf("generate run ID: %w", err)
		}
		eventID, err := service.newID()
		if err != nil {
			return nil, fmt.Errorf("generate audit event ID: %w", err)
		}
		cursor, err := service.repository.LatestChangeCursor(ctx)
		if err != nil {
			return nil, err
		}
		occurrenceID := occurrence.ID
		idempotencyKey := fmt.Sprintf("materialize:%s:%s:%d", occurrence.ID, policy.ID, policy.Revision)
		run := AgentRun{
			ID:             runID,
			ReminderID:     reminder.ID,
			OccurrenceID:   &occurrenceID,
			PolicyID:       policy.ID,
			PolicyRevision: policy.Revision,
			ProjectID:      project.ID,
			Adapter:        policy.Adapter,
			Status:         AgentRunStatusEligible,
			IdempotencyKey: idempotencyKey,
			TaskContract:   buildTaskContract(runID, reminder, policy, project),
			ContextCursor:  cursor,
			RequestedAt:    &now,
			CreatedByActor: SystemActor(),
			Revision:       1,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		event, err := service.buildRunEvent(eventID, run, AuditActionRunEligible, SystemActor(), now, nil, "", "", nil, run, []string{"adapter", "context_cursor", "occurrence_id", "policy_id", "policy_revision", "project_id", "reminder_id", "status", "task_contract"}, idempotencyKey)
		if err != nil {
			return nil, err
		}
		run, wasCreated, err := service.repository.CreateAgentRun(ctx, run, event, idempotencyKey)
		if err != nil {
			return nil, err
		}
		if wasCreated {
			created = append(created, run)
		}
	}
	return created, nil
}

// ClaimAgentRun atomically leases the oldest eligible run the runner may
// serve. With WaitSeconds > 0 it long-polls, bounded by the caller's context
// and the server-side cap.
func (service *Service) ClaimAgentRun(ctx context.Context, actor Actor, input ClaimRunInput) (AgentRun, error) {
	if actor.ID == "" {
		return AgentRun{}, ErrInvalidInput
	}
	if actor.Kind != ActorKindRunner {
		return AgentRun{}, ErrForbidden
	}
	runner, err := service.repository.GetRunner(ctx, actor.ID)
	if err != nil {
		return AgentRun{}, err
	}
	waitSeconds := input.WaitSeconds
	if waitSeconds < 0 {
		waitSeconds = 0
	}
	if waitSeconds > maxClaimWaitSeconds {
		waitSeconds = maxClaimWaitSeconds
	}
	deadline := service.clock().Add(time.Duration(waitSeconds) * time.Second)
	if err := service.repository.TouchRunnerSeen(ctx, runner.ID, service.clock().UTC()); err != nil && !errors.Is(err, ErrNotFound) {
		return AgentRun{}, err
	}
	for {
		now := service.clock().UTC()
		candidates, err := service.repository.ListClaimableRuns(ctx, runner, now)
		if err != nil {
			return AgentRun{}, err
		}
		for _, candidate := range candidates {
			claimed, err := service.tryClaimRun(ctx, actor, candidate, now)
			if errors.Is(err, ErrNotClaimable) {
				continue
			}
			if err != nil {
				return AgentRun{}, err
			}
			return claimed, nil
		}
		if waitSeconds == 0 || !now.Before(deadline) {
			return AgentRun{}, ErrNotClaimable
		}
		select {
		case <-ctx.Done():
			return AgentRun{}, ctx.Err()
		case <-time.After(claimPollInterval):
		}
	}
}

func (service *Service) tryClaimRun(ctx context.Context, actor Actor, candidate AgentRun, now time.Time) (AgentRun, error) {
	policy, err := service.repository.GetPolicy(ctx, candidate.PolicyID)
	if errors.Is(err, ErrNotFound) {
		return AgentRun{}, ErrNotClaimable
	}
	if err != nil {
		return AgentRun{}, err
	}
	leaseExpiresAt := now.Add(leaseDuration(policy.TimeoutMinutes))
	runnerID := actor.ID
	updated := cloneAgentRun(candidate)
	updated.Status = AgentRunStatusClaimed
	updated.RunnerID = &runnerID
	updated.ClaimedAt = &now
	updated.LeaseExpiresAt = &leaseExpiresAt
	updated.Revision++
	updated.UpdatedAt = now
	eventID, err := service.newID()
	if err != nil {
		return AgentRun{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	event, err := service.buildRunEvent(eventID, updated, AuditActionRunClaimed, actor, now, nil, "", "", &candidate, updated, []string{"claimed_at", "lease_expires_at", "runner_id", "status"}, fmt.Sprintf("claim:%s:%d", candidate.ID, updated.Revision))
	if err != nil {
		return AgentRun{}, err
	}
	return service.repository.ClaimAgentRun(ctx, updated, candidate.Revision, event)
}

func (service *Service) ReportAgentRunEvent(ctx context.Context, actor Actor, input ReportRunEventInput) (AgentRun, error) {
	if actor.ID == "" || input.RunID == "" {
		return AgentRun{}, ErrInvalidInput
	}
	if actor.Kind != ActorKindRunner {
		return AgentRun{}, ErrForbidden
	}
	if input.Event != RunEventStarted && input.Event != RunEventProgress && input.Event != RunEventHeartbeat {
		return AgentRun{}, ErrInvalidInput
	}
	run, err := service.repository.GetAgentRun(ctx, input.RunID)
	if err != nil {
		return AgentRun{}, err
	}
	if run.RunnerID == nil || *run.RunnerID != actor.ID {
		return AgentRun{}, ErrForbidden
	}
	if run.Status != AgentRunStatusClaimed && run.Status != AgentRunStatusRunning {
		return AgentRun{}, ErrRunStateConflict
	}
	if run.Revision != input.ExpectedRevision {
		return AgentRun{}, ErrRunStateConflict
	}
	now := service.clock().UTC()

	if input.Event == RunEventHeartbeat {
		policy, err := service.repository.GetPolicy(ctx, run.PolicyID)
		if err != nil {
			return AgentRun{}, err
		}
		leaseExpiresAt := now.Add(leaseDuration(policy.TimeoutMinutes))
		updated := cloneAgentRun(run)
		updated.LeaseExpiresAt = &leaseExpiresAt
		updated.Revision++
		updated.UpdatedAt = now
		updatedRun, _, err := service.repository.UpdateAgentRunTransition(ctx, updated, run.Revision, nil, nil, nil, "")
		if err != nil {
			return AgentRun{}, err
		}
		return updatedRun, nil
	}

	updated := cloneAgentRun(run)
	updated.Revision++
	updated.UpdatedAt = now
	action := AuditActionRunProgress
	changed := []string{"progress"}
	if input.Event == RunEventStarted {
		if run.Status != AgentRunStatusClaimed {
			return AgentRun{}, ErrRunStateConflict
		}
		updated.Status = AgentRunStatusRunning
		updated.StartedAt = &now
		action = AuditActionRunStarted
		changed = []string{"started_at", "status"}
	}
	eventID, err := service.newID()
	if err != nil {
		return AgentRun{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	event, err := service.buildRunEvent(eventID, updated, action, actor, now, nil, "", firstLine(redactSummary(input.Detail)), &run, updated, changed, fmt.Sprintf("report:%s:%d", run.ID, updated.Revision))
	if err != nil {
		return AgentRun{}, err
	}
	updatedRun, _, err := service.repository.UpdateAgentRunTransition(ctx, updated, run.Revision, &event, nil, nil, "")
	if err != nil {
		return AgentRun{}, err
	}
	return updatedRun, nil
}

// CompleteAgentRun finalizes a claimed or running run. A disagreement between
// the reported outcome and the process exit code forces a failure with
// failure_code=evidence_mismatch. On a verified success the originating
// occurrence is completed in the same transaction when the policy asks for
// it.
func (service *Service) CompleteAgentRun(ctx context.Context, actor Actor, input CompleteRunInput) (AgentRun, error) {
	if actor.ID == "" || input.RunID == "" || input.ClientRequestID == "" {
		return AgentRun{}, ErrInvalidInput
	}
	if actor.Kind != ActorKindRunner {
		return AgentRun{}, ErrForbidden
	}
	if input.Outcome != AgentRunStatusSucceeded && input.Outcome != AgentRunStatusFailed {
		return AgentRun{}, ErrInvalidInput
	}
	run, err := service.repository.GetAgentRun(ctx, input.RunID)
	if err != nil {
		return AgentRun{}, err
	}
	if run.RunnerID == nil || *run.RunnerID != actor.ID {
		return AgentRun{}, ErrForbidden
	}
	if run.Status != AgentRunStatusClaimed && run.Status != AgentRunStatusRunning {
		return AgentRun{}, ErrRunStateConflict
	}
	if run.Revision != input.ExpectedRevision {
		return AgentRun{}, ErrRunStateConflict
	}
	if input.FailureCode != "" && input.FailureCode != RunFailureAdapterUnavailable {
		return AgentRun{}, fmt.Errorf("%w: unsupported failure code %q", ErrInvalidInput, input.FailureCode)
	}
	if input.FailureCode != "" && input.Outcome != AgentRunStatusFailed {
		return AgentRun{}, fmt.Errorf("%w: failure code requires a failed outcome", ErrInvalidInput)
	}
	policy, err := service.repository.GetPolicy(ctx, run.PolicyID)
	if err != nil {
		return AgentRun{}, err
	}

	terminal := input.Outcome
	failureCode := ""
	if (input.Outcome == AgentRunStatusSucceeded && input.ExitCode != 0) || (input.Outcome == AgentRunStatusFailed && input.ExitCode == 0) {
		terminal = AgentRunStatusFailed
		failureCode = RunFailureEvidenceMismatch
	}
	if terminal == AgentRunStatusFailed && failureCode == "" {
		failureCode = input.FailureCode
	}
	now := service.clock().UTC()
	updated := cloneAgentRun(run)
	updated.Status = terminal
	updated.FinishedAt = &now
	updated.LeaseExpiresAt = nil
	updated.ResultSummary = redactSummary(input.ResultSummary)
	updated.ResultArtifactRef = strings.TrimSpace(input.ResultArtifactRef)
	updated.FailureCode = failureCode
	completedBy := actor
	updated.CompletedByActor = &completedBy
	updated.Revision++
	updated.UpdatedAt = now

	action := AuditActionRunSucceeded
	if terminal == AgentRunStatusFailed {
		action = AuditActionRunFailed
	}
	eventID, err := service.newID()
	if err != nil {
		return AgentRun{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	event, err := service.buildRunEvent(eventID, updated, action, actor, now, input.ClientTime, input.Source, firstLine(updated.ResultSummary), &run, updated, []string{"completed_by", "failure_code", "finished_at", "lease_expires_at", "result_artifact_ref", "result_summary", "status"}, input.ClientRequestID)
	if err != nil {
		return AgentRun{}, err
	}

	var occurrence *Occurrence
	var occurrenceEvent *AuditEvent
	if terminal == AgentRunStatusSucceeded && policy.MarkOccurrenceDoneOnSuccess && run.OccurrenceID != nil {
		current, err := service.repository.GetOccurrence(ctx, *run.OccurrenceID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return AgentRun{}, err
		}
		if err == nil && (current.Status == OccurrenceStatusPending || current.Status == OccurrenceStatusSnoozed) {
			completed := cloneOccurrence(current)
			completed.Status = OccurrenceStatusCompleted
			completed.CompletedAt = &now
			completed.SnoozedUntil = nil
			completed.Revision++
			completed.UpdatedAt = now
			occurrenceEventID, err := service.newID()
			if err != nil {
				return AgentRun{}, fmt.Errorf("generate audit event ID: %w", err)
			}
			builtEvent, err := service.buildAuditEvent(occurrenceEventID, current.ReminderID, AuditActionOccurrenceDone, actor, now, input.ClientTime, input.Source, input.SourceExcerpt, current, completed, []string{"occurrence.completed_at", "occurrence.status"}, completed.Revision, run.TaskContract.CorrelationID, input.ClientRequestID+"/occurrence")
			if err != nil {
				return AgentRun{}, err
			}
			occurrenceEvent = &builtEvent
			occurrence = &completed
		}
	}

	completed, applied, err := service.repository.UpdateAgentRunTransition(ctx, updated, run.Revision, &event, occurrence, occurrenceEvent, input.ClientRequestID)
	if err != nil {
		return AgentRun{}, err
	}
	if applied {
		service.notifyRunFinished(ctx, completed, policy)
	}
	return completed, nil
}

func (service *Service) RequestAgentApproval(ctx context.Context, actor Actor, input RequestApprovalInput) (AgentRun, error) {
	if actor.ID == "" || input.RunID == "" {
		return AgentRun{}, ErrInvalidInput
	}
	if actor.Kind != ActorKindRunner {
		return AgentRun{}, ErrForbidden
	}
	if !ValidCapability(input.Capability) {
		return AgentRun{}, ErrInvalidInput
	}
	run, err := service.repository.GetAgentRun(ctx, input.RunID)
	if err != nil {
		return AgentRun{}, err
	}
	if run.RunnerID == nil || *run.RunnerID != actor.ID {
		return AgentRun{}, ErrForbidden
	}
	if run.Status != AgentRunStatusRunning {
		return AgentRun{}, ErrRunStateConflict
	}
	if run.Revision != input.ExpectedRevision {
		return AgentRun{}, ErrRunStateConflict
	}
	policy, err := service.repository.GetPolicy(ctx, run.PolicyID)
	if err != nil {
		return AgentRun{}, err
	}
	if contains(policy.AllowedCapabilities, input.Capability) {
		return AgentRun{}, ErrInvalidInput
	}

	now := service.clock().UTC()
	updated := cloneAgentRun(run)
	updated.Status = AgentRunStatusNeedsApproval
	updated.ApprovalCapability = input.Capability
	updated.Revision++
	updated.UpdatedAt = now
	eventID, err := service.newID()
	if err != nil {
		return AgentRun{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	event, err := service.buildRunEvent(eventID, updated, AuditActionRunApprovalRequested, actor, now, nil, "", firstLine(redactSummary(input.Reason)), &run, updated, []string{"approval_capability", "status"}, fmt.Sprintf("approval-request:%s:%d", run.ID, updated.Revision))
	if err != nil {
		return AgentRun{}, err
	}
	updatedRun, _, err := service.repository.UpdateAgentRunTransition(ctx, updated, run.Revision, &event, nil, nil, "")
	if err != nil {
		return AgentRun{}, err
	}
	return updatedRun, nil
}

func (service *Service) ApproveAgentRun(ctx context.Context, actor Actor, input ApproveRunInput) (AgentRun, error) {
	if actor.ID == "" || input.RunID == "" || input.ClientRequestID == "" {
		return AgentRun{}, ErrInvalidInput
	}
	if actor.Kind != ActorKindOwner {
		return AgentRun{}, ErrForbidden
	}
	run, err := service.repository.GetAgentRun(ctx, input.RunID)
	if err != nil {
		return AgentRun{}, err
	}
	if run.Status != AgentRunStatusNeedsApproval {
		return AgentRun{}, ErrRunStateConflict
	}
	if run.Revision != input.ExpectedRevision {
		return AgentRun{}, ErrRunStateConflict
	}
	policy, err := service.repository.GetPolicy(ctx, run.PolicyID)
	if err != nil {
		return AgentRun{}, err
	}

	now := service.clock().UTC()
	updated := cloneAgentRun(run)
	updated.ApprovalCapability = ""
	updated.Revision++
	updated.UpdatedAt = now
	action := AuditActionRunApproved
	changed := []string{"approval_capability", "lease_expires_at", "status"}
	if input.Approved {
		leaseExpiresAt := now.Add(leaseDuration(policy.TimeoutMinutes))
		updated.Status = AgentRunStatusClaimed
		updated.LeaseExpiresAt = &leaseExpiresAt
	} else {
		updated.Status = AgentRunStatusCancelled
		updated.FinishedAt = &now
		updated.LeaseExpiresAt = nil
		updated.FailureCode = RunFailureApprovalDeclined
		completedBy := actor
		updated.CompletedByActor = &completedBy
		action = AuditActionRunDeclined
		changed = []string{"approval_capability", "completed_by", "failure_code", "finished_at", "lease_expires_at", "status"}
	}
	eventID, err := service.newID()
	if err != nil {
		return AgentRun{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	event, err := service.buildRunEvent(eventID, updated, action, actor, now, input.ClientTime, input.Source, input.SourceExcerpt, &run, updated, changed, input.ClientRequestID)
	if err != nil {
		return AgentRun{}, err
	}
	updatedRun, applied, err := service.repository.UpdateAgentRunTransition(ctx, updated, run.Revision, &event, nil, nil, input.ClientRequestID)
	if err != nil {
		return AgentRun{}, err
	}
	if applied && !input.Approved {
		service.notifyRunFinished(ctx, updatedRun, policy)
	}
	return updatedRun, nil
}

func (service *Service) CancelAgentRun(ctx context.Context, actor Actor, input CancelRunInput) (AgentRun, error) {
	if actor.ID == "" || input.RunID == "" || input.ClientRequestID == "" {
		return AgentRun{}, ErrInvalidInput
	}
	if actor.Kind != ActorKindOwner {
		return AgentRun{}, ErrForbidden
	}
	run, err := service.repository.GetAgentRun(ctx, input.RunID)
	if err != nil {
		return AgentRun{}, err
	}
	if run.Terminal() {
		return AgentRun{}, ErrRunStateConflict
	}
	if run.Revision != input.ExpectedRevision {
		return AgentRun{}, ErrRunStateConflict
	}
	policy, err := service.repository.GetPolicy(ctx, run.PolicyID)
	if err != nil {
		return AgentRun{}, err
	}

	now := service.clock().UTC()
	updated := cloneAgentRun(run)
	updated.Status = AgentRunStatusCancelled
	updated.FinishedAt = &now
	updated.LeaseExpiresAt = nil
	updated.ApprovalCapability = ""
	completedBy := actor
	updated.CompletedByActor = &completedBy
	updated.Revision++
	updated.UpdatedAt = now
	eventID, err := service.newID()
	if err != nil {
		return AgentRun{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	event, err := service.buildRunEvent(eventID, updated, AuditActionRunCancelled, actor, now, input.ClientTime, input.Source, input.SourceExcerpt, &run, updated, []string{"approval_capability", "completed_by", "finished_at", "lease_expires_at", "status"}, input.ClientRequestID)
	if err != nil {
		return AgentRun{}, err
	}
	updatedRun, applied, err := service.repository.UpdateAgentRunTransition(ctx, updated, run.Revision, &event, nil, nil, input.ClientRequestID)
	if err != nil {
		return AgentRun{}, err
	}
	if applied {
		service.notifyRunFinished(ctx, updatedRun, policy)
	}
	return updatedRun, nil
}

func (service *Service) GetAgentRun(ctx context.Context, runID string) (AgentRun, error) {
	if runID == "" {
		return AgentRun{}, ErrInvalidInput
	}
	return service.repository.GetAgentRun(ctx, runID)
}

func (service *Service) ListAgentRuns(ctx context.Context, filter AgentRunListFilter) ([]AgentRun, error) {
	return service.repository.ListAgentRuns(ctx, filter)
}

// RequeueExpiredLeases returns claimed runs whose lease expired to the
// eligible pool. Called by the execution scheduler.
func (service *Service) RequeueExpiredLeases(ctx context.Context, now time.Time) ([]AgentRun, error) {
	return service.repository.RequeueExpiredLeases(ctx, now.UTC())
}

// ExpireStaleRuns retires planned or eligible runs that outlived their
// request window, lost their occurrence to a reschedule, or reference a
// disabled policy. Called by the execution scheduler.
func (service *Service) ExpireStaleRuns(ctx context.Context, now time.Time) ([]AgentRun, error) {
	return service.repository.ExpireStaleRuns(ctx, now.UTC())
}

// notifyRunFinished invokes the injected run notifier best-effort: nil-safe,
// bounded to two seconds, errors swallowed.
func (service *Service) notifyRunFinished(ctx context.Context, run AgentRun, policy ExecutionPolicy) {
	if service.runNotifier == nil {
		return
	}
	notify := (run.Status == AgentRunStatusSucceeded && policy.NotifyOnCompletion) ||
		((run.Status == AgentRunStatusFailed || run.Status == AgentRunStatusCancelled) && policy.NotifyOnFailure)
	if !notify {
		return
	}
	reminder, err := service.repository.GetReminder(ctx, run.ReminderID)
	if err != nil {
		return
	}
	notifyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = service.runNotifier(notifyCtx, run, reminder.Title)
}

// buildTaskContract assembles the immutable task contract of a run from the
// reminder, the pinned policy revision and the project.
func buildTaskContract(runID string, reminder Reminder, policy ExecutionPolicy, project Project) TaskContract {
	criteria := make([]string, 0, 1)
	if strings.TrimSpace(reminder.Description) != "" {
		criteria = append(criteria, reminder.Description)
	}
	completionRule := ""
	if policy.MarkOccurrenceDoneOnSuccess {
		completionRule = CompletionRuleMarkOccurrenceDoneOnSuccess
	}
	contract := TaskContract{
		RunID:               runID,
		CorrelationID:       runID,
		Objective:           reminder.Title,
		AcceptanceCriteria:  criteria,
		ProjectID:           project.ID,
		ProjectName:         project.Name,
		PolicyID:            policy.ID,
		PolicyRevision:      policy.Revision,
		AllowedCapabilities: append([]string(nil), policy.AllowedCapabilities...),
		TimeoutMinutes:      policy.TimeoutMinutes,
		CompletionRule:      completionRule,
	}
	contract.ContractHash = contract.ComputeHash()
	return contract
}

// buildAuditEvent assembles an audit event with snapshots for the entity
// lifecycle methods (projects, policies, runners). A nil before value marks a
// creation event.
func (service *Service) buildAuditEvent(id string, reminderID string, action AuditAction, actor Actor, serverTime time.Time, clientTime *time.Time, source string, sourceExcerpt string, before any, after any, changedFields []string, revision int64, requestedCorrelationID string, clientRequestID string) (AuditEvent, error) {
	var beforeSnapshot json.RawMessage
	if before != nil {
		encoded, err := json.Marshal(before)
		if err != nil {
			return AuditEvent{}, fmt.Errorf("encode previous snapshot: %w", err)
		}
		beforeSnapshot = encoded
	}
	afterSnapshot, err := json.Marshal(after)
	if err != nil {
		return AuditEvent{}, fmt.Errorf("encode updated snapshot: %w", err)
	}
	return AuditEvent{
		ID:              id,
		ReminderID:      reminderID,
		Action:          action,
		Actor:           actor,
		ServerTime:      serverTime,
		ClientTime:      clientTime,
		Source:          source,
		SourceExcerpt:   sourceExcerpt,
		BeforeSnapshot:  beforeSnapshot,
		AfterSnapshot:   afterSnapshot,
		ChangedFields:   changedFields,
		Revision:        revision,
		CorrelationID:   correlationID(requestedCorrelationID, clientRequestID),
		ClientRequestID: clientRequestID,
	}, nil
}

// buildRunEvent assembles an audit event in the run's correlation scope. The
// correlation ID always stays the run's contract correlation ID so the run
// timeline can be filtered by it.
func (service *Service) buildRunEvent(id string, run AgentRun, action AuditAction, actor Actor, serverTime time.Time, clientTime *time.Time, source string, sourceExcerpt string, before *AgentRun, after AgentRun, changedFields []string, clientRequestID string) (AuditEvent, error) {
	var beforeValue any
	if before != nil {
		beforeValue = *before
	}
	return service.buildAuditEvent(id, run.ReminderID, action, actor, serverTime, clientTime, source, sourceExcerpt, beforeValue, after, changedFields, after.Revision, run.TaskContract.CorrelationID, clientRequestID)
}

// leaseDuration bounds a run lease to twice the policy timeout, clamped to
// 2..480 minutes.
func leaseDuration(timeoutMinutes int) time.Duration {
	minutes := 2 * timeoutMinutes
	if minutes < 2 {
		minutes = 2
	}
	if minutes > 480 {
		minutes = 480
	}
	return time.Duration(minutes) * time.Minute
}

// redactSummary strips secret-looking lines and bounds the result to 2 000
// characters before a summary is persisted or indexed.
func redactSummary(summary string) string {
	lines := strings.Split(summary, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if secretLinePattern.MatchString(line) {
			continue
		}
		kept = append(kept, line)
	}
	redacted := strings.Join(kept, "\n")
	runes := []rune(redacted)
	if len(runes) > maxResultSummaryLength {
		redacted = string(runes[:maxResultSummaryLength])
	}
	return redacted
}

func firstLine(value string) string {
	line, _, _ := strings.Cut(value, "\n")
	return line
}

func clonePolicy(policy ExecutionPolicy) ExecutionPolicy {
	policy.AllowedCapabilities = append([]string(nil), policy.AllowedCapabilities...)
	return policy
}

func cloneAgentRun(run AgentRun) AgentRun {
	run.OccurrenceID = cloneStringPointer(run.OccurrenceID)
	run.RunnerID = cloneStringPointer(run.RunnerID)
	run.TaskContract.AcceptanceCriteria = append([]string(nil), run.TaskContract.AcceptanceCriteria...)
	run.TaskContract.AllowedCapabilities = append([]string(nil), run.TaskContract.AllowedCapabilities...)
	run.LeaseExpiresAt = cloneTimePointer(run.LeaseExpiresAt)
	run.RequestedAt = cloneTimePointer(run.RequestedAt)
	run.ClaimedAt = cloneTimePointer(run.ClaimedAt)
	run.StartedAt = cloneTimePointer(run.StartedAt)
	run.FinishedAt = cloneTimePointer(run.FinishedAt)
	if run.CompletedByActor != nil {
		actor := *run.CompletedByActor
		run.CompletedByActor = &actor
	}
	return run
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func equalStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
