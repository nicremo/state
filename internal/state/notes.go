package state

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Notes are unstructured personal knowledge next to reminders: one flat list,
// a Markdown document as canonical content, and a title and summary that are
// either written by a person or derived from the document. A derived value is
// recomputed when the document changes; a written one is never overwritten.

const (
	MaxNoteDocumentBytes = 262144
	MaxNoteTitleRunes    = 200
	MaxNoteSummaryRunes  = 280
	derivedSummaryRunes  = 160
)

type NoteFieldSource string

const (
	NoteFieldSourceUser    NoteFieldSource = "user"
	NoteFieldSourceDerived NoteFieldSource = "derived"
)

const (
	AuditActionNoteCreated  AuditAction = "note.created"
	AuditActionNoteUpdated  AuditAction = "note.updated"
	AuditActionNoteArchived AuditAction = "note.archived"
	AuditActionNoteRestored AuditAction = "note.restored"
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
	Title           string     `json:"title,omitempty"`
	Document        string     `json:"document,omitempty"`
	Summary         string     `json:"summary,omitempty"`
	ClientTime      *time.Time `json:"client_time,omitempty"`
	Source          string     `json:"source,omitempty"`
	SourceExcerpt   string     `json:"source_excerpt,omitempty"`
	ClientRequestID string     `json:"client_request_id"`
	CorrelationID   string     `json:"correlation_id,omitempty"`
}

type UpdateNoteInput struct {
	Title            *string    `json:"title,omitempty"`
	Document         *string    `json:"document,omitempty"`
	Summary          *string    `json:"summary,omitempty"`
	Archived         *bool      `json:"archived,omitempty"`
	ExpectedRevision int64      `json:"expected_revision"`
	ClientTime       *time.Time `json:"client_time,omitempty"`
	Source           string     `json:"source,omitempty"`
	SourceExcerpt    string     `json:"source_excerpt,omitempty"`
	ClientRequestID  string     `json:"client_request_id"`
	CorrelationID    string     `json:"correlation_id,omitempty"`
}

type NoteListOptions struct {
	Query           string
	IncludeArchived bool
	Limit           int
}

