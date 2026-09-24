# Notes Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** State bekommt Notizen: flache Liste, Markdown-Dokument, Titel und Zusammenfassung mit Herkunft, signierte Audit-Kette, REST, Sync in die App, MCP-Werkzeuge und `statectl note`. Ohne KI, ohne Medien.

**Architecture:** Notizen sind eine eigene Entität neben Erinnerungen. Sie nutzen dieselbe Service-, Repository-, Audit- und Idempotenz-Infrastruktur. Audit-Ereignisse tragen ein neues Feld `note_id` (omitempty, bestehende Hashes bleiben gültig). Die App synchronisiert Notizen über den bestehenden Change-Feed und die bestehende Mutations-Warteschlange.

**Tech Stack:** Go 1.25, PocketBase/SQLite mit FTS5, modelcontextprotocol/go-sdk, SwiftUI iOS 18 und macOS 15, GRDB.

**Spec:** `docs/ai-managed-notes.md` mit den verbindlichen Abweichungen aus `docs/ai-managed-notes-review.md`.

## Global Constraints

- Das kanonische Notizdokument ist Markdown (CommonMark plus `- [ ]`, `- [x]`, `---`). Maximal 262144 Byte.
- Titel: höchstens 200 Zeichen. Fehlt er, wird er aus der ersten nicht leeren Zeile abgeleitet (`title_source = "derived"`).
- Zusammenfassung: höchstens 280 Zeichen. Fehlt sie, wird sie aus dem Text nach der Titelzeile abgeleitet, höchstens 160 Zeichen (`summary_source = "derived"`).
- Ein vom Nutzer gesetzter Titel oder eine gesetzte Zusammenfassung wird nie automatisch überschrieben.
- Harness-Akteure dürfen Notizen anlegen und ändern, nicht archivieren. Runner dürfen nichts an Notizen tun.
- Nie hart löschen. Archivieren über `archived: true`, wiederherstellen über `archived: false`.
- Bestehendes Verhalten von Erinnerungen, Runner, MCP und CLI bleibt unverändert. `search_reminders` findet keine Notizen.
- Texte in der App: Englisch als Quelle, Deutsch in `Localizable.xcstrings`. Keine Gedankenstriche, korrekte Umlaute.
- Commits auf Englisch, ohne KI-Attribution.

## Datei-Landkarte

| Datei | Aufgabe |
| --- | --- |
| `ios/State/Sources/UI/StateTheme.swift` | neuer Pill-Stil (Task 0) |
| `internal/state/notes.go` | Modelle, Eingaben, Ableitung, Service-Methoden |
| `internal/state/notes_test.go` | Service-Tests gegen `MemoryRepository` |
| `internal/state/models.go` | `AuditEvent.NoteID`, Audit-Aktionen |
| `internal/state/service.go` | Repository-Interface erweitern |
| `internal/state/memory_repository.go` | Notiz-Methoden im Speicher |
| `internal/store/notes_repository.go` | Tabellen, FTS, Repository-Methoden |
| `internal/store/notes_test.go` | Store-Tests inklusive Audit-Kette |
| `internal/api/notes_handler.go`, `internal/api/notes_handler_test.go` | REST |
| `internal/mcpserver/notes_tools.go`, `internal/mcpserver/notes_tools_test.go` | vier MCP-Werkzeuge |
| `internal/statectl/note.go`, `internal/statectl/note_test.go` | `NoteService` über `ToolCaller` |
| `cmd/statectl/note.go`, `cmd/statectl/note_test.go` | `statectl note` |
| `ios/State/Sources/Models/Note.swift` | Modell und Entwurf |
| `ios/State/Sources/Database/StateDatabase.swift` | Tabelle `notes`, Lesen und Schreiben |
| `ios/State/Sources/Networking/APIClient.swift` | `getNote` |
| `ios/State/Sources/Sync/SyncEngine.swift` | Notiz-Ereignisse im Pull, angelegte Notiz nach Push |
| `ios/State/Sources/App/AppModel.swift` | `notes`, anlegen, ändern, archivieren, Demo |
| `ios/State/Sources/UI/MarkdownBlocks.swift`, `MarkdownView.swift` | Checklisten und Trennlinien |
| `ios/State/Sources/UI/NotesView.swift` | Liste, Suche, Plus |
| `ios/State/Sources/UI/NoteDetailView.swift` | Ansicht, Bearbeiten, Formatleiste |
| `ios/State/Sources/UI/MainTabView.swift`, `SplitRootView.swift` | Tab und Seitenleiste |
| `openapi/state-v1.yaml`, `DOCUMENTATION.md`, `README.md` | Vertrag und Doku |

