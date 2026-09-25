# Agent sessions: dispatch agents from the app and iterate with them

> **For agentic workers:** Steps use checkbox (`- [ ]`) syntax. Executed inline with TDD in one session, branch `feat/agent-sessions`.

**Goal:** The owner taps "Jetzt arbeiten lassen" on a reminder, a coding agent (Claude Code, Codex or Kimi Code) starts on the owner's Mac inside a State-managed session, its answer appears in a new Agent tab, and the owner replies with feedback for as many rounds as needed, or opens the same session in a terminal on the Mac.

**Architecture:** A new `AgentSession` groups rounds. Every round is an ordinary `AgentRun` (claim, lease, heartbeat, cancel, approval, audit and push all stay as they are) that carries the session ID, the round's prompt and, when finished, the agent's final message. The runner starts the agent CLI non-interactively in the project folder, the first round without and every later round with the CLI's own resume flag, reads the final message and the CLI session ID from the structured output, and reports both. The app gets a new tab layout: Agenda (Today and Planned in one tab, Activity behind a toolbar button), Notes, Agent, Settings.

**Tech Stack:** Go 1.24 (PocketBase/SQLite), `state-runner` (Go, launchd on macOS), SwiftUI for iOS 18 and macOS 15, the CLIs `claude`, `codex` and `kimi` on the owner's Mac.

**Spec:** The owner's request is quoted in the agent log `agent-logs/2026-09-25-1629-agent-sessions-plan.md` (local). Decision from the brainstorm: rounds model plus "open on the Mac". Background: [`../../agent-execution-implementation-plan.md`](../../agent-execution-implementation-plan.md), [`../../runner-service.md`](../../runner-service.md).

## Global Constraints

- Only the owner and the owner's devices start, continue, open and close sessions. Harness agents and runners never do; runners never see session audit events.
- A runner only executes rounds of projects and adapters in its local configuration, inside its work root, with a verified contract hash (unchanged rules).
- The contract hash of runs created before this change must stay valid: every new contract field is `omitempty`.
- Round results are redacted on the runner and again on the server (credential-looking lines removed) and bounded (20000 characters for the result text, 2000 for the summary).
- No secret, token or key is read, printed, logged or committed. The agent CLIs use the owner's existing logins.
- Offline sync, conflict copies and the notes AI stay unchanged.
- No dashes (U+2013, U+2014) in code comments, UI texts, docs. Correct umlauts. German in `Localizable.xcstrings`. No AI attribution. Git in English.

## Decisions

