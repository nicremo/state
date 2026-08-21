package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// StaleRunMaxAge bounds how long a planned or eligible run may sit unclaimed
// before the expiry sweep retires it.
const StaleRunMaxAge = 24 * time.Hour

type ExecutionMode string

const (
	ExecutionModeSupervised        ExecutionMode = "supervised"
	ExecutionModeUnattendedLowRisk ExecutionMode = "unattended-low-risk"
)

// The capability vocabulary is a closed enum; server-side validation rejects
// anything outside this set.
const (
	CapabilityReadRepository   = "read_repository"
	CapabilityEditRepository   = "edit_repository"
	CapabilityRunTests         = "run_tests"
	CapabilityReadStateContext = "read_state_context"
	CapabilityWriteState       = "write_state"
	CapabilityNetworkAccess    = "network_access"
	CapabilityDeploy           = "deploy"
	CapabilityMessageExternal  = "message_external"
	CapabilityDestructive      = "destructive"
)

// unattendedLowRiskCapabilities is the explicit allow-list a policy in
// unattended-low-risk mode may carry. Anything else requires supervised mode.
var unattendedLowRiskCapabilities = []string{
	CapabilityReadRepository,
	CapabilityReadStateContext,
	CapabilityRunTests,
	CapabilityEditRepository,
}

const (
	PolicyTimeoutMinMinutes = 1
	PolicyTimeoutMaxMinutes = 240
)

// CompletionRuleMarkOccurrenceDoneOnSuccess completes the originating
// occurrence when the run finishes successfully with a matching exit status.
const CompletionRuleMarkOccurrenceDoneOnSuccess = "mark_occurrence_done_on_success"

// Run failure codes recorded in AgentRun.FailureCode.
const (
	RunFailureEvidenceMismatch = "evidence_mismatch"
	RunFailureApprovalDeclined = "approval_declined"
	// RunFailureAdapterUnavailable is the only failure code a runner may
	// supply itself; the server-owned codes above cannot be forged.
	RunFailureAdapterUnavailable = "adapter_unavailable"
)

// Run lifecycle event names accepted by ReportRunEventInput.
const (
	RunEventStarted   = "started"
	RunEventProgress  = "progress"
	RunEventHeartbeat = "heartbeat"
)

// ValidCapability reports whether a capability is part of the closed
// vocabulary.
func ValidCapability(capability string) bool {
	switch capability {
	case CapabilityReadRepository,
		CapabilityEditRepository,
		CapabilityRunTests,
		CapabilityReadStateContext,
		CapabilityWriteState,
		CapabilityNetworkAccess,
		CapabilityDeploy,
		CapabilityMessageExternal,
		CapabilityDestructive:
		return true
	}
	return false
}

// ValidUnattendedCapability reports whether a capability may run without a
// human in the loop.
func ValidUnattendedCapability(capability string) bool {
	for _, allowed := range unattendedLowRiskCapabilities {
		if allowed == capability {
			return true
		}
	}
	return false
}

// ValidPolicyConfiguration validates the mode, adapter, capability set and
// timeout bounds of a policy. Project existence is checked by the service.
func ValidPolicyConfiguration(policy ExecutionPolicy) error {
	if !ValidProjectSlug(policy.Name) {
		return ErrInvalidInput
	}
	if !ValidHarness(policy.Adapter) {
		return ErrInvalidInput
	}
	if policy.Mode != ExecutionModeSupervised && policy.Mode != ExecutionModeUnattendedLowRisk {
		return ErrInvalidInput
	}
	if policy.TimeoutMinutes < PolicyTimeoutMinMinutes || policy.TimeoutMinutes > PolicyTimeoutMaxMinutes {
		return ErrInvalidInput
	}
	seen := make(map[string]struct{}, len(policy.AllowedCapabilities))
	for _, capability := range policy.AllowedCapabilities {
		if !ValidCapability(capability) {
			return ErrInvalidInput
		}
		if _, duplicate := seen[capability]; duplicate {
			return ErrInvalidInput
		}
		seen[capability] = struct{}{}
		if policy.Mode == ExecutionModeUnattendedLowRisk && !ValidUnattendedCapability(capability) {
			return ErrPolicyViolation
		}
	}
	return nil
}