---

### Task 0: Lesbare Pill-Buttons im Dunkelmodus

**Ursache:** `.buttonStyle(.borderedProminent)` nimmt `accent` als Fläche. Im Dunkelmodus ist `accent` fast weiß (`#F3F5F7`), das System setzt die Schrift trotzdem weiß. `MainTabView.swift:220` ("New reminder") und `ActivityView.swift:121`.

**Files:**
- Modify: `ios/State/Sources/UI/StateTheme.swift`
- Modify: `ios/State/Sources/UI/MainTabView.swift:220`, `ios/State/Sources/UI/ActivityView.swift:121`

**Interfaces:**
- Produces: `ButtonStyle.statePill` (`StatePillButtonStyle`)

- [ ] **Step 1: Stil ergänzen**

```swift
/// A compact filled action for empty states: the primary style's ink and
/// onAccent pairing, sized to its label. `.borderedProminent` would keep a
/// white label on the near-white dark-mode ink and make it unreadable.
struct StatePillButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.subheadline.weight(.semibold))
            .foregroundStyle(StateTheme.onAccent)
            .padding(.horizontal, StateTheme.Space.block)
            .frame(minHeight: 36)
            .background(StateTheme.accent, in: Capsule())
            .contentShape(Capsule())
            .opacity(isEnabled ? (configuration.isPressed ? 0.8 : 1) : 0.25)
            .animation(.smooth(duration: 0.16), value: configuration.isPressed)
    }
}

extension ButtonStyle where Self == StatePillButtonStyle {
    static var statePill: StatePillButtonStyle { StatePillButtonStyle() }
}
```

- [ ] **Step 2:** Beide `.buttonStyle(.borderedProminent)` durch `.buttonStyle(.statePill)` ersetzen.
- [ ] **Step 3:** Render-Nachweis im Dunkelmodus (ImageRenderer-Wegwerftest oder Simulator-Screenshot mit `-stateDemo`), dann `git commit -m "fix: keep empty-state actions readable in dark mode"`.

---

### Task 1: Domäne und Service

**Files:**
- Create: `internal/state/notes.go`, `internal/state/notes_test.go`
- Modify: `internal/state/models.go` (AuditEvent, Aktionen), `internal/state/service.go` (Interface), `internal/state/memory_repository.go`

**Interfaces:**
- Produces:

```go
type NoteFieldSource string
const (
	NoteFieldSourceUser    NoteFieldSource = "user"
	NoteFieldSourceDerived NoteFieldSource = "derived"
)

type Note struct {
	ID            string          `json:"id"`
	Title         string          `json:"title"`
	TitleSource   NoteFieldSource `json:"title_source"`
	Document      string          `json:"document"`
	PlainText     string          `json:"plain_text"`
	Summary       string          `json:"summary"`
	SummarySource NoteFieldSource `json:"summary_source"`
	Archived      bool            `json:"archived"`
	Revision      int64           `json:"revision"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type CreateNoteInput struct {
	Title, Document, Summary      string
	ClientTime                    *time.Time
	Source, SourceExcerpt         string
	ClientRequestID, CorrelationID string
} // JSON: title, document, summary, client_time, source, source_excerpt, client_request_id, correlation_id

type UpdateNoteInput struct {
	Title, Document, Summary *string
	Archived                 *bool
	ExpectedRevision         int64
	ClientTime               *time.Time
	Source, SourceExcerpt    string
	ClientRequestID, CorrelationID string
}

type NoteListOptions struct {
	Query           string
	IncludeArchived bool
	Limit           int
}

func (s *Service) CreateNote(ctx, actor Actor, input CreateNoteInput) (Note, error)
func (s *Service) UpdateNote(ctx, actor Actor, noteID string, input UpdateNoteInput) (Note, error)
func (s *Service) GetNote(ctx, noteID string) (Note, error)
func (s *Service) ListNotes(ctx, options NoteListOptions) ([]Note, error)
func (s *Service) ListNoteHistory(ctx, noteID string) ([]AuditEvent, error)

