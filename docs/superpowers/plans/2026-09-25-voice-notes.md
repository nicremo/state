# Voice notes: recording screen, bullet notes, dictionary

> **For agentic workers:** Steps use checkbox (`- [ ]`) syntax. Executed inline with TDD in one session, branch `feat/voice-notes-dictionary`.

**Goal:** Voice notes show a compact recording row that opens a recording screen with player and live transcript highlighting, their note body is always condensed bullet points, AI texts never contain dashes, and a server-side dictionary corrects transcripts.

**Architecture:** The Go server keeps doing all AI work. Speech-to-text now asks OpenRouter for `verbose_json` segments and stores them with the raw and the corrected transcript on the attachment. A single owner dictionary lives next to the AI settings; `always` corrections run deterministically after transcription, `context` corrections and the word list go to the notes agent. The agent's voice-note result is checked on the server (list form, not a copy of the transcript) with one follow-up request. The app renders one compact row per recording and a new recording screen that plays all parts as one timeline.

**Tech Stack:** Go 1.24 (PocketBase/SQLite, MCP go-sdk), SwiftUI for iOS 18 and macOS 15 (GRDB, AVFoundation), OpenRouter.

**Spec:** Owner requirements, kept locally (not in the repository, it contains personal data): `docs/local/2026-09-25-voice-notes-dictionary.md`. Background: [`../../ai-managed-notes-review.md`](../../ai-managed-notes-review.md) section 8.

## Global Constraints

- The OpenRouter key is never read, printed or committed. It stays in `<data>/state_secrets/openrouter.key`.
- Offline sync, conflict copies, in-flight replay and runner filtering from stage A stay unchanged.
- AI data stays outside the note revision; only the empty document of a photo or voice note is written as a revisioned update.
- Originals (recordings, photos) are never changed. The raw transcript is kept next to the corrected one.
- No dashes (U+2013, U+2014) in code comments, UI texts, docs or AI output. Correct umlauts. German in `Localizable.xcstrings`.
- The seed dictionary contains personal data and never enters the repository: it is loaded at runtime from a local file or typed in the app.
- Only the owner and the owner's devices change the dictionary; agents read it (MCP, `statectl`). Runners have no access.
- No AI attribution in commits, PRs, code or docs. Git in English.

## Decisions

| # | Decision | Why |
| --- | --- | --- |
| D1 | One compact row for the whole recording, all parts in one screen and one timeline | Parts are technical 5 minute splits of one recording, the owner thinks of one recording |
| D2 | Segments stored per attachment in milliseconds (`start_ms`, `end_ms`, `text`); the app offsets them by the durations of earlier parts | Keeps server data per file, the timeline is a view concern |
| D3 | Dictionary in its own table `state_notes_dictionary` (one row, `revision`), full replace via `PUT /api/v1/notes-ai/dictionary` with `expected_revision` | Small, owner-wide, audited; the revision prevents two devices overwriting each other |
| D4 | Seed: when no dictionary exists yet, the server imports `<data>/notes-dictionary-seed.txt` (or `STATE_NOTES_DICTIONARY_SEED_FILE`) once, actor `notes-agent`. The app can import the same text format | The owner's list never enters the public code; the first start fills it |
| D5 | `always` corrections: case-insensitive, whole words only (letters and digits count as word characters, Unicode aware), longest first, one left to right pass, applied to transcript and segments. `context` corrections and words go to the agent, at most 200 entries | "Note" must never become "Node" inside "Schulnote"; ambiguous words need the context the model sees |
| D6 | Voice note check: every content line is a list item, heading or table row, no placeholder heading, and at most 50 percent of the body's 5-word sequences appear in the transcript. One follow-up request in the same conversation; if it still fails, the document is not written and the note is `needs_review` with a message | A prompt alone did not stop the 1:1 copy in the screenshot |
| D7 | Dash removal in `state.ApplyNoteAgentOutcomeFrom` for title, summary, document, relation reasons and proposals: digit ranges get a hyphen, a leading list dash becomes `- `, the first spaced dash in a title becomes `: `, other spaced dashes become `, ` | One deterministic place for every agent path |
| D8 | `verbose_json` with `timestamp_granularities: ["segment"]`; a 400 answer retries once without them | Not every provider supports it, the transcript matters more than the highlighting |
| D9 | Out of scope: provider vocabulary hints (OpenRouter does not pass them on uniformly) and re-correcting existing transcripts (marked optional in the spec) | Keeps the change reviewable |

