# Universal agent todo capture and distributed execution

**Status:** Product requirements and architecture extension

**Source:** [`universal-agent-todo-capture-transcript.md`](universal-agent-todo-capture-transcript.md)

## 1. Purpose

State is the owner-controlled system of record for reminders, recurring work, agent context, execution policies, runs, and completion evidence. This extension makes one further promise:

> When a paired agent encounters an explicit task or recurring obligation during normal work, it can persist that intent in State immediately, with enough durable context for a person or a future scheduled agent run to act on it safely.

The desired result is a single personal task inventory visible in the State iOS app, independent of where work began. A task mentioned in Codex, Claude Code, DeepSeek Harness, OpenCode, Pi Agent, or another paired harness must not disappear into an isolated chat transcript. The agent records it through State MCP, State remains canonical, and the owner can see the reminder, schedule, associated project, context, history, and any future run from the phone.

This is an extension of the already implemented reminder core and scheduled `AgentRun` model. It does not replace the agent execution design in [`agent-execution-implementation-plan.md`](agent-execution-implementation-plan.md).

## 2. Terminology and normalization

| Product-conversation term | Meaning in State |
| --- | --- |
| State MCP | The authenticated State MCP interface. It is exposed remotely at `/mcp` and locally to a harness through `statectl mcp --profile <profile>`. |
| State CLI | `statectl`, the common terminal interface for pairing, MCP proxying, project projection, diagnostics, and future capture commands. |
| Agent or harness | One independently paired client identity, such as `codex`, `claude-code`, `opencode`, `deepseek-harness`, or `pi-agent`. |
| Cloud Code | The original transcript uses this wording. Documentation and configuration should use the normalized product label `Claude Code` unless the actual target product has a different official name. |
| Reminder | The durable State record that represents one actionable task, including schedule and recurrence when applicable. |
| Recurring obligation | A reminder with calendar recurrence, for example a monthly reporting task. Each due date is a separate occurrence. |
| Context package | The bounded, auditable information that explains what must be done: objective, acceptance criteria, project reference, documentation, relevant comments, and change cursor. |
| Runner | An opted-in `state-runner` process on a workstation or another approved execution host. It pulls eligible work outbound-only. |
| Execution policy | A versioned allow-list that decides whether and how a due occurrence may create an `AgentRun`. |
| Project projection | The generated `.state/` directory. It is local, reviewable, and never the canonical database or a credential store. |

## 3. Product principles

1. **State is canonical.** A chat history, terminal session, `.state/` file, runner status, or notification may mirror data, but none of them replaces State's revisioned reminder record and audit chain.
2. **Every agent gets its own identity.** A shared token for all harnesses makes the audit trail decorative. Each installation pairs as a separate State actor and can be revoked independently.
3. **MCP is the integration contract.** Every supported agent must have a State MCP configuration, either installed by `statectl` or supplied as manual instructions. The CLI is the universal fallback for agents that cannot consume MCP directly.
4. **Capture is deliberate, not psychic.** Agents create reminders only from explicit user intent or a clear user-approved rule. A casual mention, speculative idea, or task inferred only from surrounding text must be surfaced for confirmation instead of silently becoming a recurring obligation. Surprise automation is just a bug wearing a tie.
5. **Schedules describe obligations, not shell commands.** A reminder records what should happen and when. A server or phone never sends arbitrary executable text to a machine.
6. **A workstation pulls work.** A runner creates only outbound connections to State. No inbound SSH, exposed terminal, or server-initiated shell access is required.
7. **Approval remains local and specific.** Deployments, production changes, external messages, payments, credential actions, and destructive operations remain supervised unless a future owner-approved policy explicitly permits them.
8. **Failure stays visible.** A failed, unavailable, timed-out, or approval-blocked run does not mark the occurrence complete. It remains actionable in State and produces an owner notification.
9. **Secrets do not enter State context by default.** Reminders, task contracts, `.state/`, audit events, and notification payloads must contain no credentials, private keys, connection strings, or unrestricted commands.

