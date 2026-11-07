# T3 Code agent and UI polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. This plan is executed inline (owner's choice).

**Goal:** Agent tasks started in the State app run as T3 Code threads on the owner's Mac, and the iPhone app gets a calmer, simpler interface.

**Architecture:** A new runner adapter `t3-code` drives the `t3` CLI (`thread start|send --wait`, `thread show --json`, `project add`). The run result gains an optional `harness_link` (the T3 thread URL) that the server validates, stores in the run JSON and surfaces on the session; the iOS app shows it as "In T3 Code öffnen". The UI tasks are local SwiftUI edits taken from three read-only audits.

**Tech Stack:** Go 1.x (server, runner), SwiftUI iOS 18+ (XcodeGen project in `ios/`), T3 Code CLI 0.1.7.

**Spec:** `docs/superpowers/specs/2026-11-07-t3-code-agent-and-ui-polish-design.md`

## Global Constraints

- German UI copy with correct umlauts, no em or en dashes anywhere (code comments English).
- Commits with the repo's git identity, no AI attribution, format `feat|fix|refactor|docs: ...`.
- No data reset, no secrets read; `runner.json` is edited only by adding `t3-code` to `adapters`.
- `t3-code` accepts only policies with full rights.
- iOS deployment target 18.0; iOS 26 APIs only behind `#available`.
- Verification: `go test -race ./...`, `go vet ./...`, `gofmt -l cmd internal` empty, iOS `StateTests` in the simulator, `StateMac` build.

---

### Task 1: `harness_link` on runs and sessions (server)

**Files:**
- Modify: `internal/state/execution_models.go` (AgentRun, CompleteRunInput)
- Modify: `internal/state/execution.go` (CompleteRun validation and copy)
- Modify: `internal/state/agent_sessions.go` (AgentSessionView.HarnessLink, sessionView)
- Modify: `openapi/state-v1.yaml` (AgentRun, CompleteRunInput, AgentSessionView)
- Test: `internal/state/agent_sessions_test.go`

**Interfaces:**
- Produces: `state.ValidHarnessLink(link string) bool`; JSON field `harness_link` on `AgentRun`, `CompleteRunInput`, `AgentSessionView`.

- [ ] Step 1: failing test `TestCompleteRunStoresHarnessLinkAndSessionShowsIt` (valid https link stored, session view carries the latest one) and `TestValidHarnessLink` (accepts `https://app.t3.codes/threads/a/b`, rejects `http://`, `https://user:pw@x/`, relative, > 512 chars, empty host).
- [ ] Step 2: `go test ./internal/state -run 'HarnessLink'` fails (undefined).
- [ ] Step 3: implement

```go
// ValidHarnessLink reports whether a link to the agent's own view of a session
// is safe to store and open: absolute https, a host, no credentials, bounded.
func ValidHarnessLink(link string) bool {
	if link == "" || len(link) > maxHarnessLinkLength {
		return false
	}
	parsed, err := url.Parse(link)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}
```

In `CompleteRun`: reject a non-empty invalid link with `ErrInvalidInput`, copy `updated.HarnessLink = input.HarnessLink`. In `sessionView`: the last turn with a link wins.
- [ ] Step 4: tests pass; `go test ./internal/...`.
- [ ] Step 5: commit `feat: harness link on agent runs and sessions`.

### Task 2: `t3-code` runner adapter

**Files:**
- Create: `internal/runner/t3_adapter.go`
- Modify: `internal/runner/adapters.go` (registry, `Result.HarnessLink`, doc comment)
- Modify: `internal/runner/run.go` (pass `HarnessLink`, cancel text)
- Modify: `cmd/state-server/agent_project.go` (flag help lists `t3-code`)
- Test: `internal/runner/t3_adapter_test.go`

**Interfaces:**
- Consumes: `state.ValidHarnessSessionID`, `state.ValidHarnessLink`, `rightsFor`.
- Produces: adapter slug `t3-code`; `Result.HarnessLink string`.

Behaviour:
- Binary: `exec.LookPath("t3")`, else `$HOME/.t3/bin/t3` if executable, else `ErrAdapterUnavailable`.
- `Validate`: binary present and `rightsFor(contract.AllowedCapabilities) == rightsFull`, else error `T3 Code setzt die Rechte der Policy nicht durch. Gib der Policy volle Rechte.` wrapped in `ErrAdapterUnavailable`.
- `Start`: for a first round, `t3 thread list --json --project <dir>` (30 s); on output containing `No T3 Code project matches`, `t3 project add <dir>` (30 s). Then the main command with the prompt on stdin:
  - first round: `thread start --wait --title <first line of objective, max 80 runes> <dir> -`
  - later round: `thread send --wait <threadId> -`
- stderr is scanned for `Thread <id>` (first round); stdout is the reply.
- `Wait`: exit 0 → `Text = stdout`; exit 2 → `ExitCode 0`, `Text = "Wartet in T3 Code auf deine Antwort."`; otherwise failed with last non-empty stderr line as text. Afterwards, if a thread ID is known, `t3 thread show --json --messages 1 <id>` (15 s, fresh context) → `thread.link` if `ValidHarnessLink`.
- `Cancel`: kill the process group; `Wait` then still resolves the link.
- run.go: `HarnessLink: result.HarnessLink` in `CompleteRunInput`; when cancelled and a link exists, `ResultText = "In State abgebrochen. Der Thread läuft in T3 Code weiter."`.