## File Structure

Server:
- Create `internal/state/dictionary.go`: `NotesDictionary`, `DictionaryCorrection`, validation, `ApplyDictionary`, `ParseDictionaryText`, service methods, audit action.
- Create `internal/state/dashes.go`: `RemoveDashes(text string, title bool) string`.
- Create `internal/state/voice_check.go`: `VoiceDocumentProblem(document string, transcript string) string`.
- Modify `internal/state/notes_ai.go`: attachment fields `RawText`, `Segments`; `NoteAttachmentText` carries both; dash removal in outcome acceptance; repository interface.
- Modify `internal/state/memory_notes_ai.go`, `internal/store/notes_ai_repository.go`: dictionary storage.
- Modify `internal/notesai/openrouter.go`: `TranscribeRequest.Segments`, `Transcript.Segments`, retry without `verbose_json`.
- Modify `internal/notesai/worker.go`: apply dictionary, store segments and raw text, pass dictionary to the agent.
- Modify `internal/notesai/agent.go`: prompt for voice notes and dashes, dictionary section, voice check with one follow-up.
- Modify `internal/notesai/fakeopenrouter/fake.go`: transcripts with segments.
- Modify `internal/api/notes_ai_handler.go`, `internal/api/handler.go`: `GET/PUT /api/v1/notes-ai/dictionary`.
- Modify `internal/mcpserver/notes_ai_tools.go`: read-only `get_notes_dictionary` (23 tools).
- Modify `internal/statectl/note.go`, `cmd/statectl/note_ai.go`, `cmd/statectl/note.go`: `statectl note dictionary`, segments in `note processing`.
- Modify `cmd/state-server/main.go`: seed import at start.
- Modify `openapi/state-v1.yaml`, `README.md`, `DOCUMENTATION.md`, `docs/ai-managed-notes-review.md` (new section 9).

App:
- Modify `ios/State/Sources/Models/NoteAI.swift`: `TranscriptSegment`, attachment fields, `NotesDictionary`.
- Create `ios/State/Sources/Utilities/RecordingTimeline.swift`: parts, total duration, global segments, lookup of the active segment, mapping global time to part and offset.
- Create `ios/State/Sources/Utilities/RecordingPlayer.swift`: `@Observable` player over all parts with play, pause, seek, skip 15 s.
- Create `ios/State/Sources/UI/RecordingDetailView.swift`: the recording screen.
- Create `ios/State/Sources/UI/NotesDictionaryView.swift`: dictionary editor with import.
- Modify `ios/State/Sources/UI/NoteAttachmentsView.swift`: compact row, link to the screen, dictionary link in settings.
- Modify `ios/State/Sources/Networking/APIClient.swift`, `ios/State/Sources/App/AppModel.swift`: dictionary calls.
- Modify `ios/State/Resources/Localizable.xcstrings`.
- Tests: `ios/StateTests/RecordingTimelineTests.swift`, `ios/StateTests/NotesDictionaryTests.swift`.

## Tasks

### Task 1: Dash removal (server)

**Files:** Create `internal/state/dashes.go`, `internal/state/dashes_test.go`; modify `internal/state/notes_ai.go` (`ApplyNoteAgentOutcomeFrom`, `acceptedRelations`, `acceptedProposals`).

**Interfaces:** Produces `func RemoveDashes(text string, title bool) string`.