## 4. Current foundation

The repository already supplies most of the execution path this product requirement needs:

- `state-server` stores reminders, occurrences, recurrence, revisions, comments, audit history, scheduling, full-text search, REST, and Streamable HTTP MCP.
- `statectl` pairs harness identities, proxies State over local MCP STDIO, installs integrations for Codex, Claude Code, and OpenCode, and prints safe manual configuration for other harnesses.
- `state-runner` is an outbound-only local worker. It claims an eligible run, verifies a hash-pinned contract and local project boundary, writes local run artifacts, launches an adapter, and reports a redacted terminal result.
- The State iOS app provides offline cache, reminder visibility, execution policy and run UI, and encrypted completion or failure push notifications.
- `.state/` is generated as a local project projection. It contains project metadata, a policy template, current bounded context, and local run artifacts. It is not a transport, second database, or remote-code channel.

The shipped local runner adapters currently cover `codex`, `claude-code`, and `opencode`. The generic harness pairing path already accepts other valid harness labels, but `statectl` intentionally does not modify an unknown agent's configuration. It stores the credential and emits an MCP definition and agent rules for manual installation.

## 5. Required user journeys

### 5.1 One-off task captured during an agent session

1. A person writes an explicit request such as: "Remind me to renew the certificate next week."
2. The paired agent recognizes explicit reminder intent.
3. The agent calls `create_reminder` through its State MCP connection with a concise title, structured description, schedule, time zone, source excerpt, stable `client_request_id`, and any known project reference.
4. State validates and atomically persists the reminder and audit event.
5. Only after persistence succeeds, the agent confirms the saved reminder in its own conversation.
6. The iOS app synchronizes the record and schedules local notification coverage.

If date, time zone, or recurrence is genuinely ambiguous, the agent asks one concise follow-up question. It must not invent the missing values.

### 5.2 Recurring work captured during an agent session

Example intent: "Every month I need to make a monthly report."

1. The agent identifies an explicit recurring obligation.
2. It collects the required recurrence details. At minimum: recurrence unit, interval, local schedule, time zone, reminder lead time, and whether a reminder alone or a future agent run is intended.
3. It creates one State reminder with monthly calendar recurrence. State produces independently completable occurrences using calendar semantics, not a fragile fixed-duration approximation.
4. The reminder description preserves operational context: target system, project, relevant folder or repository, desired output, documentation links or references, acceptance criteria, and an explanation of why the work matters.
5. The owner sees every upcoming occurrence in State and receives the normal iOS notification.
6. If an execution policy is later attached, due occurrences can also materialize `AgentRun` records. Capture alone never makes a task remotely executable.

### 5.3 Scheduled execution on the primary workstation

1. The owner creates or updates a State project and initializes the matching repository with `statectl project init`.
2. The owner pairs `state-runner` on the trusted primary workstation and restricts it to explicitly allowed projects and adapters.
3. The owner creates a versioned execution policy that names the project, adapter, allowed capabilities, timeout, notification behavior, and completion rule.
4. A reminder references that policy. When an occurrence becomes eligible, the State scheduler idempotently materializes one `AgentRun`.
5. A matching runner claims the run over authenticated outbound REST long-polling. It validates contract hash, policy revision, adapter availability, configured work root, and path containment before any agent is started.
6. The runner refreshes `.state/context/current.md`, writes immutable `contract.json` and regenerated status files below `.state/runs/<run-id>/`, then launches its locally owned adapter command.
7. The launched harness receives an objective plus the local context and contract paths. It can use State MCP to read the reminder, comments, and changes needed to complete the work.
8. The runner reports lifecycle events. State records them in the signed audit chain and pushes terminal status to the phone.
9. State completes the occurrence only when the policy allows it and run evidence agrees. A nonzero process exit code and a success self-report produce `evidence_mismatch`, not a fake green checkmark.