func DeriveNoteTitle(document string) string
func DeriveNoteSummary(document string) string
func NotePlainText(document string) string

// Repository additions
CreateNote(context.Context, Note, AuditEvent, string) (Note, error)
UpdateNote(context.Context, Note, int64, AuditEvent, string) (Note, error)
GetNote(context.Context, string) (Note, error)
ListNotes(context.Context, NoteListOptions) ([]Note, error)
ListNoteAuditEvents(context.Context, string) ([]AuditEvent, error)
```

`AuditEvent` bekommt `NoteID string \`json:"note_id,omitempty"\``. Aktionen: `note.created`, `note.updated`, `note.archived`, `note.restored`.

- [ ] **Step 1: Failing Tests** in `internal/state/notes_test.go`:

```go
func TestCreateNoteDerivesTitleAndSummaryFromTheDocument(t *testing.T)
// document "# Einkauf\n\nMilch und **Brot** holen.\n- [ ] Eier" ergibt
// Title "Einkauf", TitleSource derived, Summary "Milch und Brot holen. Eier",
// SummarySource derived, PlainText enthält "Brot" ohne Sternchen, Revision 1,
// genau ein Audit-Ereignis note.created mit NoteID und leerer ReminderID.

func TestCreateNoteKeepsAnExplicitTitle(t *testing.T)
// Title "Mein Titel" plus Dokument ergibt TitleSource user.

func TestCreateNoteRejectsEmptyNotesAndOversizedDocuments(t *testing.T)
// Titel und Dokument leer: ErrInvalidInput. Dokument 262145 Byte: ErrInvalidInput.

func TestCreateNoteIsIdempotentPerClientRequest(t *testing.T)
// zweimal dieselbe client_request_id: dieselbe ID, ein Ereignis.

func TestUpdateNoteRederivesOnlyDerivedFields(t *testing.T)
// Notiz mit user-Titel und derived-Zusammenfassung: neues Dokument ändert
// Summary, nicht Title. Leerer Titel setzt TitleSource wieder auf derived.

func TestUpdateNoteRejectsStaleRevision(t *testing.T)   // ErrRevisionConflict
func TestHarnessCannotArchiveNotes(t *testing.T)        // ErrForbidden, Owner darf, Aktion note.archived, danach note.restored
func TestRunnerCannotTouchNotes(t *testing.T)           // Create und Update: ErrForbidden
func TestListNotesOrdersByUpdateAndHidesArchived(t *testing.T)
func TestListNotesSearchesTitleAndPlainText(t *testing.T) // Query "brot" findet, "xyz" nicht (Speicher: strings.Contains auf lower)
```

- [ ] **Step 2:** `go test ./internal/state -run Note` schlägt fehl (Symbole fehlen).
- [ ] **Step 3: Implementierung** in `notes.go`:

```go
const (
	MaxNoteDocumentBytes = 262144
	MaxNoteTitleRunes    = 200
	MaxNoteSummaryRunes  = 280
	derivedSummaryRunes  = 160
)

// NotePlainText strips Markdown markers so search, MCP and CLI see readable text.
func NotePlainText(document string) string {
	lines := strings.Split(strings.ReplaceAll(document, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	inCode := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") { inCode = !inCode; continue }
		if inCode { out = append(out, raw); continue }
		if line == "---" || line == "***" { continue }
		line = strings.TrimLeft(line, "#")
		for _, prefix := range []string{"- [ ] ", "- [x] ", "- [X] ", "- ", "* ", "+ ", "> "} {
			if strings.HasPrefix(strings.TrimSpace(line), prefix) { line = strings.TrimPrefix(strings.TrimSpace(line), prefix); break }
		}
		line = orderedListMarker.ReplaceAllString(line, "")
		line = inlineMarkers.Replace(line)
		out = append(out, strings.TrimSpace(line))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
var orderedListMarker = regexp.MustCompile(`^\d{1,3}[.)] `)
var inlineMarkers = strings.NewReplacer("**", "", "__", "", "~~", "", "`", "")

