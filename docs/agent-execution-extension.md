# Proposal: Scheduled agent execution and project state

**Status:** Implemented. The binding decisions and the change surface live in `docs/agent-execution-implementation-plan.md`; the text below is the historical record.

**Scope:** Design proposal only. No runtime behavior is introduced by this document.

## Summary

State already gives an owner, their devices, and paired agent harnesses a durable reminder record, audit history, scheduling semantics, encrypted push, and a bounded MCP briefing. This proposal extends that foundation with **owner-controlled scheduled agent execution**.

A scheduled occurrence may optionally create an `AgentRun` on a paired workstation. The workstation, not the public State server, launches a local harness adapter. The run receives a bounded task contract plus relevant State context, records an auditable outcome, updates the originating occurrence only when policy permits it, and triggers the existing phone notification path.

The related `.state/` directory is a local, repository-scoped projection for tools and humans. It is not a secret store, a second database, or an arbitrary remote-code channel.

## Goals

- Start an explicitly configured agent task at or after a scheduled State occurrence.
- Support local workstation adapters for harnesses such as Codex, Claude Code, and OpenCode without coupling the server to a single CLI.
- Preserve State as the canonical, auditable source for reminders, task intent, revisions, execution lifecycle, and final outcome.
- Make project-specific context inspectable in a conventional `.state/` directory.
- Return structured progress and completion information to State, then notify paired iOS devices through the existing encrypted push route.
- Work for self-hosted deployments without opening inbound SSH access to a workstation.

## Non-goals

- Remote shell access from State servers to owner workstations.
- Executing arbitrary shell text sent from an iPhone, a reminder description, or a compromised server.
- Synchronizing credentials, API keys, SSH material, or harness configuration into `.state/`.
- Replacing a harness's own permissions, sandbox, approval workflow, or project instructions.
- Running unattended high-impact actions by default, including deployment, production mutations, destructive commands, or sending external communications.
- Treating `.state/` as a bidirectional Git synchronization protocol in the first release.

## Why a local runner instead of server-side SSH

A server-initiated SSH or tmux model looks convenient until the boring parts arrive: workstation reachability, port exposure, credential rotation, host verification, arbitrary command injection, and unclear ownership of a terminal session. Very enterprise. Very unnecessary.

The preferred design is an outbound-only `state-runner` process on each opted-in workstation:

1. The runner authenticates to the owner's State server with a dedicated, revocable `runner` credential.
2. It polls or holds a bounded long-poll connection for runnable jobs assigned to that runner.
3. It validates the immutable job contract against local policy.
4. It starts a selected local adapter in a dedicated working directory and session.
5. It reports lifecycle events and a structured result through State APIs or MCP.

This keeps the workstation behind normal NAT and firewall rules. SSH and tmux remain optional implementation details inside a local adapter, not a network protocol exposed by State.

## Core concepts

### 1. AgentRun

`AgentRun` is a durable execution record linked to a reminder and, when scheduled, one occurrence.

Suggested fields:

```text
id, reminder_id, occurrence_id?, runner_id, project_id?, adapter,
status, idempotency_key, task_contract, context_cursor,
requested_at, claimed_at?, started_at?, finished_at?,
result_summary?, result_artifact_ref?, failure_code?,
created_by_actor, completed_by_actor?, revision
```

Suggested statuses:

```text
planned -> eligible -> claimed -> running -> succeeded | failed | cancelled | needs_approval | expired
```

Important properties:

- A unique `(occurrence_id, execution_policy_revision)` prevents duplicate launch after retries.
- Claiming is atomic and leased. A runner crash must eventually release an expired claim.
- Completion is idempotent and uses revision checking, as existing State mutations do.
- Every transition writes an immutable audit event with actor, correlation ID, and a redacted summary.
- Full adapter logs are optional artifacts with retention and access policy, not default reminder text.

### 2. Execution policy

An occurrence does not become remotely executable merely because it has a time. A reminder must reference a versioned `ExecutionPolicy` approved by the owner.

```yaml
adapter: codex
project: customer-api
mode: supervised # supervised | unattended-low-risk
allowed_capabilities:
  - read_repository
  - edit_repository
  - run_tests
completion:
  mark_occurrence_done_on: succeeded
notification:
  on_start: false
  on_completion: true
  on_failure: true
timeout_minutes: 30
```

Policies must not carry command strings. The runner maps a selected adapter and a constrained task contract to local commands it owns. Sensitive or externally visible capabilities require an explicit local approval gate, even where the run itself was scheduled.

### 3. Task contract

The task contract is structured data generated from the reminder, selected project, and policy. It contains:

- an immutable run ID and correlation ID;
- human-readable objective and acceptance criteria;
- project reference and allowed working-directory boundary;
- a bounded State context snapshot or cursor;
- allowed capabilities and timeout;
- completion protocol;
- policy revision and content hash.

The runner must reject missing, malformed, stale, expired, or policy-incompatible contracts. It must never execute an unconstrained `command` field from State.

### 4. `.state/` project projection