func (service *Service) CreateNote(ctx context.Context, actor Actor, input CreateNoteInput) (Note, error) {
	if actor.ID == "" || actor.Kind == "" || input.ClientRequestID == "" {
		return Note{}, ErrInvalidInput
	}
	if actor.Kind == ActorKindRunner {
		return Note{}, ErrForbidden
	}
	note := Note{Document: input.Document}
	applyNoteTitle(&note, input.Title)
	applyNoteSummary(&note, input.Summary)
	if err := validateNote(note); err != nil {
		return Note{}, err
	}

	noteID, err := service.newID()
	if err != nil {
		return Note{}, fmt.Errorf("generate note ID: %w", err)
	}
	eventID, err := service.newID()
	if err != nil {
		return Note{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	now := service.clock().UTC()
	note.ID = noteID
	note.Revision = 1
	note.CreatedAt = now
	note.UpdatedAt = now

	event, err := service.buildAuditEvent(eventID, "", AuditActionNoteCreated, actor, now, input.ClientTime, input.Source, input.SourceExcerpt, nil, note, []string{"archived", "document", "plain_text", "summary", "title"}, note.Revision, input.CorrelationID, input.ClientRequestID)
	if err != nil {
		return Note{}, err
	}
	event.NoteID = note.ID
	return service.repository.CreateNote(ctx, note, event, input.ClientRequestID)
}

func (service *Service) UpdateNote(ctx context.Context, actor Actor, noteID string, input UpdateNoteInput) (Note, error) {
	if actor.ID == "" || actor.Kind == "" || noteID == "" || input.ClientRequestID == "" {
		return Note{}, ErrInvalidInput
	}
	if actor.Kind == ActorKindRunner {
		return Note{}, ErrForbidden
	}
	if input.Archived != nil && actor.Kind != ActorKindOwner && actor.Kind != ActorKindDevice {
		return Note{}, ErrForbidden
	}

	current, err := service.repository.GetNote(ctx, noteID)
	if err != nil {
		return Note{}, err
	}
	if current.Revision != input.ExpectedRevision {
		return Note{}, ErrRevisionConflict
	}

	updated := current
	if input.Document != nil {
		updated.Document = *input.Document
		updated.PlainText = NotePlainText(updated.Document)
	}
	switch {
	case input.Title != nil:
		applyNoteTitle(&updated, *input.Title)
	case updated.TitleSource == NoteFieldSourceDerived:
		applyNoteTitle(&updated, "")
	}
	switch {
	case input.Summary != nil:
		applyNoteSummary(&updated, *input.Summary)
	case updated.SummarySource == NoteFieldSourceDerived:
		applyNoteSummary(&updated, "")
	}
	if input.Archived != nil {
		updated.Archived = *input.Archived
	}
	if err := validateNote(updated); err != nil {
		return Note{}, err
	}

	changed := changedNoteFields(current, updated)
	if len(changed) == 0 {
		return current, nil
	}
	action := AuditActionNoteUpdated
	if current.Archived != updated.Archived {
		action = AuditActionNoteRestored
		if updated.Archived {
			action = AuditActionNoteArchived
		}
	}

	eventID, err := service.newID()
	if err != nil {
		return Note{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	now := service.clock().UTC()
	updated.Revision = current.Revision + 1
	updated.UpdatedAt = now
	event, err := service.buildAuditEvent(eventID, "", action, actor, now, input.ClientTime, input.Source, input.SourceExcerpt, current, updated, changed, updated.Revision, input.CorrelationID, input.ClientRequestID)
	if err != nil {
		return Note{}, err
	}
	event.NoteID = updated.ID
	return service.repository.UpdateNote(ctx, updated, current.Revision, event, input.ClientRequestID)
}

func (service *Service) GetNote(ctx context.Context, noteID string) (Note, error) {
	if noteID == "" {
		return Note{}, ErrInvalidInput
	}
	return service.repository.GetNote(ctx, noteID)
}

func (service *Service) ListNotes(ctx context.Context, options NoteListOptions) ([]Note, error) {
	return service.repository.ListNotes(ctx, options)
}

func (service *Service) ListNoteHistory(ctx context.Context, noteID string) ([]AuditEvent, error) {
	if _, err := service.GetNote(ctx, noteID); err != nil {
		return nil, err
	}
	return service.repository.ListNoteAuditEvents(ctx, noteID)
}

// applyNoteTitle sets a written title, or derives one from the document when
// the written title is empty.
func applyNoteTitle(note *Note, title string) {
	title = strings.TrimSpace(title)
	if title != "" {
		note.Title = title
		note.TitleSource = NoteFieldSourceUser
	} else {
		note.Title = DeriveNoteTitle(note.Document)
		note.TitleSource = NoteFieldSourceDerived
	}
	note.PlainText = NotePlainText(note.Document)
}

// applyNoteSummary sets a written summary, or derives one. A derived title
// already shows the first line, so the derived summary starts after it.
func applyNoteSummary(note *Note, summary string) {
	summary = strings.TrimSpace(summary)
	if summary != "" {
		note.Summary = summary
		note.SummarySource = NoteFieldSourceUser
		return
	}
	if note.TitleSource == NoteFieldSourceDerived {
		note.Summary = DeriveNoteSummary(note.Document)
	} else {
		note.Summary = summarize(nonEmptyLines(NotePlainText(note.Document)))
	}
	note.SummarySource = NoteFieldSourceDerived
}

func validateNote(note Note) error {
	if note.Title == "" && strings.TrimSpace(note.Document) == "" {
		return ErrInvalidInput
	}
	if len(note.Document) > MaxNoteDocumentBytes || !utf8.ValidString(note.Document) {
		return ErrInvalidInput
	}
	if utf8.RuneCountInString(note.Title) > MaxNoteTitleRunes || utf8.RuneCountInString(note.Summary) > MaxNoteSummaryRunes {
		return ErrInvalidInput
	}
	return nil
}

func changedNoteFields(before Note, after Note) []string {
	changed := make([]string, 0, 7)
	add := func(field string, differs bool) {
		if differs {
			changed = append(changed, field)
		}
	}
	add("archived", before.Archived != after.Archived)
	add("document", before.Document != after.Document)
	add("plain_text", before.PlainText != after.PlainText)
	add("summary", before.Summary != after.Summary || before.SummarySource != after.SummarySource)
	add("title", before.Title != after.Title || before.TitleSource != after.TitleSource)
	sort.Strings(changed)
	return changed
}

var (
	noteHeadingMarker     = regexp.MustCompile(`^#{1,6}\s+`)
	noteOrderedListMarker = regexp.MustCompile(`^\d{1,3}[.)]\s+`)
	noteInlineMarkers     = strings.NewReplacer("**", "", "__", "", "~~", "", "`", "", "*", "")
	noteLinePrefixes      = []string{"- [ ] ", "- [x] ", "- [X] ", "- ", "* ", "+ ", "> "}
)

// NotePlainText strips Markdown markers so search, MCP and CLI see readable
// text. Code blocks keep their lines verbatim.
func NotePlainText(document string) string {
	lines := strings.Split(strings.ReplaceAll(document, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	inCode := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			out = append(out, strings.TrimRight(raw, " \t"))
			continue
		}
		if line == "---" || line == "***" || line == "___" {
			continue
		}
		line = noteHeadingMarker.ReplaceAllString(line, "")
		for _, prefix := range noteLinePrefixes {
			if strings.HasPrefix(line, prefix) {
				line = strings.TrimPrefix(line, prefix)
				break
			}
		}
		line = noteOrderedListMarker.ReplaceAllString(line, "")
		out = append(out, strings.TrimSpace(noteInlineMarkers.Replace(line)))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// DeriveNoteTitle takes the first non-empty line, as Apple Notes does.
func DeriveNoteTitle(document string) string {
	lines := nonEmptyLines(NotePlainText(document))
	if len(lines) == 0 {
		return ""
	}
	return truncateRunes(lines[0], MaxNoteTitleRunes)
}

// DeriveNoteSummary joins the lines after the title line into a short preview.
func DeriveNoteSummary(document string) string {
	lines := nonEmptyLines(NotePlainText(document))
	if len(lines) <= 1 {
		return ""
	}
	return summarize(lines[1:])
}

func summarize(lines []string) string {
	return truncateRunes(strings.Join(lines, " "), derivedSummaryRunes)
}

func nonEmptyLines(text string) []string {
	result := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}

// noteMatchesQuery is the in-memory search used by MemoryRepository: every
// term must occur in the title, summary or plain text.
func noteMatchesQuery(note Note, query string) bool {
	haystack := strings.ToLower(note.Title + "\n" + note.Summary + "\n" + note.PlainText)
	for _, term := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}
