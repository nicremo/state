# T3 Code as agent and a calmer iPhone app

Date: 2026-11-07. Status: approved by the owner up front ("ohne Rückfragen komplett durchziehen").
Sparring: one Haiku 5.5 discussion on the T3 architecture, three read-only Sonnet 5.5 UI audits.

## Goals

1. The owner starts an agent task in the State app (Agent tab or from a reminder) and it runs
   on his Mac as a thread in T3 Code, the harness app he orchestrates all agents from.
2. The iPhone app gets calmer: no overlapping header on the home screen, switches that look
   like switches, fewer controls to tap, shorter copy. Nothing functional is removed.

## Part A: T3 Code adapter

### What T3 offers (measured on this Mac, t3 0.1.7)

- `t3 thread start --wait [--json] [--title T] <path> <message|->` starts a thread in the T3
  project that contains `<path>` and waits for the run. Output:
  `{"threadId","runId","status","reply","error"}`. Exit 0 done, exit 1 run failed, exit 2 the
  thread waits for an answer in T3 (JSON is printed before the exit, so the thread ID is known).
- `t3 thread send --wait --json <threadId> <message|->` adds the next turn, same output.
- `t3 thread show --json <threadId>` returns the thread with `link`
  (`https://app.t3.codes/threads/<environment>/<thread>`, reachable from the phone through
  T3 Connect) and `runtimeMode`.
- `t3 project add <path>` registers a folder as a T3 project.
- No command interrupts a running thread. Approval rules live in T3 per provider.
- T3 Connect is T3's own remote channel for T3's own clients. State cannot join it, so State
  reaches T3 through the runner that already runs on the Mac.

### Design

A new runner adapter `t3-code` next to `claude-code`, `codex` and the others.

- Binary: `t3` on `PATH`, else `~/.t3/bin/t3` (the launchd `PATH` of the runner does not
  contain `~/.t3/bin`).
- First round: `t3 thread start --wait --title <objective first line> <checkout> -`, prompt
  on stdin (no length or quoting limits in argv). Without `--json` the CLI writes
  `Thread <id>` to stderr as soon as the thread exists and the reply to stdout at the end;
  exit codes are the same. So the thread ID is known even when State cancels the round,
  which `--json` (printed only at the end) cannot give.
- Later rounds: `t3 thread send --wait <threadId> -`. The thread ID is the
  `HarnessSessionID`, so the existing resume plumbing carries it.
- Before a first round the adapter checks the project with
  `t3 thread list --json --project <checkout>`; if T3 answers "No T3 Code project matches",
  it runs `t3 project add <checkout>`, so a fresh project needs no setup in T3.
- After the round the adapter reads `t3 thread show --json <threadId>` for the link.
- Result mapping:
  - exit 0: succeeded, stdout (the reply) is the result text.
  - exit 2: the round is handed over to T3 and waits for the owner there. Reported as
    succeeded (the server knows only succeeded and failed, and nothing went wrong) with the
    result text "Wartet in T3 Code auf deine Antwort." and the link. The next State round is
    queued by T3 as the thread's next turn.
  - any other exit: failed, the last stderr line (T3's error message) is the result text.
- Cancel in State kills the waiting `t3` process. The T3 thread keeps running; the summary
  says so ("In State abgebrochen. Der Thread läuft in T3 Code weiter."), the link stays.
- Rights: T3 does not enforce State's read and edit levels. The adapter therefore only
  accepts policies with full rights and rejects others with a clear message
  ("T3 Code setzt die Rechte der Policy nicht durch. Gib der Policy volle Rechte."). This keeps
  State's promise that a policy's rights are the ceiling. (Haiku sparring: block, not warn.)
- "Am Mac öffnen" (terminal resume) is not offered for `t3-code`; the link replaces it.
- `--wait` stays: the runner executes one run at a time like `claude -p` does, heartbeats keep
  the lease, and the policy timeout bounds the round. (Haiku proposed fire and forget with
  polling; rejected because it adds a second state machine for no gain in this runner.)

### Data

- `AgentRun.harness_link` and `CompleteRunInput.harness_link`: optional, absolute `https` URL
  without credentials, at most 512 characters, validated on the server. Stored inside the run
  JSON, so no migration.
- `AgentSessionView.harness_link`: the latest link of its turns.
- OpenAPI and the iOS models gain the field. The runner sends it only when set, so a runner
  without T3 keeps working against an older server.

### Setup

`state-server agent-project --name <folder> --adapter t3-code --rights full --runner "<name>"`
creates the policy and adds the adapter to the server's runner record. The runner's local
`runner.json` lists `t3-code` under `adapters`. Documented in `docs/runner-service.md`.

### Tests

- Adapter argument and output tests with a fake `t3` script on `PATH` (start, send, exit 2,
  exit 1, unknown project then `project add`, link from `thread show`).
- Rights gate test.
- Server: `harness_link` validation and session view.
- End to end on this Mac: temporary server and runner on a temp data dir, real `t3`, one
  thread in a scratch project.

## Part B: UI polish

Filled from the three audits, see the plan for the concrete list. Principles:

- System look over custom: switches use the system green; the global ink tint stays for
  buttons and links.
- The home screen header has one clear order: title, search, then the Heute/Geplant switch
  pinned below the search field with its own background, never under the navigation bar.
- Fewer sections and shorter footers in Settings and Notes; rarely used actions move into menus.
- Agent tab: start a task in as few taps as possible (a "Neue Aufgabe" button in the Agent tab
  creates the reminder and starts the session), remember the last policy but always show the
  choice when there is more than one, show "In T3 Code öffnen" for T3 sessions.
- Not changed after sparring: notes plus menu, formatting bar, end session confirmation,
  editor agent section.