Each opted-in repository may contain a generated, reviewable `.state/` directory. State remains canonical. The directory is a local projection and adapter input, updated by `state-runner` or `statectl`, never a credential store.

Suggested layout:

```text
.state/
  README.md                 # generated explanation and safety boundary
  project.json              # stable project ID, server origin, schema version
  policy.yaml               # local allow-list reference, no secrets
  context/
    current.md              # bounded, redacted briefing for the selected project
  runs/
    <run-id>/contract.json  # immutable task contract
    <run-id>/status.json    # current local state, safe to regenerate
    <run-id>/result.json    # structured final result
  .gitignore                # ignores logs, transient locks, local overrides, artifacts
```

Recommended Git behavior:

- `README.md`, `project.json` and a reviewed policy template may be committed.
- `context/`, `runs/`, logs, lock files, artifacts and local overrides should be ignored by default.
- No local state is uploaded merely because a repository is pushed.
- The server synchronizes records through authenticated APIs. Git is optional human-readable configuration, not the transport or conflict-resolution mechanism.

This provides the familiar repository affordance requested for agent work while retaining State's existing revision, audit, and idempotency guarantees.

## End-to-end flow

1. An owner creates or edits a scheduled reminder in State and selects a project, runner, adapter, and execution policy.
2. State materializes the next occurrence as it does today. At eligibility time, the scheduler creates one `AgentRun` if the policy is enabled.
3. A matching outbound `state-runner` claims the run through an authenticated, leased API.
4. The runner refreshes `.state/context/current.md`, writes the immutable contract under `.state/runs/<run-id>/`, validates local policy, and starts the adapter.
5. The adapter receives the task objective and calls the existing State MCP tools for the full reminder, comments, audit history, and incremental changes when needed.
6. The runner sends started, progress, and terminal status events. State records them in the audit timeline and updates iOS through the current encrypted push design.
7. On successful verified completion, State optionally completes the originating occurrence. On failure, timeout, or approval requirement, the occurrence stays actionable and contains the result summary.

## MCP additions

The existing MCP server is a strong integration point because it already exposes bounded briefings, full reminder detail, comments, occurrences, revisions, and auditable writes.

Suggested tools:

- `get_execution_context`: returns the selected reminder, occurrence, policy, and a bounded change window for a specific run.
- `claim_agent_run`: atomically claims an eligible run for an authenticated runner and returns its contract.
- `report_agent_run_event`: appends redacted lifecycle events, progress, and structured result references.
- `complete_agent_run`: finalizes one run with optimistic revision checking.
- `request_agent_approval`: moves a run to `needs_approval` with a precise requested capability.

A separate runner credential type is preferable to giving the process an unrestricted harness credential. Existing `get_reminder`, `get_changes`, `add_comment`, and `complete_occurrence` remain useful to the launched harness, but runner lifecycle authorization should be narrower and independently revocable.

`statectl` already dynamically proxies the remote MCP tool catalog. New server tools therefore need no protocol-specific forwarding code in the STDIO proxy. Local `statectl` work is only needed for runner installation, project initialization, policy validation, and `.state/` projection.

## Repository integration map

The following paths are the expected implementation surface after this proposal is accepted:

| Area | Existing path | Proposed change |
| --- | --- | --- |
| Domain models | `internal/state/models.go` | Add `Runner`, `ExecutionPolicy`, `AgentRun`, lifecycle statuses, result types, and audit actions. |
| Domain service | `internal/state/service.go` | Add policy validation, idempotent scheduling, leasing, state transitions, approval behavior, and completion rules. |
| Persistence | `internal/store/pocketbase_repository.go` | Add PocketBase collections, indexes, atomic claim/update operations, and signed audit writes. |
| Scheduling | `cmd/state-server/main.go` | Extend the existing push scheduler or introduce a dedicated execution scheduler that materializes eligible runs. |
| MCP | `internal/mcpserver/server.go` | Register runner-scoped lifecycle and context tools with narrow authorization. |
| REST contract | `openapi/state-v1.yaml` | Add runner registration, run claiming, events, approval, and query endpoints. |
| Local CLI | `cmd/statectl/main.go`, `internal/statectl/` | Add project initialization, runner installation, policy validation, and `.state/` projection commands. |
| New local process | `cmd/state-runner/` | New outbound worker, adapter registry, local lease loop, isolated process launch, and result collection. |
| iOS domain and sync | `ios/State/Sources/Models/Models.swift`, `ios/State/Sources/Networking/APIClient.swift`, `ios/State/Sources/Sync/SyncEngine.swift` | Sync policies and runs, then expose execution state offline. |
| iOS UI | `ios/State/Sources/UI/ReminderEditorView.swift`, `ios/State/Sources/UI/ReminderDetailView.swift`, `ios/State/Sources/UI/ActivityView.swift`, `ios/State/Sources/UI/SettingsView.swift` | Configure execution, show run lifecycle and approval prompts, and manage runners. |
| Encrypted notification path | `internal/relay/handler.go`, `internal/push/`, `ios/StateNotificationService/Sources/NotificationService.swift` | Add an allow-listed `run_finished` payload type, encryption schema, collapse behavior, and iOS rendering. Existing relay types only cover reminder and sync delivery. |
| Security | `docs/threat-model.md`, `SECURITY.md` | Add workstation compromise, malicious task contract, lease takeover, policy bypass, log retention, and adapter supply-chain threats. |