### 5.4 Secondary execution host or VPS

A VPS can host `state-server` and State's relay. It may also run `state-runner` only when it is intentionally registered as an execution host for a project whose work is safe and available there.

The architecture must distinguish two cases:

| Need | Correct execution location |
| --- | --- |
| Work requires local source code, macOS-only tooling, private desktop access, locally installed harnesses, or a project checkout on the primary computer | The primary computer runs `state-runner` and pulls its own work. The VPS records and schedules, but does not attempt to wake it through SSH. |
| Work is self-contained on a hardened server project and the relevant runner, repository, harness, and policy are installed there | A separately paired VPS runner may claim that project's allowed work. |

The server may notify that a workstation is unavailable, but it must never solve reachability by pushing commands into the workstation. The runner reconnects and claims eligible work when online. For time-sensitive work, State keeps the reminder actionable and notifies the phone.

## 6. Universal MCP and CLI integration contract

### 6.1 Required capabilities for every supported harness

Every harness integration must provide all of the following:

1. A dedicated State actor credential, paired under a stable harness label and profile.
2. Access to State MCP for briefing, search, reminder reads, reminder writes, comments, occurrence completion, and snoozing according to the server's authorization model.
3. A startup rule block that tells the agent to request a bounded State briefing and recognize explicit reminder intent.
4. A post-write rule: never tell the user a reminder was saved until the State tool confirms persistence.
5. A conflict rule: surface ambiguity instead of silently choosing dates, recurrence, target project, or execution capability.
6. An execution rule: do not double-complete an occurrence when a runner-owned policy performs completion automatically.
7. A privacy rule: never add secrets, unrestricted commands, or full logs to a reminder, comment, run result, or `.state/` file.

### 6.2 Built-in integrations

`statectl pair` should continue to provide first-class installation and reversible marked configuration blocks for:

- Codex
- Claude Code
- OpenCode

The built-in installer already backs up an existing configuration before changing it. Pairing must remain idempotent and must not overwrite unrelated MCP settings or agent instructions.

### 6.3 Manual integrations

DeepSeek Harness, Pi Agent, and future agents may expose different configuration shapes. For every agent without a vetted automatic installer, State must support this safe manual path:

```text
statectl pair --server https://state.example.com --code ONE_TIME_CODE \
  --harness <agent-label> --profile <profile>

statectl mcp --profile <profile>
```

`statectl` stores the credential using the operating-system secret store, then prints the local MCP server declaration and the agent instruction block. The operator copies that output into the agent's own configuration. The lack of an auto-installer is not a lack of State support. It is a refusal to guess where another tool stores config, which is considerably less exciting than corrupting it.

The operator workflow, including the exact files statectl writes, the backup and removal behavior, and the prepared hints for Pi Agent and DeepSeek Harness, is described in [Agent integration](agent-integration.md).

### 6.4 CLI as universal fallback

The State CLI must expose enough functionality for terminal-only or MCP-incapable agents to participate without direct database access:

```text
statectl doctor --profile <profile>
statectl mcp --profile <profile>
statectl project init --name <project>
statectl project sync
statectl project status
statectl project validate
```

A future capture command may be added only when it maps exactly to the same validated State service operations as MCP, for example:

```text
statectl reminder create ...
statectl reminder add-context ...
statectl reminder schedule ...
```

The CLI must not become a second persistence model. It is another client of State's versioned, audited contract.

## 7. Capture decision protocol

Agents need deterministic behavior, not vague advice. The standard capture protocol is:

### 7.1 Detect intent

Treat an utterance as a capture candidate only if it includes an explicit request or a clearly declarative obligation, for example:

- "Remind me to ..."
- "I need to ..."
- "This must be done every month ..."
- "Create a task for ..."
- "Schedule ..."
- "Do not let me forget ..."

Treat it as non-capture by default when it is a hypothetical, a status update, a thought experiment, an implementation proposal, or a past event without a requested follow-up.

