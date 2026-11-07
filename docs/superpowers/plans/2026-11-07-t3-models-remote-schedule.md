# T3 models, remote access and scheduled sessions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Executed inline (owner's choice).

**Goal:** Every agent task runs through T3 Code with a chosen provider and model, due reminders start T3 sessions on their own and catch up after downtime, the iPhone reaches the Mac from anywhere over Tailscale, and agents get the reminder context through the State MCP.

**Architecture:** The runner reads T3's provider caches and reports a model catalog to the server. `AgentModel {provider, model}` travels from the reminder or the start sheet through the session and the task contract to `t3 thread start --provider --model`. The scheduler turns the newest due occurrence of a reminder with a `t3-code` policy into a session. The desktop server also admits Tailscale peers and publishes its Tailscale URL; the app falls back to it when the LAN address fails.

**Tech Stack:** Go (server, runner, MCP), SwiftUI iOS 18+, T3 Code 0.1.7 CLI and caches, Tailscale.

**Spec:** `docs/superpowers/specs/2026-11-07-t3-models-remote-schedule-design.md`

## Global Constraints

- German UI copy with umlauts, no em or en dashes; code comments English.
- Contract hash of existing contracts must not change (new fields omitempty, at the end).
- No data reset; the desktop certificate is never regenerated (paired iPhones keep working).
- Commits with the repo identity, no AI attribution; one branch, PR, merge after green checks.
- Verification: `go test -count=1 ./...` (store test known red on main), `go vet ./...`, `gofmt -l cmd internal`, iOS `StateTests`, StateMac build, live T3 round, simulator over Tailscale.

---

### Task 1: AgentModel on reminder, session and contract

**Files:** `internal/state/agent_model.go` (new), `internal/state/models.go`, `internal/state/service.go`, `internal/state/agent_sessions.go`, `internal/state/execution_models.go`, `openapi/state-v1.yaml`; tests `internal/state/agent_model_test.go`.

**Interfaces:**
- Produces: `type AgentModel struct { Provider string \`json:"provider"\`; Model string \`json:"model"\` }`, `func (model AgentModel) Valid() bool`; `Reminder.ExecutionModel *AgentModel` (`execution_model`), `CreateReminderInput.ExecutionModel *AgentModel`, `UpdateReminderInput.ExecutionModel **AgentModel`; `StartAgentSessionInput.Model *AgentModel` (`model`); `AgentSession.Model *AgentModel` (`model`); `TaskContract.ModelProvider`, `TaskContract.Model` (`model_provider`, `model`, omitempty).

- [ ] Tests: validation (provider pattern `^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`, model 1 to 200 runes without control characters), update sets and clears, session start copies the model into session and first contract, contract hash of a contract without model equals the old hash.
- [ ] Implement, `go test ./internal/state`, commit `feat: agent model on reminders, sessions and task contracts`.

### Task 2: Harness model catalog on runners

**Files:** `internal/state/execution_models.go` (`Runner.HarnessModels []HarnessModel`, `ReportHarnessModelsInput`), `internal/state/execution.go` (`ReportHarnessModels`), `internal/api/handler.go` (`PUT /api/v1/runner/harness-models`), `openapi/state-v1.yaml`; tests in `internal/state` and `internal/api`.

**Interfaces:** `type HarnessModel struct { Provider, ProviderName, Model, ModelName string; Default bool }` (json snake case); `func (service *Service) ReportHarnessModels(ctx, actor Actor, input ReportHarnessModelsInput) (Runner, error)` writes only when the list changed; at most 200 entries.

- [ ] Tests, implement, commit `feat: runners report their T3 model catalog`.

### Task 3: Scheduled T3 sessions with catch-up

**Files:** `internal/state/execution.go` (`MaterializeEligibleRuns`), `internal/state/agent_sessions.go` (`scheduledSessionObjective`, system-started session); tests `internal/state/scheduled_sessions_test.go`.

