# Implementation plan: scheduled agent execution and project state

**Status:** Accepted for implementation
**Source concept:** `docs/agent-execution-extension.md` (PR #20)
**Scope:** Full implementation of the concept across Go server, persistence, MCP, REST, statectl, the new `state-runner` process, push/relay, iOS, and security documentation. This document records every design decision the concept left open and lists the concrete change surface file by file.

## 1. Decisions on the concept's open questions

The concept ended with seven open decisions. They are resolved as follows, and the reasons are part of the record.

1. **Pull-only polling first.** The runner polls `POST /api/v1/runner/claims` on a fixed interval with a bounded long-poll (`wait_seconds`, server-capped at 25 s). No server-sent streams in v1: SQLite serialization already bounds concurrency, polling is trivially debuggable, and long-poll gives near-instant pickup without a second protocol.
2. **Unattended low risk is an explicit capability set.** The policy mode `unattended-low-risk` may carry only capabilities from `read_repository`, `read_state_context`, `run_tests`, `edit_repository`. Anything else (network deploys, external messages, package publishes, privileged execution, destructive operations) is rejected by server-side validation in unattended mode and requires `supervised` mode plus a local approval gate (`needs_approval`).
3. **Runner pool per policy, constrained at claim time.** A run is claimable by any runner whose registration lists the run's project and the policy's adapter. No static one-runner-per-project assignment; the owner edits each runner's `projects`/`adapters` lists via REST/iOS.
4. **Artifacts stay local by default.** `.state/runs/<id>/` holds contract, status, and result; logs are bounded (64 KiB tail), redacted, and never leave the workstation. The server stores only `result_summary` (≤ 2 000 chars, redacted) and an optional `result_artifact_ref` (opaque local path label, not uploaded content). Uploading encrypted artifacts is out of scope for v1.
5. **Success evidence = adapter exit status + structured self-report.** The harness reports success/failure through `complete_agent_run`; the runner independently reports the process exit code. A disagreement (exit 0 but reported failure, or vice versa) marks the run `failed` with `failure_code=evidence_mismatch`. Deeper proof (test result parsing) is a later enhancement and stays out of the trust boundary.
6. **Approval is granted per run.** `request_agent_approval` names one capability; the owner approves or declines that single run in iOS/REST. Approving resumes the run exactly once; a second request requires a second approval. No standing capability grants in v1.
7. **`.state/` is generated and mostly ignored.** `README.md`, `project.json`, and `policy.yaml` may be committed at the owner's discretion; `context/`, `runs/`, logs, and locks are gitignored by the generated `.state/.gitignore`. Git is never a transport.

Two codebase constraints found during mapping shaped the design:

- **SQLite CHECK constraints cannot be widened in place.** `state_actors.kind` and `state_pairing_codes.actor_kind` exclude `runner`. Both tables get a guarded rebuild migration (detect old DDL via `sqlite_master`, copy rows into a rebuilt table, swap names, recreate indexes) inside the existing idempotent `ensureSchema` pattern in `internal/auth/manager.go`.
- **`state_audit_events.reminder_id` is `NOT NULL`.** Policy and runner registration events have no reminder. The audit table gets the same guarded rebuild treatment in `internal/store/pocketbase_repository.go`, keeping the immutability triggers, the hash chain, and all indexes. `AgentRun` lifecycle events always carry a `reminder_id` (a run is always created from a reminder), so only policy/runner events use the new NULL case. `ListChanges` is global and sequence-based, so NULLs flow without protocol change; iOS pull groups by `reminder_id` and must skip events without one (policy/runner data has its own fetch path).

## 2. Data model

All new domain types live in `internal/state/models.go` beside the existing blocks; all times UTC; all IDs UUIDv7; every mutable entity carries `Revision` and participates in the existing audit chain.

### 2.1 Actor kind

- New const `ActorKindRunner ActorKind = "runner"` next to `owner|device|harness|system` (`models.go`).
- `state_actors.kind` and `state_pairing_codes.actor_kind` CHECK widened via rebuild migration (`internal/auth/manager.go` `ensureSchema`).
- Authorization matrix:
  - Existing 9 MCP tools and existing REST routes: unchanged kinds (owner/device/harness). `runner` is rejected from reminder mutations (service-level kind gate, same pattern as the archive rule).
  - New runner lifecycle surface (MCP tools + REST `/api/v1/runner/*`): `runner` and `owner` kinds. Owner access exists for debugging and for the iOS UI.
  - Policy and runner management REST: `owner` (and `device` read-only for iOS sync on non-owner devices: `GET` only).

### 2.2 Project

```text
Project{ID, Name, Description, RootPathHint, Revision, CreatedAt, UpdatedAt}
```

- `Name` unique, validated like a harness slug extended: `^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$` (new `ValidProjectSlug` in `internal/state/harness.go`-adjacent file `internal/state/project.go` with tests).
- `RootPathHint` is informational text for the runner (e.g. `~/src/customer-api`); the runner must still enforce its own configured root.
- Audit actions: `project.created`, `project.updated`, `project.archived`? No — projects are not archived in v1; deletion is out of scope. Only `project.created`, `project.updated`. `reminder_id` NULL.

### 2.3 ExecutionPolicy

```text
ExecutionPolicy{
  ID, Name,                         # unique slug, same validation as project
  ProjectID,                        # REFERENCES state_projects
  Adapter,                          # harness slug (state.ValidHarness)
  Mode,                             # supervised | unattended-low-risk
  AllowedCapabilities []string,     # validated against the known capability set
  MarkOccurrenceDoneOnSuccess bool, # completion rule (concept: completion.mark_occurrence_done_on)
  NotifyOnStart, NotifyOnCompletion, NotifyOnFailure bool,
  TimeoutMinutes int,               # 1..240
  Enabled bool,
  Revision, CreatedAt, UpdatedAt,
}
```

- Capability vocabulary (closed enum, validated server-side): `read_repository`, `edit_repository`, `run_tests`, `read_state_context`, `write_state`, `network_access`, `deploy`, `message_external`, `destructive`. The last four are never allowed in `unattended-low-risk` mode (server rejects the combination).
- Policies are mutable with revision checks; every edit bumps `Revision`. A run pins `policy_revision` at creation, so re-editing a policy never changes a materialized run.
- Audit actions: `policy.created`, `policy.updated`, `policy.enabled`, `policy.disabled`. `reminder_id` NULL.

### 2.4 Runner

```text
Runner{
  ID,               # == the actor ID of kind runner (1:1)
  DisplayName,
  Projects  []string,  # project IDs this runner may serve; empty = none
  Adapters  []string,  # harness slugs this runner can launch; empty = none
  RegisteredAt, LastSeenAt, Revision,
}
```

- Created by the runner itself on first contact (`POST /api/v1/runner/registration`), owner-editable afterwards (`PATCH /api/v1/runners/{id}` for projects/adapters/display name).
- Revocation = existing `RevokeActor` on the runner actor; revoked runners fail `Authenticate` and cannot claim. Historical audit rows are untouched.
- Audit actions: `runner.registered`, `runner.updated`. (`runner.revoked` reuses the existing actor-revocation event written by `internal/auth`.) `reminder_id` NULL.

### 2.5 AgentRun

```text
AgentRun{
  ID, ReminderID, OccurrenceID *string,  # nil occurrence = manual run
  PolicyID, PolicyRevision,
  ProjectID, Adapter,
  RunnerID *string,                      # set at claim time
  Status,                                # enum below
  IdempotencyKey,                        # client_request_id for creation
  TaskContract TaskContract,             # embedded struct, JSON
  ContextCursor int64,                   # audit sequence at materialization
  LeaseExpiresAt *time.Time,
  RequestedAt, ClaimedAt, StartedAt, FinishedAt *time.Time,
  ResultSummary string,                  # redacted, ≤ 2000 chars
  ResultArtifactRef string,              # optional opaque local label
  FailureCode string,                    # enum-ish string, empty when none
  ApprovalCapability string,             # set while needs_approval
  CreatedByActor Actor, CompletedByActor *Actor,
  Revision, CreatedAt, UpdatedAt,
}
```

Statuses:

```text
planned -> eligible -> claimed -> running -> succeeded | failed | cancelled | needs_approval | expired
```

- `planned`: created for a future occurrence, not yet due. `eligible`: due, claimable. Transitions `planned -> eligible` (scheduler), `eligible -> claimed` (atomic lease), `claimed -> running` (first runner event), `running -> needs_approval -> claimed` (approval granted; the runner re-validates and continues), `running|claimed -> succeeded|failed|cancelled`, `planned|eligible -> expired` (occurrence replaced or policy disabled before pickup), `claimed -> eligible` (lease expiry sweep requeues).
- Uniqueness: `UNIQUE(occurrence_id, policy_revision)` on `state_runs` (with `occurrence_id` NULL for manual runs — SQLite treats NULLs as distinct, manual runs instead dedupe on `idempotency_key` via the existing `state_idempotency` table).
- `TaskContract` (embedded, serialized into `data_json`, also written to `.state/runs/<id>/contract.json` by the runner):

```text
TaskContract{RunID, CorrelationID, Objective, AcceptanceCriteria []string,
  ProjectID, ProjectName, PolicyID, PolicyRevision, ContractHash,
  AllowedCapabilities []string, TimeoutMinutes, CompletionRule}
```

- `ContractHash` = SHA-256 over the canonical JSON of the contract fields; the runner re-computes and refuses mismatches.
- Audit actions: `run.planned`, `run.eligible`, `run.claimed`, `run.started`, `run.progress`, `run.approval_requested`, `run.approved`, `run.declined`, `run.succeeded`, `run.failed`, `run.cancelled`, `run.expired`, `run.requeued`. All carry the run's `reminder_id`.
- `SourceExcerpt` on run events carries at most the redacted one-line status (never log content), satisfying the relay/FTS redaction rules.

### 2.6 Service input structs

Following the existing `CreateReminderInput` pattern: `CreateProjectInput`, `UpdateProjectInput`, `CreatePolicyInput`, `UpdatePolicyInput`, `RegisterRunnerInput`, `UpdateRunnerInput`, `CreateManualRunInput{ReminderID, PolicyID, MutationMetadata}`, `ClaimRunInput{WaitSeconds}`, `ReportRunEventInput{RunID, Event, Detail, ExpectedRevision}`, `CompleteRunInput{RunID, Outcome, ResultSummary, ResultArtifactRef, ExitCode, ExpectedRevision, MutationMetadata}`, `RequestApprovalInput{RunID, Capability, Reason, ExpectedRevision}`, `ApproveRunInput{RunID, Approved, ExpectedRevision, MutationMetadata}`, `CancelRunInput{...}`.

## 3. Persistence (`internal/store/pocketbase_repository.go`)

New DDL appended to the `statements` slice in `ensureSchema`, all `STRICT`, following the `data_json` + denormalized-columns pattern:

- `state_projects(id PK, name UNIQUE, revision CHECK>0, created_at, updated_at, data_json)`.
- `state_policies(id PK, name UNIQUE, project_id REFERENCES state_projects, adapter, mode CHECK, enabled 0|1, revision CHECK>0, created_at, updated_at, data_json)` + index `(project_id)`.
- `state_runners(id PK REFERENCES state_actors, display_name, last_seen_at, revision CHECK>0, registered_at, data_json)` — projects/adapters live in `data_json`.
- `state_runs(id PK, reminder_id REFERENCES state_reminders, occurrence_id REFERENCES state_occurrences, policy_id REFERENCES state_policies, policy_revision, project_id, adapter, runner_id, status CHECK IN (...), idempotency_key, lease_expires_at, requested_at, claimed_at, started_at, finished_at, revision CHECK>0, created_at, updated_at, data_json, UNIQUE(occurrence_id, policy_revision))` + indexes `(status, lease_expires_at)`, `(reminder_id)`, `(runner_id, status)`.
- Audit table rebuild migration: `migrateAuditReminderNullable()` — checks `sqlite_master` DDL for `reminder_id TEXT NOT NULL`; when found, inside one transaction: drop immutability triggers, `ALTER TABLE state_audit_events RENAME TO state_audit_events_old`, create new table with `reminder_id TEXT NULL REFERENCES state_reminders(id)`, copy ordered by sequence, drop old, recreate triggers and indexes. Skipped entirely on fresh databases (they get the new DDL directly). Same guarded-rebuild helper shape in `internal/auth/manager.go` for `state_actors` / `state_pairing_codes` CHECK widening (`migrateActorKindRunner()`), preserving `state_single_owner_idx` and all foreign keys.

New repository methods (each with idempotency lookup first, `RunInTransaction`, `RowsAffected()==1` CAS, `sealAuditEvent` + `insertAuditEvent`, search rows where useful):

- `CreateProject`, `UpdateProject`, `GetProject`, `ListProjects`
- `CreatePolicy`, `UpdatePolicy`, `GetPolicy`, `ListPolicies`
- `UpsertRunner`, `UpdateRunner`, `GetRunner`, `ListRunners`, `TouchRunnerSeen`
- `CreateAgentRun` (used by scheduler materialization and manual creation; `INSERT OR IGNORE` on the occurrence/policy-revision unique key answers "already materialized")
- `ClaimAgentRun(runID? none — claim query)` — `ListClaimableRuns(runner, now)` (status `eligible`, adapter ∈ runner.Adapters, project ∈ runner.Projects, ordered by `requested_at`, LIMIT 1) then `UPDATE ... SET status='claimed', runner_id, lease_expires_at=now+2*timeout, revision=revision+1 WHERE id AND status='eligible' AND revision=...` with `RowsAffected()==1`.
- `GetAgentRun`, `ListAgentRuns(filter{ReminderID, Status, RunnerID}, limit)`, `UpdateAgentRunTransition` (generic CAS transition used by report/complete/approve/cancel/requeue)
- `RequeueExpiredLeases(now)` — `claimed` with `lease_expires_at < now` → `eligible`, audit `run.requeued`.
- `ExpireStaleRuns(now)` — `planned`/`eligible` runs whose occurrence vanished or whose policy was disabled → `expired`. (Called by the scheduler; the reconcile path in `reconcileOccurrences` does **not** cascade into runs directly — see §9.)

The `Repository` interface in `internal/state/service.go` grows accordingly; `internal/state/memory_repository.go` implements the same methods with the same semantics (idempotency maps, hash-chained audit, CAS) so all service tests run against both backends exactly as today.

New `result_type` values in `state_idempotency`: `project`, `policy`, `runner`, `agent_run`.

FTS: `insertAuditSearch` gains run/policy text (action, adapter, project name, redacted summary). No run-log content.

## 4. Service layer (`internal/state/service.go` + new `internal/state/execution.go`)

`execution.go` holds the new methods so `service.go` stays reviewable. Patterns copied from `updateOccurrence` (validate → load → check revision → build after-snapshot → audit event with sorted `ChangedFields` → one repository call).

- **Policy CRUD** — validation: slug name, adapter `ValidHarness`, mode enum, capability vocabulary, unattended-mode capability restriction, timeout bounds, project exists. Kind gate: owner only for writes (devices read).
- **Runner registration/update** — `RegisterRunner(actor, RegisterRunnerInput)`: requires `actor.Kind == runner`; upserts profile keyed by actor ID; validates project IDs and adapter slugs. `UpdateRunner`: owner only.
- **MaterializeEligibleRuns(now)** — scheduler entry: loads due window occurrences (reuse the push due-window semantics: `ScheduledAt - PrewarningMinutes <= now`, status pending/snoozed, unarchived reminder), joins reminders that carry `execution_policy_id`, policy enabled → `CreateAgentRun` with status `eligible` and a system-actor audit event (`ActorKindSystem`, finally used). Idempotent by the unique key.
- **PlanRunsForOccurrence** is folded into materialization (concept's `planned` state is used only for occurrences materialized ahead of due time — v1 materializes at due time directly into `eligible`; `planned` remains in the enum for forward compatibility and the `expired` sweep).
- **ClaimAgentRun(runner, ClaimWaitSeconds)** — loads runner profile, loops claimable-run query until one claims or the long-poll deadline passes; claim sets lease `now + 2*policy.TimeoutMinutes` (bounded 2..480 min) and returns the contract. Runner kind enforced.
- **ReportAgentRunEvent(runner, input)** — runner must own the claim (`run.RunnerID == actor.ID`), run must be `claimed|running`; event `started` transitions to `running`, `progress` appends audit only. Heartbeat = `ReportRunEvent{Event: "heartbeat"}` which also extends the lease (CAS update, no audit row, keeps the chain lean).
- **CompleteAgentRun(runner|harness?, input)** — v1: the *runner* completes (it owns the process evidence). `outcome ∈ {succeeded, failed}`; on `succeeded` with `exit_code == 0` and policy `MarkOccurrenceDoneOnSuccess`, the service completes the originating occurrence inside the same transaction (reusing `updateOccurrence` logic with the runner actor and correlation ID of the run). Evidence mismatch (`succeeded` + nonzero exit, `failed` + zero exit) → forced `failed` with `failure_code=evidence_mismatch`. Sets `FinishedAt`, clears lease.
- **RequestAgentApproval / ApproveAgentRun** — request: runner-owned run in `running`, capability must be outside the policy's allowed set (else `ErrInvalidInput`), status → `needs_approval`, audit + push. Approve (owner only, `expected_revision`): approved → back to `claimed` with fresh lease + audit `run.approved`; declined → `cancelled` with `failure_code=approval_declined`.
- **CancelAgentRun(owner, input)** — any non-terminal status → `cancelled`; a claimed/running cancellation is observed by the runner on its next poll/report (response carries `cancelled: true`) and it kills the process.
- **Expiry sweep** — `ExpireStaleRuns(now)`: planned/eligible runs past `requested_at + 24h`, or whose occurrence was deleted by a reschedule, or whose policy got disabled → `expired`.
- **Run completion notification** — after terminal transitions the service invokes an injected `RunNotifier` func (wired to `push.Service.NotifyRunFinished`); best-effort, 2 s timeout, error swallowed exactly like `notifySync`.

Redaction helper `redactSummary(string) string`: trims to 2 000 chars, strips lines matching secret-ish patterns (`(key|token|secret|password)\s*[:=]\s*\S+`, case-insensitive — same spirit as the relay caps, enforced before persistence and before audit/search content).

## 5. MCP server (`internal/mcpserver/server.go`)

Five new tools, registered in `registerTools()` with the shared annotation vars (`claim/complete/report/approve/cancel` = `mutating`, `get_execution_context` = `readOnly`):

| Tool | Kinds | Maps to |
| --- | --- | --- |
| `get_execution_context` | runner, owner | run + reminder detail + policy + changes since `context_cursor` |
| `claim_agent_run` | runner | `ClaimAgentRun` (long-poll) |
| `report_agent_run_event` | runner | `ReportAgentRunEvent` |
| `complete_agent_run` | runner | `CompleteAgentRun` |
| `request_agent_approval` | runner | `RequestAgentApproval` |

- New kind-gate helper `requireRunnerOrOwner(actor)` next to `server.actor()`; existing 9 tools additionally reject `runner` kind (defense in depth; the service rejects too).
- `TestMCPServerNegotiatesListsToolsAndCreatesAuditedReminder` tool-list assertion extended to the 14-tool list; new tests pair a runner through the real pairing flow (kind `runner`) and exercise claim/report/complete including a revoked-runner 401 case.

## 6. REST (`internal/api/handler.go` + `openapi/state-v1.yaml`)

New routes (all bearer-authenticated; kind rules per §2.1):

```text
POST   /api/v1/projects                       owner
GET    /api/v1/projects                       owner, device
GET    /api/v1/projects/{id}                  owner, device
PATCH  /api/v1/projects/{id}                  owner
POST   /api/v1/policies                       owner
GET    /api/v1/policies                       owner, device
GET    /api/v1/policies/{id}                  owner, device
PATCH  /api/v1/policies/{id}                  owner
POST   /api/v1/runner/registration            runner (self)
POST   /api/v1/runner/claims                  runner (long-poll, ?wait_seconds<=25)
POST   /api/v1/runner/runs/{id}/events        runner
POST   /api/v1/runner/runs/{id}/complete      runner
POST   /api/v1/runner/runs/{id}/approval      runner
GET    /api/v1/runners                        owner, device
PATCH  /api/v1/runners/{id}                   owner
GET    /api/v1/runs                           owner, device (?reminder_id, ?status, ?runner_id, ?limit)
GET    /api/v1/runs/{id}                      owner, device
POST   /api/v1/runs                           owner (manual run)
POST   /api/v1/runs/{id}/cancel               owner
POST   /api/v1/runs/{id}/approval             owner (approve/decline)
```

- Reminder write paths gain an optional `execution_policy_id` field (`CreateReminderInput`/`UpdateReminderInput`, PATCH `null` clears — the iOS DTO gets a custom encoder exactly like `schedule`/`recurrence`). Reminder responses embed the policy ID.
- `ReminderDetail` gains `runs []AgentRun` (latest 20 per reminder) so the existing iOS change pull caches runs with zero new sync protocol.
- Every new schema, route, and error code is mirrored into `openapi/state-v1.yaml` in the same pass (the repo has no drift check; discipline plus a YAML parse test).
- `mapError` gains the new sentinels (`ErrNotClaimable` → 409 `not_claimable`, `ErrPolicyViolation` → 403 `policy_violation`, `ErrRunStateConflict` → 409 `run_state_conflict`).

## 7. Scheduler (`cmd/state-server/main.go`)

- New goroutine `runExecutionScheduler(ctx, service, logger)` next to `runPushScheduler` (spawn in `runServe`, line ~93): immediate run, then every 30 s: `MaterializeEligibleRuns(now)` → `RequeueExpiredLeases(now)` → `ExpireStaleRuns(now)`; failures logged, `context.Canceled` tolerated — identical discipline to the push scheduler.
- `newApplication` wires the new service dependencies (push service already available for `RunNotifier`).

## 8. Push pipeline (`internal/push`, `internal/relay`)

- `internal/push/service.go`: `NotifyRunFinished(ctx, run, reminderTitle)` — payload `{"kind":"run_finished","run_id","reminder_id","occurrence_id"?,"status","title"}`; title is the reminder title (owner's own server content, same trust level as reminder pushes); collapse ID = `run.ID`; fans to all device routes (owner + devices), reusing `ListUnconfirmedRoutes`-independent route listing for devices (`ListRoutes` — small addition: runs are not occurrence-confirmed, so a plain `ListDeviceRoutes()`).
- `internal/push/sender.go:32` allow-list gains `run_finished`.
- `internal/relay/handler.go`: `validNotification` gains `run_finished`; `notificationPayload` treats it as alert (mutable-content, `interruption-level: time-sensitive`, category `STATE_RUN`, relevance-score 1) with generic alert text and new fallback strings: de „Agent-Lauf abgeschlossen" / „Agent-Lauf fehlgeschlagen", en „Agent run finished" / „Agent run failed" (status-dependent).
- End-to-end relay test extended: new kind passes, unknown kind still rejected, plaintext never in the outer APS payload.

## 9. Occurrence reconciliation interplay

`reconcileOccurrences` deletes pending occurrences on every reminder edit, which would strand runs keyed to those occurrence IDs. The expiry sweep covers this (occurrence gone → `expired`), and the materializer only keys on occurrences that exist at due time. Runs in `claimed`/`running` survive a reschedule untouched (their occurrence row is `pending` only until due — a reschedule mid-run expires nothing but leaves the run's occurrence reference dangling if deleted; the run itself stays valid and completes normally, its occurrence-completion step becomes a no-op when the occurrence row is gone). This behavior is covered by a store test.

## 10. statectl (`cmd/statectl`, `internal/statectl`)

- `statectl project init --name <slug> [--root <path>]`: creates or reuses the server project (`POST /api/v1/projects` via the paired harness/owner credential — harness tokens may read projects but not create; creation requires an owner/device token, so `project init` accepts `--owner-token` or uses the device profile; documented), then writes:
  - `.state/README.md` (generated explanation + safety boundary),
  - `.state/project.json` (`{"schema_version":1,"project_id","project_name","server"}`),
  - `.state/policy.yaml` (commented template matching §2.3 fields),
  - `.state/.gitignore` (`context/`, `runs/`, `*.log`, `*.lock`, `local.yaml`),
  all writes atomic (temp+chmod+rename, existing helper discipline) and never overwriting an existing `project.json` with a different project ID (hard error instead).
- `statectl project sync`: refreshes `.state/context/current.md` from `get_briefing` (bounded, redacted server-side as today) — also callable by the runner before each launch.
- `statectl project status`: prints project identity, last sync, policy template validity (`statectl project validate` checks `policy.yaml` against the server schema without contacting the server — pure local validation reusing `internal/state` validation).
- No changes needed in `proxy.go` (dynamic catalog picks the new tools up). `rules.go` `DefaultAgentRules()` gains a short section: how a launched harness should read its run context (`get_execution_context` is runner-only — harnesses instead use the reminder ID from their task prompt with the existing `get_reminder`/`get_changes`) and that completing the occurrence happens automatically on success when the policy says so — agents must not double-complete.

## 11. state-runner (new `cmd/state-runner` + `internal/runner`)

Separate small binary; does not depend on PocketBase.

- `internal/runner/config.go`: `RunnerConfig{ServerURL, ProfileDir, Projects []string, Adapters []string, WorkRoot, PollIntervalSeconds (default 5), LongPollSeconds (default 25)}` at `~/.config/state/runner.json` (atomic 0600 writes, HTTPS-except-loopback validation reused from statectl's config code).
- `state-runner pair --server URL --code XXXXX-XXXXX --name <display> --projects a,b --adapters codex,claude-code`: exchanges the pairing code (kind `runner`), stores the token in the OS keychain (`statectl.SecretStore`, service account distinct from harness profiles), writes the config, then self-registers (`POST /api/v1/runner/registration`).
- `state-runner run [--once]`: loop = long-poll claim → validate contract (hash, policy revision pin, adapter installed locally, project root under `WorkRoot` — `filepath.EvalSymlinks` + prefix check, refuse escapes) → write `.state/runs/<id>/{contract.json,status.json}` in the project checkout → spawn adapter → stream bounded output to `status.json` (tail 64 KiB) → on exit, `complete_agent_run` with outcome/exit code/summary → continue polling. Lease heartbeat every 30 s while a run executes. Cancellation observed from claim/report/complete responses (run status `cancelled`) kills the process group.
- `internal/runner/adapters.go`: the concept's `Adapter` interface (`Name`, `Validate`, `Start`, `Session.Wait/Cancel`). First adapters, deliberately thin:
  - `codex`: `codex exec --skip-git-repo-check "<prompt>"` in the project dir (prompt = objective + acceptance criteria + context pointer file path; flags adapter-owned).
  - `claude-code`: `claude -p "<prompt>" --output-format json` (best effort; falls back to text).
  - `opencode`: `opencode run "<prompt>"`.
  Each adapter validates that its binary exists (`exec.LookPath`) and that the contract's capabilities are a subset of what it can honor; unknown/missing binary → run `failed` with `failure_code=adapter_unavailable` (reported back, not silently dropped).
- Runner auth uses REST only (no MCP client dependency in the runner), keeping the binary lean; the launched harness talks MCP through the normal statectl proxy if installed.
- Tests (`internal/runner/*_test.go`): fake adapter registry with a `script` adapter for tests (`/bin/sh -c` echo) exercising claim→run→complete against a real booted server (same `httptest` + PocketBase fixture style as `mcpserver` tests), contract-hash tamper rejection, workdir escape rejection, lease requeue after runner "crash", cancellation mid-run, evidence mismatch.

## 12. iOS

- **Models** (`Models.swift`): `ActorKind` gains `runner`; new structs `Project`, `ExecutionPolicy` (+ `ExecutionMode`, capability strings), `AgentRun` (+ `AgentRunStatus`), `Runner`; list-response wrappers; `Reminder` gains `executionPolicyID`; `ReminderDetail` gains `runs`. Property names follow the `policyID` acronym convention.
- **APIClient**: `protocol StateAPI` unchanged for sync-critical methods (runs ride inside `ReminderDetail`); new UI-level methods: `listProjects`, `listPolicies`, `createPolicy`, `updatePolicy`, `listRunners`, `updateRunner`, `listRuns(reminderID:)`, `createManualRun`, `approveRun`, `cancelRun`, plus pairing-code support for kind `runner`.
- **StateDatabase**: migration `"v2"` (append-only): `project_cache`, `policy_cache`, `runner_cache` tables (json blob + name/status columns), `run_cache` (json blob + reminder_id + status + updated_at, FK `reminder_id` → `reminder_cache` cascade); `apply(detail:)` extended to replace that reminder's run rows; new queries `runs(for:)`, `policies()`, `runners()`, `projects()`; `apply(projects/policies/runners)` for the global lists.
- **SyncEngine**: pull loop skips change events whose `reminderID` is null (new optional decoding) — these are policy/runner/project events; `AppModel.synchronize` additionally fetches and applies the global lists (best-effort, after the change pull). `changedFields` metadata set unchanged (new DTOs reuse the same metadata keys).
- **AppModel**: new `private(set)` state `projects`, `policies`, `runners`; mutations for policy create/update (optimistic + enqueue, same pattern), manual run trigger, approve/cancel; demo mode seeds one project, one policy, one succeeded run + one `needs_approval` run so UI tests and screenshots keep working.
- **UI**:
  - `ReminderEditorView`: new "Agent execution" section — policy picker (None + policies + "New policy…" sheet), shown only when policies exist or owner creates one; draft field `executionPolicyID` with explicit-null encoding.
  - `ReminderDetailView`: "Agent runs" section listing run rows (status icon, adapter, times, summary), navigation to a run detail (timeline from audit events filtered to the run's correlation ID, approve/decline buttons while `needs_approval`, cancel while active, "Run now" for owner when a policy is attached); `AuditEventRow.actionLabel` gains all `run.*`, `policy.*`, `project.*`, `runner.*` cases.
  - `SettingsView`: owner-only "Runners" section (create pairing code for kind runner → shows `state-runner pair …` command to copy; list runners with projects/adapters, revoke) and "Execution policies" management (list, enable/disable, edit capabilities/mode/timeout/notifications).
  - `StateTheme.OriginBadge` gains the runner case.
- **Push/NSE**: `NotificationService.didReceive` dispatches on `payload.kind`: `run_finished` decodes `RunPushPayload{kind, runID, reminderID, occurrenceID?, status, title}`, title = "State", body = `<reminder title> — <localized status>`; `userInfo` gains `agent_run_id`, taps deep-link to the reminder detail (existing action plumbing). Fallback map extended de/en. `StateAppDelegate` registers category `STATE_RUN` (no actions in v1, view-only).
- **Localization**: all new strings in `Localizable.xcstrings` (en + de), following existing entries.
- **Tests**: model decoding for new types (incl. null `reminder_id` change events), DB v2 migration from a v1 fixture, `MockStateAPI`-based sync test covering runs inside `ReminderDetail` + null-reminder events, AppModel demo test updated, NSE payload decoding unit test for `run_finished` (plain decoder), harness catalog untouched. UI screenshot flow keeps passing (demo seeds extended, not changed shape).

## 13. Security documentation

- `docs/threat-model.md`: move "remote agent execution" out of the v1 out-of-scope line; new threats+controls rows — stolen runner credential (separate kind, per-runner revocation, no reminder-write scope), malicious task contract (server-signed? No — hash-pinned contract + local policy validation + workdir containment), lease takeover (runner-bound claims, CAS, lease expiry), policy bypass (server-side capability validation + unattended allow-list), log disclosure (local-only artifacts, redaction, 64 KiB bound), adapter supply chain (local binary verification is the owner's responsibility; adapters are thin and pinned to local `PATH`), compromised server sending commands (contracts carry no commands; runner maps adapter+contract locally). New residual risks: a compromised workstation can execute anything its policies allow until revoked; success evidence relies on adapter exit codes.
- `SECURITY.md`: add the runner credential type and the local-only artifact rule to the security model summary.
- `docs/agent-execution-extension.md`: mark the concept as implemented, point at this plan, and record the seven decisions as resolved (short addendum section; the concept text itself stays as the historical record).

## 14. Build order (each slice lands green: `gofmt`, `go vet`, `go test -race ./...`)

1. Domain models + validation (`internal/state`) + unit tests.
2. Store tables + rebuild migrations + repository methods + store integration tests (legacy-schema fixture for both migrations).
3. Memory repository parity + service layer (`execution.go`) + service tests (claim atomicity, idempotent materialization, lease requeue, approval, evidence mismatch, occurrence auto-complete).
4. Auth: `runner` kind + pairing + manager migration + tests (incl. legacy DB).
5. REST surface + OpenAPI + handler tests.
6. MCP tools + kind gates + multi-actor tests (tool list, runner flow, revocation).
7. Execution scheduler + server integration test (boot server, due occurrence with policy → run materializes; expiry).
8. Push `run_finished` end to end (service → sender → relay → dispatcher) + tests.
9. statectl project commands + tests.
10. state-runner + adapters + full loop test against a booted server.
11. iOS models/API/DB/sync + tests.
12. iOS UI + NSE + localization + unit/UI tests (`xcodegen generate`, `xcodebuild ... test`).
13. Docs (threat model, SECURITY, concept addendum) + final full verification + PR.

## 15. Acceptance criteria (verified before merge)

Every criterion from the concept's list, mapped to a test:

- One runner claims exactly one eligible run; a second runner cannot claim it — store CAS test + service test.
- Repeating a scheduler cycle or runner retry never launches the same occurrence twice — unique-key test + idempotent replay test.
- A runner rejects tasks outside its project root or capability allow-list — runner validation tests.
- A launched adapter receives a stable run ID and can pull context via MCP — runner loop test + `get_execution_context` test.
- All lifecycle transitions appear in the signed audit chain — audit verification test after a full run.
- iOS shows run state and receives an encrypted completion notification — iOS unit tests + relay/NSE tests.
- Revoking a runner blocks new claims without touching history — auth + MCP tests.
- No secret, shell command, or unredacted log lands in `.state/` or a push payload — redaction unit tests + relay payload assertions + a repo-wide grep test fixture review.
- Plus repo-wide: `go vet ./...`, `go test -race ./...` green; iOS `xcodebuild test` green; `openapi/state-v1.yaml` parses; Docker image still builds (`docker build -f deploy/Dockerfile .`).