### 7.2 Extract a structured draft

The agent produces a draft with these fields:

| Field | Requirement |
| --- | --- |
| Title | Imperative, compact, and human-readable. |
| Description | Context, target system, desired result, and enough detail to resume the task later. |
| Schedule | Absolute date and local time where known. Use State's time-zone-aware schedule model. |
| Recurrence | Calendar rule and anchor, only for explicit recurring intent. |
| Notification lead time | User-provided value or the profile default. Never fabricate a false exact time. |
| Project | Existing State project when clearly named. Otherwise no project or one clarification. |
| Sources and documentation | Safe URLs, repository-relative paths, document titles, or State comments. No secrets. |
| Acceptance criteria | Observable evidence of completion. |
| Execution intent | `reminder-only` by default. A policy reference is attached only through owner-approved configuration. |
| Provenance | Harness actor, source excerpt, and client request ID through normal State auditing. |

### 7.3 Resolve ambiguity

The agent may choose only safe defaults already configured in the user's State profile, such as local time zone or default reminder lead time. It must ask if the answer changes the task's meaning or execution:

- Which day of the month applies?
- At what time should the notification fire?
- Is this a reminder only, or should a low-risk run be configured later?
- Which project and repository boundary apply?
- Is a suggested task actually an instruction to create one?

### 7.4 Persist and confirm

The MCP client calls `create_reminder` using an idempotency key. It confirms the resulting reminder ID and schedule only after State accepts the mutation. A network failure, validation error, or conflict is reported plainly and never converted into an imaginary reminder.

### 7.5 Enrich after capture

Context can evolve without rewriting the original obligation:

- Agents add comments for new documentation, decisions, or dependencies.
- `get_changes` and the audit chain let a later agent receive relevant updates without consuming an entire old conversation.
- A project projection refreshes a bounded local `current.md` view before an approved run.
- Edits use `expected_revision` so concurrent agents cannot silently erase each other's context.

## 8. Context package requirements

A recurring task that is supposed to be executed later needs more than a title. The State record and task contract should make a cold-started agent productive without carrying an unbounded chat archive.

### 8.1 Required context sections

```markdown
## Objective
What must be achieved now.

## Why it matters
The business, technical, or maintenance reason.

## Project boundary
State project ID, repository or directory hint, and permitted working root.

## Procedure and references
Documentation links, repository-relative paths, runbooks, and known constraints.

## Acceptance criteria
Concrete checks that distinguish completion from activity.

## Risk and approvals
Allowed capabilities, operations that need owner approval, and actions that are prohibited.

## Continuity
Relevant State comments, audit cursor, prior run summary, and unresolved blockers.
```

### 8.2 Boundedness and redaction

The context package must have size limits and redaction. It must refer to larger local documentation by path or safe URL rather than copy logs, credentials, private conversation dumps, or arbitrary shell history. The runner writes only the approved contract and bounded context below `.state/`.

### 8.3 Completion evidence

An agent result should say what changed, which verification was executed, what remains unresolved, and whether owner action is required. It is not enough to write "done". State stores a redacted, bounded summary and retains detailed local artifacts only on the execution host according to local retention policy.

## 9. Scheduling and notification behavior

1. State stores schedule and recurrence in calendar-aware form with IANA time-zone semantics.
2. The iOS app maintains rolling local notifications so the owner still receives reminders during a State-server outage.
3. The server sends encrypted push as a synchronization and fallback path, without exposing reminder plaintext to the relay.
4. A due occurrence can produce both an owner notification and an `AgentRun` when an enabled policy exists.
5. Notification delivery does not imply execution permission. An execution policy is a separate, versioned opt-in.
6. Start, approval requested, success, failure, cancellation, expiry, and unavailable-runner states must be visible in the State activity history.
7. Recurring occurrences remain independent. Completing February's report does not erase March's obligation.

## 10. Execution policy boundary