- [ ] Test table: `"Nerviger Tag \u2013 Cloud.md angepasst"` title → `"Nerviger Tag: Cloud.md angepasst"`; prose `"Heute \u2013 wie immer \u2013 nervig"` → `"Heute, wie immer, nervig"`; `"10\u201312 Uhr"` → `"10-12 Uhr"`; line `"\u2013 Milch"` → `"- Milch"`; `"Ende \u2014"` → `"Ende"`; hyphens and Markdown tables unchanged.
- [ ] Test `TestApplyOutcomeRemovesDashes`: outcome with dashes in title, summary, document, relation reason, proposal title, description and reason; stored values contain neither U+2013 nor U+2014.
- [ ] Run `go test ./internal/state -run 'Dash' -v`, expect FAIL.
- [ ] Implement, run again, expect PASS. Commit `feat: remove dashes from notes AI output`.

### Task 2: Dictionary domain and storage (server)

**Files:** Create `internal/state/dictionary.go`, `internal/state/dictionary_test.go`; modify `internal/state/notes_ai.go` (repository interface), `internal/state/memory_notes_ai.go`, `internal/store/notes_ai_repository.go`, `internal/store/notes_ai_test.go`.

**Interfaces:**
```go
type DictionaryMode string // "always" | "context"
type DictionaryCorrection struct { From string `json:"from"`; To string `json:"to"`; Mode DictionaryMode `json:"mode"` }
type NotesDictionary struct { Words []string `json:"words"`; Corrections []DictionaryCorrection `json:"corrections"`; Revision int64 `json:"revision"`; UpdatedAt time.Time `json:"updated_at"` }
type UpdateNotesDictionaryInput struct { Words []string; Corrections []DictionaryCorrection; ExpectedRevision *int64 }
func (service *Service) GetNotesDictionary(ctx) (NotesDictionary, error)
func (service *Service) UpdateNotesDictionary(ctx, actor, input) (NotesDictionary, error) // owner and devices
func (service *Service) SeedNotesDictionary(ctx, text string) (bool, error)             // only when none exists
func ParseDictionaryText(text string) ([]string, []DictionaryCorrection, error)
func ApplyDictionary(text string, corrections []DictionaryCorrection) string            // always entries only
repository: GetNotesDictionary(ctx) (NotesDictionary, bool, error); SaveNotesDictionary(ctx, NotesDictionary, AuditEvent) error
audit action: notes_ai.dictionary_updated
```
Limits: 500 words, 500 corrections, 80 runes per entry, no control characters, no empty `from` or `to`, duplicates (case-insensitive) merged.

Seed text format, one entry per line, `#` comments:
```
word: Supabase
always: ZEVDISK -> sevDesk
context: Note -> Node
```

- [ ] Tests: `ApplyDictionary` replaces `ZEVDISK` and `zevdisk` with `sevDesk`; `Cloud.md` → `CLAUDE.md` and `Cloud Code` → `Claude Code` (longest first, not `Claude Code` via `Cloud`); an `always` entry `Note -> Node` leaves `Schulnote` and `Notebook` unchanged but replaces `Note` in `die Note ist`; a `context` entry never replaces; replaced text is not replaced again; umlauts count as word characters (`Wörsel` inside `Wörselchen` stays).
- [ ] Tests: `UpdateNotesDictionary` rejects agent and runner (`ErrForbidden`), accepts owner and device, writes one audit event, increments revision, refuses a stale `expected_revision` with `ErrRevisionConflict`, validates limits. `SeedNotesDictionary` imports once and returns false the second time. `ParseDictionaryText` reads the three line kinds and rejects unknown ones.
- [ ] Store test: dictionary round trip with PocketBase repository and audit chain verification.
- [ ] Run, FAIL; implement; run, PASS. Commit `feat: owner dictionary for voice notes`.

### Task 3: Transcripts with segments, raw text and corrections (server)