func DeriveNoteTitle(document string) string   // erste nicht leere Zeile von NotePlainText, auf 200 Runen gekürzt
func DeriveNoteSummary(document string) string // restliche Zeilen mit " " verbunden, auf 160 Runen mit "…" gekürzt
```

Service-Regeln wie in den Global Constraints. `buildAuditEvent` bekommt die Note-ID über ein neues Hilfsfeld: nach dem Aufruf `event.NoteID = note.ID` setzen. `ChangedFields` bei Create: `archived, document, summary, title`. Bei Update nur die geänderten Felder, bei reinem Archivieren `archived` mit Aktion `note.archived` bzw. `note.restored`.

Memory-Repository: Maps `notes`, `requestNotes`, Audit über das bestehende `appendAuditEvent`, `ListNoteAuditEvents` filtert `auditChain` nach `NoteID`.

- [ ] **Step 4:** `go test ./internal/state` grün, `go vet ./...` sauber.
- [ ] **Step 5:** `git commit -m "feat: add notes to the state domain"`

---

### Task 2: PocketBase-Store

**Files:**
- Create: `internal/store/notes_repository.go`, `internal/store/notes_test.go`
- Modify: `internal/store/pocketbase_repository.go` (`ensureSchema` ruft `ensureNotesSchema`)

**Interfaces:**
- Consumes: Repository-Methoden aus Task 1.

Schema:

```sql
CREATE TABLE IF NOT EXISTS state_notes (
	id TEXT PRIMARY KEY,
	title TEXT NOT NULL,
	archived INTEGER NOT NULL DEFAULT 0 CHECK(archived IN (0, 1)),
	revision INTEGER NOT NULL CHECK(revision > 0),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	data_json TEXT NOT NULL CHECK(json_valid(data_json))
) STRICT;
CREATE INDEX IF NOT EXISTS state_notes_updated_idx ON state_notes(archived, updated_at DESC);
CREATE VIRTUAL TABLE IF NOT EXISTS state_note_search USING fts5(
	note_id UNINDEXED, content, tokenize = 'unicode61 remove_diacritics 2'
);
CREATE INDEX IF NOT EXISTS state_audit_events_note_idx
	ON state_audit_events(json_extract(event_json, '$.note_id'), sequence);