- [ ] Step 1: tests with a fake `t3` shell script on `PATH` that logs argv and stdin: first round (list, start args, stdin prompt, reply text, session ID, link from show), project add when list reports no project, later round uses `send` with the thread ID, exit 2 → succeeded with waiting text, exit 1 → failed with stderr text, rights gate rejects edit rights, fallback to `$HOME/.t3/bin/t3`.
- [ ] Step 2: tests fail.
- [ ] Step 3: implement `t3_adapter.go`, registry entry, run.go plumbing.
- [ ] Step 4: `go test -race ./internal/runner/ ./internal/state/`.
- [ ] Step 5: commit `feat: t3-code runner adapter runs agent rounds as T3 Code threads`.

### Task 3: iOS T3 support in the Agent tab

**Files:**
- Modify: `ios/State/Sources/Models/AgentSession.swift` (`harnessLink`, `AgentNames` `t3-code`, fallback to `HarnessCatalog.displayLabel`)
- Modify: `ios/State/Sources/Models/Models.swift` (`AgentRun.harnessLink`)
- Modify: `ios/State/Sources/Models/HarnessCatalog.swift` (`adapterPresets` + `t3-code`, `kimi-code`)
- Modify: `ios/State/Sources/UI/AgentSessionDetailView.swift` (link in header and menu, hide terminal actions for adapters without resume, readable failure text)
- Modify: `ios/State/Sources/UI/AgentSessionsView.swift` (no "Ended" pill in the ended section, toolbar "Neue Aufgabe")
- Modify: `ios/State/Sources/UI/StartAgentSessionSheet.swift` (remember last policy, hide picker for one policy, optional free task text when no reminder)
- Modify: `ios/State/Sources/App/AppModel.swift` (`startAgentTask(title:policyID:instruction:)` creates a reminder then starts the session)
- Modify: `ios/State/Sources/UI/RunDetailView.swift` (adapter display names)
- Test: `ios/StateTests` decoding test for `harness_link`

- [ ] Step 1: decoding test for `AgentSession` and `AgentRun` with `harness_link`.
- [ ] Step 2: implement model and views.
- [ ] Step 3: `xcodebuild test ... -only-testing:StateTests` passes.
- [ ] Step 4: commit `feat: T3 Code sessions and faster task start in the Agent tab`.

### Task 4: Home screen header and list

**Files:** `ios/State/Sources/UI/MainTabView.swift`, `ReminderDetailView.swift`, `ReminderEditorView.swift`, `StateTheme.swift`

- [ ] Segmented Heute/Geplant as `.principal` toolbar item with inline title on iPhone (compact width); the iPad split keeps the top inset with `.background(.bar, ignoresSafeAreaEdges: [])` and padding.
- [ ] Remove the empty sync spinner toolbar item (pull to refresh shows progress).
- [ ] Activity leaves the agenda toolbar; reachable from Settings.
- [ ] Origin badge only for actors other than the owner.
- [ ] Empty state without the duplicate "New reminder" button; check box with a 44 pt tap area.
- [ ] Overdue and status captions use a contrast-safe token.
- [ ] Detail: drop `r<revision>`, Archive into the "…" menu.
- [ ] Simulator screenshot light and dark, commit `fix: calmer agenda header and reminder screens`.

### Task 5: Switches, Settings and Notes

**Files:** `StateTheme.swift`, all files with `Toggle`, `SettingsView.swift`, `PolicyEditorView.swift`, `NoteAttachmentsView.swift`, `NoteCaptureViews.swift`, `NoteDetailView.swift`

- [ ] `View.stateSwitch()` = `.tint(.green)`, applied to every `Toggle`.
- [ ] Settings: Sync row inside the connection section; Notes AI and Notifications in one section; Activity row; Help collapsed into one row.
- [ ] Policy editor: no "Enabled" toggle when editing (the list has it).
- [ ] Notes AI: shorter footers, key actions in one menu.
- [ ] Simulator screenshots, commit `fix: green switches and simpler settings and notes`.

### Task 6: Docs, setup on this Mac, end to end

- [ ] `docs/runner-service.md`, `README.md` adapter list, `DOCUMENTATION.md`: T3 Code adapter, setup command, rights rule, cancel semantics.
- [ ] End to end: temp server data dir + temp runner config, real `t3`, scratch project under a temp work root, one round, check link and reply; remove the scratch T3 project afterwards.
- [ ] Full verification suite, agent log, PR.

## Sparring decisions (Haiku 5.5, second round)

- Kept: editor agent section (it is the entry to creating a policy), visible plus menu for notes
  (long press is not discoverable), the 11 button formatting bar (no usage data), the end
  session confirmation.
- Start sheet: with several policies the picker always shows, preselected with the last used
  one; it hides only when exactly one policy exists.
- Failure states: T3 errors ("T3 Code läuft nicht", rights gate) surface as readable text in
  the session.