type Project struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	RootPathHint string    `json:"root_path_hint,omitempty"`
	Revision     int64     `json:"revision"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type ExecutionPolicy struct {
	ID                          string        `json:"id"`
	Name                        string        `json:"name"`
	ProjectID                   string        `json:"project_id"`
	Adapter                     string        `json:"adapter"`
	Mode                        ExecutionMode `json:"mode"`
	AllowedCapabilities         []string      `json:"allowed_capabilities"`
	MarkOccurrenceDoneOnSuccess bool          `json:"mark_occurrence_done_on_success"`
	NotifyOnStart               bool          `json:"notify_on_start"`
	NotifyOnCompletion          bool          `json:"notify_on_completion"`
	NotifyOnFailure             bool          `json:"notify_on_failure"`
	TimeoutMinutes              int           `json:"timeout_minutes"`
	Enabled                     bool          `json:"enabled"`
	Revision                    int64         `json:"revision"`
	CreatedAt                   time.Time     `json:"created_at"`
	UpdatedAt                   time.Time     `json:"updated_at"`
}

type Runner struct {
	ID           string    `json:"id"`
	DisplayName  string    `json:"display_name"`
	Projects     []string  `json:"projects"`
	Adapters     []string  `json:"adapters"`
	RegisteredAt time.Time `json:"registered_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	Revision     int64     `json:"revision"`
}

type AgentRunStatus string

const (
	AgentRunStatusPlanned       AgentRunStatus = "planned"
	AgentRunStatusEligible      AgentRunStatus = "eligible"
	AgentRunStatusClaimed       AgentRunStatus = "claimed"
	AgentRunStatusRunning       AgentRunStatus = "running"
	AgentRunStatusNeedsApproval AgentRunStatus = "needs_approval"
	AgentRunStatusSucceeded     AgentRunStatus = "succeeded"
	AgentRunStatusFailed        AgentRunStatus = "failed"
	AgentRunStatusCancelled     AgentRunStatus = "cancelled"
	AgentRunStatusExpired       AgentRunStatus = "expired"
)

// Terminal reports whether the run has reached a state nothing transitions
// out of.
func (run AgentRun) Terminal() bool {
	switch run.Status {
	case AgentRunStatusSucceeded, AgentRunStatusFailed, AgentRunStatusCancelled, AgentRunStatusExpired:
		return true
	}
	return false
}

type TaskContract struct {
	RunID               string   `json:"run_id"`
	CorrelationID       string   `json:"correlation_id"`
	Objective           string   `json:"objective"`
	AcceptanceCriteria  []string `json:"acceptance_criteria,omitempty"`
	ProjectID           string   `json:"project_id"`
	ProjectName         string   `json:"project_name"`
	PolicyID            string   `json:"policy_id"`
	PolicyRevision      int64    `json:"policy_revision"`
	ContractHash        string   `json:"contract_hash"`
	AllowedCapabilities []string `json:"allowed_capabilities"`
	TimeoutMinutes      int      `json:"timeout_minutes"`
	CompletionRule      string   `json:"completion_rule,omitempty"`
}

