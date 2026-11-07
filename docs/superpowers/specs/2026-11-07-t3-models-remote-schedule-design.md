# T3 models, access from anywhere, scheduled sessions and catch-up

Date: 2026-11-07. Status: approved by the owner up front ("Setzt das alles komplett bitte um").
Follows `2026-11-07-t3-code-agent-and-ui-polish-design.md`.

## Facts measured on this Mac

- T3 agents run in the background service `com.t3tools.t3code.service` (`t3 serve`, PID 990);
  the agent processes are its children, the desktop app is a separate process. The service
  binary `~/.t3/runtime/versions/<activeVersion>/t3` works as the CLI on its own (0.34 s for
  `thread list`), so T3 Code needs no open desktop app. `activeVersion` is in
  `~/.t3/runtime/service-state.json`.
- Enabled T3 providers: `claudeAgent_claude_hub` (Hub), `codex`, `acpRegistry_kimi` (Kimi),
  `acpRegistry_dsh` (DSH, DeepSeek). Each has `~/.t3/caches/<id>.json` with `enabled`, `status`,
  `displayName` and `models[{slug, name, isDefault}]`; `~/.t3/userdata/settings.json`
  `providerModelPreferences.<id>.hiddenModels` lists what the owner hid in T3.
- No T3 environment is linked (`t3 environment list`), and `t3 thread start` cannot target
  another environment.
- The State server materializes runs for due occurrences at start and every 30 s; eligible
  runs expire after 24 h. No reminder has a policy yet. Mac-Tagespflege has 397 open past
  occurrences; its `get_reminder` answer is 145 922 characters.
- The desktop server answers only private, loopback and link-local peers: a request over the
  Mac's Tailscale address `100.92.239.22` gets 403 "local network only". The iPhone pins the
  certificate fingerprint per origin and does not check the host name.

## Decisions

### 1. Model catalog

The runner reads the T3 caches, keeps providers that are enabled and `ready`, drops models
listed as hidden, and reports `[{provider, provider_name, model, model_name, default}]` with
`PUT /api/v1/runner/harness-models` at start and every 10 minutes when it changed. The server
stores it on the runner record (`harness_models`); devices already sync runners, so the app
reads it without a new read endpoint.

### 2. Model selection travels with the work

`AgentModel {provider, model}`:

- `Reminder.execution_model` (owner and devices set it in the editor's agent section; agents
  can read it),
- `StartAgentSessionInput.model` and `AgentSession.model`,
- `TaskContract.model_provider` and `TaskContract.model` (omitempty, old contract hashes stay).

The runner passes them to `t3 thread start --provider --model`. When the editor assigns a
`t3-code` policy it always stores a model (preselected: the catalog's default of the first
provider), so scheduled sessions never depend on recent manual activity. Only a contract
without a model (older clients) uses the fallback: provider and model of the project's newest
T3 thread. Later rounds stay in their thread and do not change the model.

### 3. The app shows T3 only

A visible switch in Settings, "Nur T3 Code" (default on), controls it. While it is on and a
`t3-code` policy exists, the start sheet and the reminder editor offer only
`t3-code` policies, labelled **Projekt** with the folder name and its description, plus a
**Modell** picker grouped by provider (Claude, Codex, Kimi, DeepSeek). Other policies stay
visible in Settings. Without any `t3-code` policy the old behaviour stays, so other State
installations keep working.

### 4. Scheduled T3 sessions with catch-up

`MaterializeEligibleRuns` creates an agent session (actor: system) instead of a bare run when
the reminder's policy uses `t3-code`. The first round carries the occurrence ID, so a successful
round completes the occurrence like a scheduled run does, and the reminder's
`execution_model`. Per reminder only the newest due occurrence is a candidate, so a backlog of
a daily reminder becomes one session, not hundreds; when it already has a run, nothing new is
created (no cascade through the backlog). The first round names the due date and lists older
due occurrences that were not started ("Ebenfalls offen und nicht automatisch gestartet"), so
an agent can catch up, for example build two missed monthly reports, and nothing is lost
silently. The idempotency key per occurrence and policy revision stays.

Catch-up: server and runner live on the same Mac, the scheduler runs immediately at start, so
a due date that passed while the Mac was off starts the session as soon as the Mac is on.
Rounds wait as `eligible` until a runner claims them.

### 5. Offline starts on the iPhone

A start (from a reminder or a typed task) that fails because the server is unreachable is
kept in a local queue (persisted with its client request ID) and sent after the next
successful sync. The Agent tab shows queued starts as "Wartet auf Verbindung", and a hint when
the Mac has not answered for more than an hour.

### 6. Access from anywhere over Tailscale

- Server: the desktop listener also accepts a request when both the peer and the local
  address are Tailscale addresses (`100.64.0.0/10`, `fd7a:115c:a1e0::/48`). Everything else
  stays "local network only". Same certificate, no new pairing.
- Server: the desktop process finds its Tailscale IPv4 address (interface scan, refreshed
  every minute) and `GET /api/v1/connection` (authenticated) answers
  `{"remote_url": "https://<tailscale-ip>:9847"}` or an empty value.
- App: after a sync it stores `remote_url` in the server session. Requests try the LAN
  address; on a connection-level failure they retry once on the remote address (its own
  pinned trust with the same fingerprint) and remember which address answered.
- iPhone: the Tailscale app must be connected. iOS allows one VPN at a time.

### 7. Context for the agent

The first round's objective names the State reminder ID and tells the agent to read the
reminder with the State MCP (`get_reminder`) for comments and history, and to record its
result with `add_comment`. The description is already inline, so agents without the State
MCP still have the task. `get_reminder` becomes bounded by default: all comments (the context
the owner and agents wrote), the 3 latest past and 5 next occurrences, counts by status, and
the 20 latest history entries; `full: true` returns everything.

### 8. T3 without the desktop app

The adapter prefers the service binary `~/.t3/runtime/versions/<activeVersion>/t3`, then `t3`
on `PATH`, then `~/.t3/bin/t3`.

### 9. Mac mini backup: not built now

The State server runs on the MacBook. When the MacBook is off, no runner anywhere gets work,
so a backup T3 on the Mac mini would not help in that case, and the T3 CLI cannot reach a
linked environment. The documented path: run the State server on an always-on machine (Mac
mini or the VPS deploy kit) and pair a second runner there. Until then the queue and catch-up
of decision 4 make sure nothing is lost.

## Testing

Go unit tests for catalog parsing, harness-models endpoint, model validation, contract hash
stability, scheduled sessions, coalescing, idempotency, Tailscale address filter, connection
endpoint, bounded `get_reminder`. iOS unit tests for decoding and the queued start. Live:
runner catalog report against the real T3 caches, one real T3 round with an explicit model,
the simulator reaching the server over the Tailscale address.

## Sparring (Haiku 5.5)

- Taken: no silent loss when coalescing (decision 4 lists skipped dates), explicit model on
  assignment instead of a history-dependent fallback (decision 2), an explicit "Nur T3 Code"
  switch (decision 3), a visible hint when the Mac has been unreachable (decision 5), keep all
  comments in the bounded `get_reminder` (decision 7).
- Rejected: binding a separate listener to the Tailscale interface and `tailscale cert`. The
  interface name changes on macOS, a forged source address cannot complete a TCP handshake
  because replies to the tailnet range go into the tunnel, and every request still needs the
  pinned certificate and a bearer token. `tailscale cert` would need MagicDNS and HTTPS
  certificates enabled in the tailnet.