- [ ] Tests: a due occurrence of a reminder with a `t3-code` policy creates one session with the system actor, first round carries occurrence ID and the reminder's model; a second cycle creates nothing; three due occurrences create one session for the newest and its objective lists the two older dates; non-T3 policies keep creating bare runs (only the newest per reminder).
- [ ] Implement, commit `feat: due reminders with a T3 policy start agent sessions and catch up`.

### Task 4: Agent context through the State MCP

**Files:** `internal/state/agent_sessions.go` (`firstRoundObjective` names reminder ID, State MCP use), `internal/mcpserver/server.go` (bounded `get_reminder`, `full`); tests.

- [ ] Tests: objective contains `State reminder ID: <id>` and `get_reminder`; bounded detail returns all comments, at most 3 past and 5 upcoming occurrences, counts, 20 history entries; `full: true` returns everything.
- [ ] Implement, commit `feat: agents get the reminder context through the State MCP`.

### Task 5: Tailscale access

**Files:** `cmd/state-server/desktop.go` (filter, address watcher), `internal/api/handler.go` (`GET /api/v1/connection` via injected func), `cmd/state-server/main.go` (application config), tests `cmd/state-server/desktop_test.go`.

**Interfaces:** `func tailnetAddress(addr netip.Addr) bool`; `applicationConfig.remoteURL func() string`; response `{"remote_url": "..."}`.

- [ ] Tests: LAN, loopback, link-local allowed; Tailscale peer to Tailscale local allowed; Tailscale peer to LAN local rejected; public rejected; connection endpoint needs auth and returns the URL.
- [ ] Implement, commit `feat: reach the desktop server over Tailscale`.

### Task 6: Runner: service binary, catalog, explicit model

**Files:** `internal/runner/t3_adapter.go`, `internal/runner/t3_catalog.go` (new), `internal/runner/run.go` / `cmd/state-runner/main.go` (catalog report loop), `internal/runner/client.go`; tests with fixture caches.

- [ ] Tests: binary order (service binary, PATH, launcher); catalog from fixture caches drops disabled, non-ready and hidden models; contract model becomes `--provider/--model`; report sends only on change.
- [ ] Implement, live check against real caches, commit `feat: runner uses the T3 service binary and reports the model catalog`.

### Task 7: iOS data and transport

**Files:** `Models/Models.swift`, `Models/AgentSession.swift`, `Networking/APIClient.swift`, `Session/SessionRepository.swift`, `App/AppModel.swift`, `shared/LocalTransport/LocalServerTrust.swift` (unchanged API); tests `StateTests`.

- [ ] `AgentModel`, `HarnessModel`, `Runner.harnessModels`, `Reminder.executionModel` (create, update, conflict paths), `AgentSession.model`.
- [ ] `ServerSession.remoteURL`; `APIClient` with primary and remote base, retry once on connection errors, remembers the working base; `connection()` call after sync stores the remote URL.
- [ ] Queued starts: persisted list, flushed after a successful sync.
- [ ] Tests for decoding, fallback order, queue; commit `feat: iOS models, Tailscale fallback and queued agent starts`.

### Task 8: iOS UI

**Files:** `UI/StartAgentSessionSheet.swift`, `UI/ReminderEditorView.swift`, `UI/AgentSessionsView.swift`, `UI/AgentSessionDetailView.swift`, `UI/SettingsView.swift`, new `UI/AgentModelPicker.swift`, `Resources/Localizable.xcstrings`.

- [ ] "Nur T3 Code" switch; Projekt and Modell pickers in the start sheet and the reminder editor (model preselected on assignment); model shown on sessions; queued starts and the unreachable hint in the Agent tab.
- [ ] Simulator check with MobAI; commit `feat: choose project and model for T3 tasks in the app`.

### Task 9: Docs, deploy, TestFlight

- [ ] `docs/runner-service.md`, `DOCUMENTATION.md`, `docs/LOCAL_MAC_CONNECTION.md` (Tailscale), `README.md`.
- [ ] Full verification, PR, merge, rebuild server app and runner from `main`, assign the Karla reminder its T3 policy with Sonnet 5.5 only if the owner asked (he did: "das soll über T3 Code und dann über Sonnet 5.5 passieren"), TestFlight build with a 12-digit build number, readback.