// ComputeHash returns the SHA-256 over the canonical JSON encoding of the
// contract (every field in declaration order, ContractHash cleared). The
// runner recomputes it and refuses mismatches.
func (contract TaskContract) ComputeHash() string {
	contract.ContractHash = ""
	encoded, err := json.Marshal(contract)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

type AgentRun struct {
	ID                 string         `json:"id"`
	ReminderID         string         `json:"reminder_id"`
	OccurrenceID       *string        `json:"occurrence_id,omitempty"`
	PolicyID           string         `json:"policy_id"`
	PolicyRevision     int64          `json:"policy_revision"`
	ProjectID          string         `json:"project_id"`
	Adapter            string         `json:"adapter"`
	RunnerID           *string        `json:"runner_id,omitempty"`
	Status             AgentRunStatus `json:"status"`
	IdempotencyKey     string         `json:"idempotency_key"`
	TaskContract       TaskContract   `json:"task_contract"`
	ContextCursor      int64          `json:"context_cursor"`
	LeaseExpiresAt     *time.Time     `json:"lease_expires_at,omitempty"`
	RequestedAt        *time.Time     `json:"requested_at,omitempty"`
	ClaimedAt          *time.Time     `json:"claimed_at,omitempty"`
	StartedAt          *time.Time     `json:"started_at,omitempty"`
	FinishedAt         *time.Time     `json:"finished_at,omitempty"`
	ResultSummary      string         `json:"result_summary,omitempty"`
	ResultArtifactRef  string         `json:"result_artifact_ref,omitempty"`
	FailureCode        string         `json:"failure_code,omitempty"`
	ApprovalCapability string         `json:"approval_capability,omitempty"`
	CreatedByActor     Actor          `json:"created_by_actor"`
	CompletedByActor   *Actor         `json:"completed_by_actor,omitempty"`
	Revision           int64          `json:"revision"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

// MutationMetadata carries the idempotency and provenance fields of a
// client-initiated run mutation, mirroring the inline metadata of the
// reminder inputs.
type MutationMetadata struct {
	ClientTime      *time.Time `json:"client_time,omitempty"`
	Source          string     `json:"source,omitempty"`
	SourceExcerpt   string     `json:"source_excerpt,omitempty"`
	ClientRequestID string     `json:"client_request_id"`
	CorrelationID   string     `json:"correlation_id,omitempty"`
}

type CreateProjectInput struct {
	Name            string     `json:"name"`
	Description     string     `json:"description,omitempty"`
	RootPathHint    string     `json:"root_path_hint,omitempty"`
	ClientTime      *time.Time `json:"client_time,omitempty"`
	Source          string     `json:"source,omitempty"`
	SourceExcerpt   string     `json:"source_excerpt,omitempty"`
	ClientRequestID string     `json:"client_request_id"`
	CorrelationID   string     `json:"correlation_id,omitempty"`
}

type UpdateProjectInput struct {
	Name             *string    `json:"name,omitempty"`
	Description      *string    `json:"description,omitempty"`
	RootPathHint     *string    `json:"root_path_hint,omitempty"`
	ExpectedRevision int64      `json:"expected_revision"`
	ClientTime       *time.Time `json:"client_time,omitempty"`
	Source           string     `json:"source,omitempty"`
	SourceExcerpt    string     `json:"source_excerpt,omitempty"`
	ClientRequestID  string     `json:"client_request_id"`
	CorrelationID    string     `json:"correlation_id,omitempty"`
}

type CreatePolicyInput struct {
	Name                        string        `json:"name"`
	ProjectID                   string        `json:"project_id"`
	Adapter                     string        `json:"adapter"`
	Mode                        ExecutionMode `json:"mode"`
	AllowedCapabilities         []string      `json:"allowed_capabilities"`
	MarkOccurrenceDoneOnSuccess bool          `json:"mark_occurrence_done_on_success"`
	NotifyOnStart               bool          `json:"notify_on_start"`
	NotifyOnCompletion          bool          `json:"notify_on_completion"`
	NotifyOnFailure             bool          `json:"notify_on_failure"`
	TimeoutMinutes              int           `json:"timeout_minutes"`
	ClientTime                  *time.Time    `json:"client_time,omitempty"`
	Source                      string        `json:"source,omitempty"`
	SourceExcerpt               string        `json:"source_excerpt,omitempty"`
	ClientRequestID             string        `json:"client_request_id"`
	CorrelationID               string        `json:"correlation_id,omitempty"`
}

type UpdatePolicyInput struct {
	Name                        *string        `json:"name,omitempty"`
	Adapter                     *string        `json:"adapter,omitempty"`
	Mode                        *ExecutionMode `json:"mode,omitempty"`
	AllowedCapabilities         *[]string      `json:"allowed_capabilities,omitempty"`
	MarkOccurrenceDoneOnSuccess *bool          `json:"mark_occurrence_done_on_success,omitempty"`
	NotifyOnStart               *bool          `json:"notify_on_start,omitempty"`
	NotifyOnCompletion          *bool          `json:"notify_on_completion,omitempty"`
	NotifyOnFailure             *bool          `json:"notify_on_failure,omitempty"`
	TimeoutMinutes              *int           `json:"timeout_minutes,omitempty"`
	Enabled                     *bool          `json:"enabled,omitempty"`
	ExpectedRevision            int64          `json:"expected_revision"`
	ClientTime                  *time.Time     `json:"client_time,omitempty"`
	Source                      string         `json:"source,omitempty"`
	SourceExcerpt               string         `json:"source_excerpt,omitempty"`
	ClientRequestID             string         `json:"client_request_id"`
	CorrelationID               string         `json:"correlation_id,omitempty"`
}

type RegisterRunnerInput struct {
	DisplayName     string     `json:"display_name"`
	Projects        []string   `json:"projects"`
	Adapters        []string   `json:"adapters"`
	ClientTime      *time.Time `json:"client_time,omitempty"`
	Source          string     `json:"source,omitempty"`
	SourceExcerpt   string     `json:"source_excerpt,omitempty"`
	ClientRequestID string     `json:"client_request_id"`
	CorrelationID   string     `json:"correlation_id,omitempty"`
}

type UpdateRunnerInput struct {
	DisplayName      *string    `json:"display_name,omitempty"`
	Projects         *[]string  `json:"projects,omitempty"`
	Adapters         *[]string  `json:"adapters,omitempty"`
	ExpectedRevision int64      `json:"expected_revision"`
	ClientTime       *time.Time `json:"client_time,omitempty"`
	Source           string     `json:"source,omitempty"`
	SourceExcerpt    string     `json:"source_excerpt,omitempty"`
	ClientRequestID  string     `json:"client_request_id"`
	CorrelationID    string     `json:"correlation_id,omitempty"`
}

type CreateManualRunInput struct {
	ReminderID string `json:"reminder_id"`
	PolicyID   string `json:"policy_id"`
	MutationMetadata
}

type ClaimRunInput struct {
	WaitSeconds int `json:"wait_seconds"`
}

type ReportRunEventInput struct {
	RunID            string `json:"run_id"`
	Event            string `json:"event"`
	Detail           string `json:"detail,omitempty"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type CompleteRunInput struct {
	RunID             string         `json:"run_id"`
	Outcome           AgentRunStatus `json:"outcome"`
	ResultSummary     string         `json:"result_summary,omitempty"`
	ResultArtifactRef string         `json:"result_artifact_ref,omitempty"`
	// FailureCode is optional and caller-supplied; only
	// RunFailureAdapterUnavailable is accepted from runners.
	FailureCode      string `json:"failure_code,omitempty"`
	ExitCode         int    `json:"exit_code"`
	ExpectedRevision int64  `json:"expected_revision"`
	MutationMetadata
}

type RequestApprovalInput struct {
	RunID            string `json:"run_id"`
	Capability       string `json:"capability"`
	Reason           string `json:"reason,omitempty"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type ApproveRunInput struct {
	RunID            string `json:"run_id"`
	Approved         bool   `json:"approved"`
	ExpectedRevision int64  `json:"expected_revision"`
	MutationMetadata
}

type CancelRunInput struct {
	RunID            string `json:"run_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	MutationMetadata
}

type AgentRunListFilter struct {
	ReminderID string
	Status     *AgentRunStatus
	RunnerID   string
	Limit      int
}

// DueOccurrence pairs an occurrence at or past its fire time with the
// unarchived reminder it belongs to.
type DueOccurrence struct {
	Reminder   Reminder   `json:"reminder"`
	Occurrence Occurrence `json:"occurrence"`
}