**Files:** Modify `internal/notesai/openrouter.go`, `internal/notesai/openrouter_test.go`, `internal/notesai/worker.go`, `internal/notesai/worker_test.go`, `internal/notesai/fakeopenrouter/fake.go`, `internal/state/notes_ai.go`, `internal/store/notes_ai_repository.go`.

**Interfaces:**
```go
type TranscriptSegment struct { StartMS int64 `json:"start_ms"`; EndMS int64 `json:"end_ms"`; Text string `json:"text"` } // package state
NoteAttachment.RawText string `json:"raw_text,omitempty"`; NoteAttachment.Segments []TranscriptSegment `json:"segments,omitempty"`
NoteAttachmentText.RawText, NoteAttachmentText.Segments
notesai.TranscribeRequest.Segments bool; notesai.Transcript.Segments []struct{ Start, End float64; Text string }
fakeopenrouter.TranscriptWithSegments(text string, segments [][3]any, cost float64) Reply
```
- [ ] Tests: request body contains `response_format: verbose_json` and `timestamp_granularities: ["segment"]`; a 400 reply leads to one retry without them; segments convert to milliseconds; the worker stores raw text, corrected text and corrected segments; without segments the attachment has none and processing still succeeds.
- [ ] Run, FAIL; implement; run, PASS. Commit `feat: transcript segments and dictionary corrections`.

### Task 4: Agent: bullet notes, dictionary, one follow-up (server)

**Files:** Create `internal/state/voice_check.go`, `internal/state/voice_check_test.go`; modify `internal/notesai/agent.go`, `internal/notesai/worker.go`, `internal/notesai/worker_test.go`.

**Interfaces:** `func VoiceDocumentProblem(document string, transcript string) string` (empty when fine). `AgentInput.Dictionary state.NotesDictionary`.

- [ ] Tests for the check: the screenshot case (transcript copied as prose under `## Sprachnotiz`) fails; the transcript split into bullets fails; condensed bullets pass; a checklist passes; a placeholder heading alone fails.
- [ ] Worker tests with the fake: first `submit_result` copies the transcript, the server sends one follow-up message naming the problem, the second result is stored; if the second one also fails, the note is `needs_review`, its document stays empty and the error says why; the agent's user message contains the context corrections and words and not the `always` entries; photo notes are never checked.
- [ ] Run, FAIL; implement; run, PASS. Commit `feat: voice notes become bullet points`.

### Task 5: REST, MCP, statectl, seed at start (server)

**Files:** Modify `internal/api/handler.go`, `internal/api/notes_ai_handler.go`, `internal/api/notes_ai_key_test.go` (or new `notes_ai_dictionary_test.go`), `internal/mcpserver/notes_ai_tools.go`, `internal/mcpserver/server_test.go`, `internal/mcpserver/notes_ai_tools_test.go`, `internal/statectl/note.go`, `cmd/statectl/note.go`, `cmd/statectl/note_ai.go`, `cmd/statectl/note_test.go`, `cmd/state-server/main.go`, `openapi/state-v1.yaml`.

- [ ] REST tests: `GET` as owner, device and agent returns the dictionary; `PUT` as agent is 403, as owner 200 with audit, stale revision 409, invalid entries 400.
- [ ] MCP test: `get_notes_dictionary` is listed and read-only, returns words and corrections; tool count 23.
- [ ] statectl test: `statectl note dictionary --profile codex` prints words and corrections, `--json` passes the raw result; `note processing` prints segment count.
- [ ] Seed test: a server started with a seed file imports it once.
- [ ] Run, FAIL; implement; run, PASS. Commit `feat: dictionary over REST, MCP and statectl`.

### Task 6: App models, timeline and player (app)

**Files:** Modify `ios/State/Sources/Models/NoteAI.swift`; create `ios/State/Sources/Utilities/RecordingTimeline.swift`, `ios/State/Sources/Utilities/RecordingPlayer.swift`, `ios/StateTests/RecordingTimelineTests.swift`.