## Adapter boundary

Adapters should use an internal interface similar to:

```go
type Adapter interface {
    Name() string
    Validate(Contract, LocalPolicy) error
    Start(context.Context, StartRequest) (Session, error)
}

type Session interface {
    Wait(context.Context) (Result, error)
    Cancel(context.Context) error
}
```

The first adapters should be deliberately small:

- `codex`: local non-interactive or supervised run using an explicit adapter-owned invocation.
- `claude-code`: same contract, isolated adapter invocation.
- `opencode`: same contract, isolated adapter invocation.

Adapter-specific CLI flags, temporary prompt files, tmux sessions, and terminal capture stay local. The common State protocol only sees the typed task and redacted lifecycle result.

## Security requirements

- The server must not store or transmit shell commands, private SSH keys, local tokens, or harness configuration files.
- Runner registration uses one-time pairing, separate credential rotation and revocation, and an owner-visible device identity.
- Every run is pinned to a runner, project boundary, adapter, policy version, and expiration.
- The runner verifies server origin, contract signature or hash, policy compatibility, and working-directory containment before launch.
- A policy allow-list denies network deployment, privileged execution, external messages, payment operations, and destructive actions unless explicitly approved locally.
- Logs are redacted, size-limited, encrypted at rest when retained, and separated from push payloads.
- Push messages use generic lifecycle text unless the device can decrypt a richer State payload.
- A failed run never auto-completes an occurrence. A successful run only completes it when the policy's completion rule and terminal evidence agree.

## Delivery plan

### Phase 0: Design and threat model

- Confirm runner trust model, policy schema, log retention, approval semantics, and offline behavior.
- Add tests that prove no arbitrary remote command can reach a runner.

### Phase 1: Read-only project projection

- Add `statectl project init` and `.state/` generation.
- Generate a project reference and bounded context file only.
- No scheduled execution yet.

### Phase 2: Runner and manual runs

- Introduce runner pairing, local policy validation, adapter registry, and owner-triggered manual runs.
- Implement lifecycle auditing and iOS run visibility.
- Keep all runs supervised.

### Phase 3: Scheduled low-risk runs

- Add `AgentRun` materialization from occurrences, leases, retries, timeout behavior, and notification flow.
- Permit only explicitly approved low-risk policies.

### Phase 4: Approval and recovery

- Add capability-level approvals, crash recovery, local session discovery, artifact retention controls, and operational dashboards.

## Open decisions

1. Should the runner use pull-only polling first, or a long-lived outbound stream with a polling fallback?
2. Which execution capabilities qualify as unattended low risk?
3. Does each project need one runner assignment, or can policies choose from a runner pool?
4. Are run artifacts retained only locally, uploaded encrypted to the State server, or both?
5. How should a harness prove task success beyond reporting its own result?
6. Which UI action grants an approval: per run, per policy version, or time-bounded capability grant?
7. Should `.state/` ever be committed as project metadata, or always generated and ignored by default?

## Acceptance criteria for a first implementation

- A paired runner can claim exactly one eligible run and cannot claim another runner's work.
- Repeating a scheduler cycle or runner retry never launches the same occurrence twice.
- A runner rejects a task outside its configured project root or capability allow-list.
- A launched adapter receives a stable State run ID and can retrieve relevant State context via MCP.
- Start, success, failure, timeout, cancellation, and approval request appear in immutable audit history.
- iOS displays run state and receives an encrypted completion or failure notification.
- Revoking a runner prevents new claims without invalidating historical audit records.
- No secret, executable shell command, or unredacted log is written into `.state/` or emitted in a push payload.

## Addendum: as implemented

The proposal shipped as specified here, with the open decisions resolved as follows:

1. Pull-only polling with a bounded long-poll (`wait_seconds`, server-capped at 25 s); no outbound stream in v1.
2. `unattended-low-risk` policies may carry only `read_repository`, `read_state_context`, `run_tests` and `edit_repository`; the server rejects anything riskier in that mode.
3. Runner pool: a run is claimable by any runner whose registration covers the run's project and the policy's adapter.
4. Artifacts stay local; only a redacted, bounded `result_summary` and an opaque `result_artifact_ref` reach the server.
5. Success evidence is the adapter exit code plus the structured self-report; a disagreement forces `failure_code=evidence_mismatch`.
6. Approvals are granted per run, for one named capability at a time.
7. `.state/` is generated; `context/`, `runs/`, logs and locks are gitignored by default, and Git is never the transport.

Implementation notes, the migration approach for the audit and actor tables, and the file-level change surface are recorded in `docs/agent-execution-implementation-plan.md`. The threat model in `docs/threat-model.md` covers the new trust boundaries.