| Policy mode | Permitted baseline | Requires owner approval |
| --- | --- | --- |
| `reminder-only` | No runner invocation. | Not applicable. |
| `unattended-low-risk` | Read repository, read State context, edit repository, run tests. | Any capability outside the strict low-risk allow-list. |
| `supervised` | Start a prepared local harness session inside project bounds. | Network access, deployment, external messages, payments, credential rotation, privileged execution, destructive actions, and any unlisted capability. |

Policies must name an adapter and project. They may never embed raw shell command text. The runner owns the adapter invocation and must reject a task contract that is stale, malformed, hash-invalid, outside its work root, or incompatible with the locally configured policy.

## 11. `.state/` project projection

The projection remains a convenience layer for people and harnesses, not a hidden execution backdoor:

```text
.state/
  README.md
  project.json
  policy.yaml
  context/
    current.md
  runs/
    <run-id>/contract.json
    <run-id>/status.json
    <run-id>/result.json
  .gitignore
```

Recommended Git behavior:

- `README.md`, `project.json`, and a reviewed policy template may be committed.
- `context/`, `runs/`, logs, locks, local overrides, and artifacts are ignored by default.
- Git is never the State synchronization transport.
- The projection never contains credentials or executable server-provided commands.

## 12. Data, audit, and conflict expectations

State's existing consistency model applies to automated capture as well:

- All timestamps are server-assigned UTC timestamps, while schedules retain their intended local time zone.
- Reminder writes require stable client request IDs for retry-safe idempotency.
- Mutable records use revision checks to prevent blind overwrites.
- One transaction writes the domain mutation and immutable audit event.
- Each harness and runner has a distinct actor identity in audit history.
- Search indexes only safe reminder, comment, and redacted summary material.
- A future automatic extraction feature must record why it proposed a reminder and provide a confirmation path before persistence if intent is not explicit.

## 13. Acceptance criteria

This extension is complete only when all statements below are demonstrably true:

1. Codex, Claude Code, OpenCode, DeepSeek Harness, Pi Agent, and any future agent can be paired with a separate State identity through MCP or the documented CLI fallback.
2. Built-in installations are reversible, preserve unrelated configuration, and back up an existing config before modification.
3. A manual harness integration prints an executable `statectl mcp` configuration and rules instead of guessing another tool's config location.
4. An explicit one-off or recurring user obligation becomes an auditable State reminder after successful server confirmation.
5. Ambiguous intent, date, recurrence, project, or execution scope produces a clarification or a visible draft, not silent invention.
6. A monthly reminder produces calendar-correct, independently completable occurrences and appears in the iOS app with notification coverage.
7. A reminder can retain enough bounded context for another agent session to resume work later.
8. A due policy-backed occurrence creates no more than one runnable `AgentRun` for its occurrence and policy revision.
9. Only a paired, authorized, outbound-only runner can claim a run, and it rejects invalid contracts, unsafe capabilities, missing adapters, and work-directory escapes.
10. A VPS can operate as State server and optionally as an explicitly paired execution host, but it cannot push shell commands into the primary workstation.
11. A terminal State result reaches the phone through the existing encrypted notification path and remains visible in State's activity history.
12. Failed, blocked, cancelled, expired, or evidence-mismatched runs never silently complete the source occurrence.
13. No credentials, unrestricted shell commands, or unredacted full logs are persisted in State, `.state/`, audit summaries, or push payloads.

## 14. Explicit non-goals

This extension does not propose:

- a generic agent-chat application inside State;
- an inbound remote-control service for personal computers;
- a universal background worker that can run every task without policy and approval;
- automated configuration edits for unknown agent products;
- a Git-based synchronization protocol for State records;
- server-side storage of private local agent sessions or full logs by default;
- automatic execution of deployments, destructive operations, external communication, credential work, or payments.

The system is valuable precisely because a task can survive every chat and every machine without turning the phone into an unrestricted remote terminal. That constraint is a feature, not bureaucracy.