```

Kein `ALTER TABLE` an der Audit-Tabelle: die Historie einer Notiz wird über den Ausdrucksindex auf `event_json` gelesen. Notiz-Ereignisse schreiben **keinen** Eintrag in `state_search`.

- [ ] **Step 1: Failing Tests:** anlegen, lesen, idempotent, Update mit falscher Revision, Liste nach `updated_at`, Archiv ausgeblendet, FTS findet "Brot" in "Milch und **Brot**", `SearchReminders("Brot")` findet nichts, `ListNoteAuditEvents` liefert die Kette der Notiz, `VerifyAuditChain` bleibt grün nach Notiz-Ereignissen, bestehende Datenbank (Tabellen vor der Migration) öffnet sich erneut ohne Fehler.
- [ ] **Step 2:** rot.
- [ ] **Step 3:** Implementierung nach dem Muster von `CreateReminder` (Transaktion, Idempotenz mit `insertIdempotencyValue(..., "note", ...)`, `sealAuditEvent`, `insertAuditEvent`, `upsertNoteSearch`).
- [ ] **Step 4:** `GOTOOLCHAIN=local go test -race ./internal/store ./internal/state` grün.
- [ ] **Step 5:** `git commit -m "feat: store notes with full-text search and audit history"`

---

### Task 3: REST und Change-Feed

**Files:**
- Create: `internal/api/notes_handler.go`, `internal/api/notes_handler_test.go`
- Modify: `internal/api/handler.go` (Routen)

Routen:

```text
POST  /api/v1/notes                   owner, device, harness       201 Note
GET   /api/v1/notes?q=&include_archived=&limit=  owner, device, harness  200 {"notes": [...]}
GET   /api/v1/notes/{id}              owner, device, harness       200 Note
PATCH /api/v1/notes/{id}              owner, device, harness       200 Note (archived nur owner/device, sonst 403)
GET   /api/v1/notes/{id}/history      owner, device, harness       200 {"history": [...]}
```

Nach jeder Schreiboperation `handler.notifySync`. Fehler über `writeError` (409 bei Revisionskonflikt mit aktuellem Snapshot wie bei Erinnerungen).

- [ ] **Step 1: Failing Tests:** Owner legt an (201, `title_source`), Gerät listet, Harness ändert, Harness archiviert (403), Runner liest (403), Revisionskonflikt (409), `GET /api/v1/changes` enthält `note.created` mit `note_id`.
- [ ] **Step 2–4:** rot, implementieren, `go test ./internal/api` grün.
- [ ] **Step 5:** `git commit -m "feat: expose notes over the versioned REST API"`

---

### Task 4: MCP-Werkzeuge

**Files:**
- Create: `internal/mcpserver/notes_tools.go`, `internal/mcpserver/notes_tools_test.go`
- Modify: `internal/mcpserver/server.go` (`registerTools` ruft `registerNoteTools`), Instructions-Text um einen Satz zu Notizen ergänzen

Werkzeuge (alle über `reminderActor`, also ohne Runner):

| Name | Eingabe | Ausgabe |
| --- | --- | --- |
| `search_notes` | `query` (optional, leer = neueste), `limit` (max 50) | `{"notes": [...]}` ohne `document`, mit `summary` |
| `get_note` | `note_id` | `{"note": Note, "history": [...]}` |
| `create_note` | `title?`, `document`, `client_request_id`, `source_text`, `correlation_id?` | `{"stored": true, "note": Note}` |
| `update_note` | `note_id`, `expected_revision`, `title?`, `document?`, `summary?`, `client_request_id`, `source_text` | `{"stored": true, "note": Note}` |

- [ ] **Step 1: Failing Tests** mit der In-Memory-Sitzung: Werkzeugliste enthält 18 Namen; Create, Search, Get, Update im Durchlauf; Runner-Identität erhält `forbidden`.
- [ ] **Step 2–4:** rot, implementieren, grün. Bestehende Tests, die 14 Werkzeuge zählen, auf 18 heben.
- [ ] **Step 5:** `git commit -m "feat: give agents note tools over MCP"`

---

### Task 5: `statectl note`

**Files:**
- Create: `internal/statectl/note.go`, `internal/statectl/note_test.go`, `cmd/statectl/note.go`, `cmd/statectl/note_test.go`
- Modify: `cmd/statectl/main.go` (Befehl `note`, Usage-Text)

Befehle:

```text
statectl note list   --profile P [--query Q] [--limit N] [--json]
statectl note show   --profile P --id ID [--json]
statectl note create --profile P --source-text S [--title T] (--document-file F | -)  [--request-id R] [--json]
statectl note update --profile P --id ID --source-text S [--title T] [--summary S2] [--document-file F] [--json]
```

`update` holt die Revision selbst über `get_note`. Inhalte nur über `--document-file` (Pfad oder `-`), keine Inline-Dokumente. Ausgabe ohne `--json`: `stored note <id> "<title>"`, Liste `<id> "<title>"  <summary>`.

- [ ] **Step 1: Failing Tests:** Service gegen In-Memory-Server (anlegen, listen, zeigen, ändern mit automatischer Revision), CLI-Flag-Fehler ohne Netzwerk (fehlendes `--source-text`, fehlendes Dokument und Titel).
- [ ] **Step 2–4:** rot, implementieren, grün.
- [ ] **Step 5:** `git commit -m "feat: add statectl note commands"`

---

### Task 6: App-Daten und Sync

**Files:**
- Create: `ios/State/Sources/Models/Note.swift`, `ios/StateTests/NoteSyncTests.swift`
- Modify: `StateDatabase.swift` (Migration `v_notes`, `apply(note:)`, `notes(includeArchived:)`, `note(id:)`, `deleteNote(id:)`), `APIClient.swift` (`getNote(id:)`), `SyncEngine.swift`, `AppModel.swift`

Regeln:
- `Note` spiegelt das Server-JSON (snake_case über den bestehenden `StateJSON`-Decoder).
- Anlegen: vorläufige UUIDv7, lokal gespeichert, Mutation `POST /api/v1/notes`. Nach dem Push ersetzt die Server-Notiz die vorläufige (wie bei Erinnerungen).
- Ändern: lokales Update plus Mutation `PATCH /api/v1/notes/{id}` mit `expected_revision`. Ein 409 wird als Konflikt protokolliert und die Serverfassung übernommen.
- Pull: Ereignisse mit `note_id` sammeln, je Notiz `GET /api/v1/notes/{id}` und `apply(note:)`. Ereignisse ohne `reminder_id` und ohne `note_id` rücken weiter nur den Cursor vor.
- Demo: eine Beispielnotiz mit Überschrift, Absatz und Checkliste.

- [ ] **Step 1: Failing Tests:** Decoding einer Server-Notiz, Datenbank-Rundreise, Sortierung nach `updated_at`, archivierte ausgeblendet, lokale Suche über Titel, Zusammenfassung und Klartext.
- [ ] **Step 2–4:** rot, implementieren, `xcodebuild test -only-testing:StateTests` grün.
- [ ] **Step 5:** `git commit -m "feat: sync notes into the app cache"`

---

### Task 7: Notizen-Oberfläche

**Files:**
- Create: `ios/State/Sources/UI/NotesView.swift`, `ios/State/Sources/UI/NoteDetailView.swift`
- Modify: `MarkdownBlocks.swift` (`.task(checked:text:)`, `.divider`), `MarkdownView.swift`, `MainTabView.swift`, `SplitRootView.swift`, `Localizable.xcstrings`, `ios/StateTests/MarkdownBlocksTests.swift`

Gestaltung (DESIGN.md: Tinte auf kühlem Papier, Systemtypografie, ein Gedanke pro Screen):
- Tab "Notes" mit `note.text`, zwischen "Planned" und "Activity".
- Liste: Titel `.headline`, Zusammenfassung `.subheadline` sekundär, zwei Zeilen, relative Zeit `.caption` tertiär. Suche über `.searchable`. Leerer Zustand mit `.statePill` "New note".
- Plus oben rechts wie bei Erinnerungen, öffnet direkt eine neue Notiz. Foto und Audio erscheinen erst mit Stufe B.
- Detail: gerenderte Notiz mit `MarkdownView`, Checklisten per Tipp abhakbar (schreibt das Dokument zurück), "Edit" wechselt in den Editor.
- Editor: Titelfeld, `TextEditor` mit Auswahl-Bindung, Formatleiste über der Tastatur: Überschrift, fett, kursiv, Liste, Checkliste, Trennlinie. Speichern nur bei Änderung, sonst Abbrechen ohne Nachfrage.
- Seitenleiste auf iPad und Mac: Eintrag "Notes".
- Deutsch: Notizen, Neue Notiz, Notizen durchsuchen, Noch keine Notizen, Titel, Bearbeiten, Fertig, Archivieren.

- [ ] **Step 1: Failing Tests:** `MarkdownBlocks` erkennt `- [ ] a`, `- [x] b`, `---`; `NoteEditing.toggleTask(at:in:)` schaltet genau die richtige Zeile.
- [ ] **Step 2–4:** rot, implementieren, grün, Mac-Target baut, Render-Nachweis hell und dunkel.
- [ ] **Step 5:** `git commit -m "feat: add the notes tab with a markdown editor"`

---

### Task 8: Vertrag, Doku, Auslieferung

**Files:**
- Modify: `openapi/state-v1.yaml`, `DOCUMENTATION.md`, `README.md` (MCP-Liste), `docs/ai-managed-notes.md` (Status-Zeile auf Stufe A umgesetzt)
- Außerhalb des Repos: `~/.agents/skills/state-sync/references/mcp-tools.md` und `cli.md` um die Notiz-Werkzeuge ergänzen

- [ ] **Step 1:** `gofmt -l ./cmd ./internal` leer, `go vet ./...`, `GOTOOLCHAIN=local go test -race ./...`, iOS-Tests, Mac-Build.
- [ ] **Step 2:** Server neu bauen und Mac Server aktualisieren (`bash macos/build.sh`, App ersetzen), `scripts/install-agent-tools.sh` für `statectl`, Live-Nachweis: `statectl note create` und `statectl note list` gegen den Mac Server.
- [ ] **Step 3:** PR, Merge, TestFlight iOS und Mac.
- [ ] **Step 4:** `git commit -m "docs: document notes in the api contract and guides"`