| # | Decision | Why |
| --- | --- | --- |
| D1 | Rounds model: each owner message is one run of the session; the agent works until it ends the round, then waits | Chosen in the brainstorm; robust, testable, the same for all CLIs |
| D2 | `AgentSession` is a new stored entity; rounds are `AgentRun` rows with `session_id`, `turn_kind`, `prompt`, `result_text` | Reuses the audited run lifecycle instead of a second one |
| D3 | Sessions always start from a reminder and a policy (project, agent, rights, timeout) | Matches the request ("auf Geplant, Karla Monatsreport, jetzt arbeiten lassen") and the existing NOT NULL columns; a free-form session is not needed yet |
| D4 | New contract fields `session_id`, `turn_kind`, `resume_session_id`, all `omitempty` | Old hashes stay valid; the runner learns everything from the signed contract |
| D5 | The CLI session ID is read from the output of the first round and stored on the session; later rounds pass it as `resume_session_id` | Works the same way for all three CLIs (probed on the owner's Mac) |
| D6 | Adapter flags follow the policy's capabilities: read only by default, `edit_repository` allows edits, any of `run_tests`, `network_access`, `write_state`, `deploy`, `message_external`, `destructive` gives full rights | In non-interactive mode the agent cannot ask; the owner decides the rights once in the policy. Applies to scheduled runs too |
| D7 | "Am Mac öffnen" is a round of kind `open_terminal`: the runner writes a `.command` file with the CLI's resume command and opens it with `/usr/bin/open`; the round succeeds at once | The iPhone cannot reach the Mac directly and the Mac app is sandboxed; the runner is the process on the Mac that may start programs |
| D8 | Session status is derived from the latest round: working, needs approval, waiting for you, failed, closed | Nothing to keep in sync |
| D9 | Every finished round of a session notifies (push payload with `session_id`), independent of the policy's notify flags | The owner asked to be told when the agent is done |
| D10 | Navigation: tabs Agenda (Heute and Geplant with a segmented control, Activity as a toolbar button left of plus), Notizen, Agent, Einstellungen; the iPad and Mac sidebar lists the same four | Requested layout; "Agenda" is the name for the merged tab |
| D11 | The Agent tab refreshes every 5 seconds while a visible session is working, plus on appear and pull to refresh | No streaming channel exists; "nur wenn fertig" is enough per the request |
| D12 | "Jetzt arbeiten lassen" replaces the old one-shot "Run now" button on the reminder | Two manual paths would confuse; scheduled runs stay as they are |
| D13 | New adapter `kimi-code` (`kimi -p ... --output-format stream-json`, resume with `-S`) | Requested ("Kimi") and probed |
| D14 | Live test on a harmless probe project, not on the Karla report | The report is due on 02.10. and does real work; its policy is prepared so the button is ready |
| D15 | Added during E2E: the owner's devices start, answer, open, close and cancel rounds of sessions; projects, policies and approvals stay with the owner | On the Mac Server the owner is the desktop app and the iPhone is a device; the owner decides what agents may do, the devices dispatch within that |
| D16 | Added during E2E: `POST /api/v1/agent-sessions/{id}/cancel` for owner and devices instead of the owner-only run cancel | Follows from D15 |
| D17 | Added during E2E: `state-server agent-project --data --name --adapter --rights --runner` sets up project, policy and runner scope as the owner, locally on the data directory, idempotent | The iPhone cannot create policies (D15) and the Mac Server has no owner UI |

### Probed CLI behaviour (25.09.2026, owner's Mac)

| CLI | First round | Later round | Final text | Session ID |
| --- | --- | --- | --- | --- |
| `claude` | `claude -p --output-format json <prompt>` | `claude -p --output-format json --resume <id> <prompt>` | `result` of the JSON object, `is_error` marks failure | `session_id` |
| `codex` | `codex exec --json --skip-git-repo-check <prompt>` | `codex exec resume --json --skip-git-repo-check <id> <prompt>` | text of the last `item.completed` with `item.type == "agent_message"` | `thread_id` of `thread.started` |
| `kimi` | `kimi -p <prompt> --output-format stream-json` | `kimi -S <id> -p <prompt> --output-format stream-json` | `content` of the last line with `role == "assistant"` | `session_id` of `session.resume_hint` |

## File Structure

Server:
- Create `internal/state/agent_sessions.go`: `AgentSession`, `AgentSessionView`, `SessionStatus`, inputs, `StartAgentSession`, `SendAgentSessionMessage`, `OpenAgentSessionOnMac`, `CloseAgentSession`, `GetAgentSession`, `ListAgentSessions`, `deriveSessionStatus`, audit actions.
- Create `internal/state/agent_sessions_test.go`.
- Modify `internal/state/execution_models.go`: `TaskContract` session fields, `AgentRun.SessionID`, `TurnKind`, `Prompt`, `ResultText`, `CompleteRunInput.ResultText`, `HarnessSessionID`, `AgentRunListFilter.SessionID`.
- Modify `internal/state/execution.go`: `CompleteAgentRun` stores result text and the CLI session ID, notifications for session rounds.
- Modify `internal/state/notes.go` (`VisibleChanges`): hide `agent_session.` events from runners.
- Modify repository interface, `internal/state/memory_repository.go`, `internal/store/execution_repository.go`, `internal/store/pocketbase_repository.go`: `state_agent_sessions` table, session filter on runs, session update inside the run completion transaction.
- Modify `internal/push/service.go`: `session_id` in the payload, round title.
- Modify `internal/api/handler.go` plus new `internal/api/agent_sessions_handler.go` and test.
- Modify `openapi/state-v1.yaml`.

Runner:
- Modify `internal/runner/adapters.go`: session-aware arguments, permission flags, output capture, parsers, `kimi-code`.
- Create `internal/runner/session_output.go` (parsers) and `internal/runner/session_output_test.go` with fixtures from the probes.
- Modify `internal/runner/run.go`: pass session data, report result text and CLI session ID, `open_terminal` rounds.
- Modify `internal/runner/client.go` if the completion payload needs new fields.

App:
- Modify `ios/State/Sources/Models/Models.swift` (or a new `AgentSession.swift`): `AgentSession`, `AgentSessionStatus`, run fields.
- Modify `ios/State/Sources/Networking/APIClient.swift`, `ios/State/Sources/App/AppModel.swift`.
- Modify `ios/State/Sources/UI/MainTabView.swift`, `ios/State/Sources/UI/SplitRootView.swift`: Agenda, Agent tab, Activity button.
- Create `ios/State/Sources/UI/AgentSessionsView.swift` (list), `ios/State/Sources/UI/AgentSessionDetailView.swift` (conversation, composer, actions), `ios/State/Sources/UI/StartAgentSessionSheet.swift`.
- Modify `ios/State/Sources/UI/ReminderDetailView.swift`: "Jetzt arbeiten lassen".
- Modify push handling (`ios/State/Sources/Notifications/*`) to open the session.
- Modify `ios/State/Resources/Localizable.xcstrings`.
- Tests: `ios/StateTests/AgentSessionTests.swift`.

## Tasks

### Task 1: Session domain and rules (server)

**Interfaces:**
```go
type SessionStatus string // "working" | "needs_approval" | "waiting" | "failed" | "closed"
type TurnKind string       // "message" | "open_terminal"
type AgentSession struct {
    ID, ReminderID, PolicyID, ProjectID, Adapter, Title string
    HarnessSessionID string `json:"harness_session_id,omitempty"`
    Closed bool; ClosedAt *time.Time
    CreatedByActor Actor; Revision int64; CreatedAt, UpdatedAt time.Time
}
type AgentSessionView struct { AgentSession; Status SessionStatus; Turns []AgentRun }
type StartAgentSessionInput struct { ReminderID, PolicyID, Instruction string; MutationMetadata }
type SendAgentSessionMessageInput struct { SessionID, Text string; MutationMetadata }
type AgentSessionActionInput struct { SessionID string; MutationMetadata }
func (s *Service) StartAgentSession(ctx, actor, StartAgentSessionInput) (AgentSessionView, error)
func (s *Service) SendAgentSessionMessage(ctx, actor, SendAgentSessionMessageInput) (AgentSessionView, error)
func (s *Service) OpenAgentSessionOnMac(ctx, actor, AgentSessionActionInput) (AgentSessionView, error)
func (s *Service) CloseAgentSession(ctx, actor, AgentSessionActionInput) (AgentSessionView, error)
func (s *Service) GetAgentSession(ctx, id) (AgentSessionView, error)
func (s *Service) ListAgentSessions(ctx, limit int) ([]AgentSessionView, error)
```
Rules: owner and device only; policy must be enabled; message text 1 to 8000 characters; a message or an open request is refused with `ErrRunStateConflict` while a round is not terminal; open needs a known CLI session ID; closed sessions refuse everything but reads; idempotent by `client_request_id`.

- [ ] Tests: start creates session and eligible first round whose contract objective contains reminder title, description and instruction, and `turn_kind=message`, no resume ID; harness and runner get `ErrForbidden`; message while working is a conflict; after the first round succeeded with a CLI session ID the next round carries `resume_session_id`; open without CLI session ID is invalid; close then message is a conflict; derived status for each case; a contract without session fields keeps its old hash (golden hash of a fixed contract).
- [ ] Implement with the memory repository. Commit.

### Task 2: Storage (server)

- [ ] Store tests: session round trip with restart, rounds listed by session in order, audit chain valid, completion updates session in the same transaction.
- [ ] `state_agent_sessions (id, reminder_id, created_at, data_json)`; runs keep `session_id` in `data_json`, list filter via `json_extract`. Commit.

### Task 3: Completion carries the answer (server)

- [ ] Tests: `CompleteRunInput.ResultText` is redacted, bounded and stored; `HarnessSessionID` must match `^[A-Za-z0-9_.:-]{1,128}$`, is stored on the session only once; session rounds notify on success, failure and cancel even if the policy does not ask; push payload has `session_id`; a runner's change feed contains no `agent_session.` events.
- [ ] Implement. Commit.

### Task 4: REST (server)

Endpoints: `POST /api/v1/agent-sessions` (start), `GET /api/v1/agent-sessions`, `GET /api/v1/agent-sessions/{id}`, `POST /api/v1/agent-sessions/{id}/messages`, `POST /api/v1/agent-sessions/{id}/open`, `POST /api/v1/agent-sessions/{id}/close`. Cancelling the current round uses the existing `POST /api/v1/runs/{id}/cancel`.
- [ ] Handler tests for rights (owner, device, harness, runner), conflict 409, invalid 400, not found 404. OpenAPI. Commit.

### Task 5: Runner adapters with sessions and rights

**Interfaces:**
```go
type StartRequest struct { Contract state.TaskContract; Dir, Prompt string }
type Result struct { ExitCode int; Tail string; Text string; HarnessSessionID string }
func adapterArguments(slug string, contract state.TaskContract, prompt string) []string
func parseClaudeOutput([]byte) (text, sessionID string, failed bool)
func parseCodexOutput([]byte) (text, sessionID string)
func parseKimiOutput([]byte) (text, sessionID string)
```
- [ ] Tests: arguments per adapter for first and later round and for each rights level (read only, edits, full); parsers with the probed outputs, with extra noise lines, with an error result; output capture bounded at 4 MB; `kimi-code` registered.
- [ ] Implement. Commit.

### Task 6: Runner session rounds and "Am Mac öffnen"

- [ ] Tests with a fake `claude` script on PATH that records its arguments and prints the JSON shape: first round reports text and CLI session ID; second round was started with `--resume <id>`; a round whose CLI reports an error fails with its text; an `open_terminal` round writes an executable `.command` file in `.state/sessions/<session>/` containing `cd <checkout>` and the resume command, calls the injected opener, and succeeds without starting the agent.
- [ ] Implement. Commit.

### Task 7: App data layer

- [ ] Tests: decoding a session view from server JSON; status mapping; composer enabled only when waiting; resume command text per adapter.
- [ ] Models, API client, `AppModel` (`agentSessions`, `loadAgentSessions`, `startAgentSession`, `sendAgentMessage`, `openSessionOnMac`, `closeSession`, `cancelCurrentRound`, polling). Commit.

### Task 8: Navigation

- [ ] `StateTab`: `agenda`, `notes`, `agent`, `settings`; Agenda shows a segmented control Heute / Geplant; toolbar button Aktivität (with conflict badge) left of the plus opens Activity; iPad and Mac sidebar with the same four; DEBUG launch arguments map `today`, `planned`, `activity`, `agent`. Commit.

### Task 9: Agent tab

- [ ] List: sessions grouped Aktiv / Beendet, row with title, agent, status, last answer snippet, relative time; empty state explains how to start one.
- [ ] Detail: conversation (owner messages right aligned, agent answers as Markdown), working row with elapsed time, approval card (existing approve API), failure text, composer at the bottom, menu with "Am Mac öffnen", "Befehl kopieren", "Runde abbrechen", "Session beenden".
- [ ] Localization, accessibility identifiers. Commit.

### Task 10: "Jetzt arbeiten lassen"

- [ ] Reminder detail: button for the owner when at least one enabled policy exists; sheet with policy picker (reminder's policy preselected), optional instruction, start; afterwards the Agent tab opens on the new session. Old "Run now" removed. Commit.

### Task 11: Push tap

- [ ] A `run_finished` push with `session_id` opens the Agent tab on that session. Test for payload decoding. Commit.

### Task 12: Verify and ship

- [ ] Docs: README, DOCUMENTATION (Agent sessions section), `docs/runner-service.md`, state-sync skill if tools change.
- [ ] `go vet`, `gofmt`, `go test -race ./...`, iOS `StateTests`, `StateMac` build.
- [ ] Independent code review by a subagent; every confirmed finding fixed with a regression test.
- [ ] MobAI end to end on the iPhone simulator: local server, local runner with a fake `claude` on PATH, reminder "Jetzt arbeiten lassen", answer in the Agent tab, feedback round, new tab bar and Agenda switch, Activity button.
- [ ] Live on the Mac: new server and runner, probe project, two real Claude Code rounds where the second proves the resume ("Welches Wort habe ich dir gesagt?"), "Am Mac öffnen" opens a terminal.
- [ ] Karla report prepared: project for its folder, runner project list, policy with full rights and Claude Code on the reminder; not run.
- [ ] PR, merge, deploy server and runner (backup first), TestFlight iOS and Mac.