**Interfaces:**
```swift
struct TranscriptSegment: Codable, Hashable, Sendable { var startMs: Int64; var endMs: Int64; var text: String }
struct RecordingTimeline { init(parts: [NoteAttachment]); var duration: TimeInterval; var segments: [Segment]; func segmentIndex(at: TimeInterval) -> Int?; func location(of: TimeInterval) -> (part: Int, offset: TimeInterval); var hasTimestamps: Bool; var transcript: String }
@Observable final class RecordingPlayer { var isPlaying: Bool; var currentTime: TimeInterval; func load(_ data: [Data]); func play(); func pause(); func seek(to: TimeInterval); func skip(by: TimeInterval) }
```
- [ ] Tests: two parts of 10 s and 5 s give 15 s; a segment of part 2 at 1 to 3 s maps to 11 to 13 s; `segmentIndex(at: 12)` finds it; times before, between and after segments; `location(of: 12)` is part 1 at 2 s; no segments gives `hasTimestamps == false` and the full transcript; decoding `start_ms` from server JSON.
- [ ] Run, FAIL; implement; run, PASS. Commit `feat: recording timeline in the app`.

### Task 7: Compact row and recording screen (app)

**Files:** Create `ios/State/Sources/UI/RecordingDetailView.swift`; modify `ios/State/Sources/UI/NoteAttachmentsView.swift`, `ios/State/Resources/Localizable.xcstrings`.

- [ ] Row: play button, "Aufnahme", total duration, `info.circle` button, no transcript; tapping the row or the button opens the screen (sheet with its own navigation on every platform, so it works in the split layout too).
- [ ] Screen: title "Aufnahme", scrubber (`Slider`, adjustable for VoiceOver), elapsed and total time, back 15 s, play and pause, forward 15 s, transcript as tappable sentences with the active one highlighted and scrolled into view; without timestamps the transcript as selectable text and a quiet hint; copy action.
- [ ] Build and check in the simulator with MobAI. Commit `feat: recording screen with live transcript`.

### Task 8: Dictionary settings (app)

**Files:** Create `ios/State/Sources/UI/NotesDictionaryView.swift`, `ios/StateTests/NotesDictionaryTests.swift`; modify `ios/State/Sources/Networking/APIClient.swift`, `ios/State/Sources/App/AppModel.swift`, `ios/State/Sources/UI/NoteAttachmentsView.swift`, `ios/State/Resources/Localizable.xcstrings`.

- [ ] Tests: the Swift parser of the import text matches the server format; encoding of the `PUT` body with `expected_revision`.
- [ ] Screen: sections "Wörter" and "Falsch gehört" (from, to, mode picker "Immer" or "Nach Kontext"), add, edit, swipe to delete, import from pasted text, save with conflict message.
- [ ] Commit `feat: dictionary settings in the app`.

### Task 9: Docs, review, E2E, live test, release

- [ ] `README.md`, `DOCUMENTATION.md`, OpenAPI, review doc section 9, state-sync skill (23 tools, `note dictionary`).
- [ ] Full checks: `go vet ./...`, `test -z "$(gofmt -l cmd internal)"`, `go test -race ./...`, iOS `StateTests`, `StateMac` build.
- [ ] Independent code review by a subagent; every confirmed finding fixed with a regression test.
- [ ] MobAI on the iPhone simulator against a local server with the fake: compact row, recording screen highlighting while playing, tap to jump, dictionary editing, bullet note; screenshots.
- [ ] Live test on a local test server with a temporary copy of the owner's key (removed afterwards): spoken text with "Cloud.md" and "ZEVDISK" becomes "CLAUDE.md" and "sevDesk", bullet note, segments present or the reason they are missing.
- [ ] PR, merge, backup and deploy to the Mac Server with the seed file, `statectl doctor`, TestFlight iOS and Mac.
