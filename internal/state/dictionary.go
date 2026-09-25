package state

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The owner's dictionary helps the notes AI with names and terms that speech
// recognition gets wrong. Words are spellings the notes AI should use.
// Corrections map what was misheard to what was meant: "always" entries are
// replaced in every transcript, "context" entries are ambiguous everyday
// words ("Note", "Cloud") that only the notes agent, which sees the whole
// note, may correct. There is one dictionary per server; only the owner and
// the owner's devices change it, agents may read it.

type DictionaryMode string

const (
	DictionaryModeAlways  DictionaryMode = "always"
	DictionaryModeContext DictionaryMode = "context"
)

const (
	AuditActionNotesDictionaryUpdated AuditAction = "notes_ai.dictionary_updated"

	maxDictionaryWords       = 500
	maxDictionaryCorrections = 500
	maxDictionaryEntryRunes  = 80
)

type DictionaryCorrection struct {
	From string         `json:"from"`
	To   string         `json:"to"`
	Mode DictionaryMode `json:"mode"`
}

type NotesDictionary struct {
	Words       []string               `json:"words"`
	Corrections []DictionaryCorrection `json:"corrections"`
	Revision    int64                  `json:"revision"`
	UpdatedAt   time.Time              `json:"updated_at,omitempty"`
}

type UpdateNotesDictionaryInput struct {
	Words            []string               `json:"words"`
	Corrections      []DictionaryCorrection `json:"corrections"`
	ExpectedRevision *int64                 `json:"expected_revision,omitempty"`
}

// GetNotesDictionary returns the dictionary, empty before the first save.
func (service *Service) GetNotesDictionary(ctx context.Context) (NotesDictionary, error) {
	dictionary, found, err := service.repository.GetNotesDictionary(ctx)
	if err != nil {
		return NotesDictionary{}, err
	}
	if !found {
		dictionary = NotesDictionary{}
	}
	if dictionary.Words == nil {
		dictionary.Words = []string{}
	}
	if dictionary.Corrections == nil {
		dictionary.Corrections = []DictionaryCorrection{}
	}
	return dictionary, nil
}

// UpdateNotesDictionary replaces the whole dictionary. A stale
// expected_revision is refused, so two devices never overwrite each other.
func (service *Service) UpdateNotesDictionary(ctx context.Context, actor Actor, input UpdateNotesDictionaryInput) (NotesDictionary, error) {
	if actor.Kind != ActorKindOwner && actor.Kind != ActorKindDevice {
		return NotesDictionary{}, ErrForbidden
	}
	current, err := service.GetNotesDictionary(ctx)
	if err != nil {
		return NotesDictionary{}, err
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return NotesDictionary{}, ErrRevisionConflict
	}
	return service.saveNotesDictionary(ctx, actor, current, input.Words, input.Corrections, "rest")
}

// SeedNotesDictionary fills a dictionary that was never saved from the
// owner's local seed file. It reports whether it did.
func (service *Service) SeedNotesDictionary(ctx context.Context, text string) (bool, error) {
	_, found, err := service.repository.GetNotesDictionary(ctx)
	if err != nil || found {
		return false, err
	}
	words, corrections, err := ParseDictionaryText(text)
	if err != nil {
		return false, err
	}
	if _, err := service.saveNotesDictionary(ctx, SystemActor(), NotesDictionary{}, words, corrections, "seed"); err != nil {
		return false, err
	}
	return true, nil
}

func (service *Service) saveNotesDictionary(ctx context.Context, actor Actor, current NotesDictionary, words []string, corrections []DictionaryCorrection, source string) (NotesDictionary, error) {
	cleanWords, cleanCorrections, err := normalizeDictionary(words, corrections)
	if err != nil {
		return NotesDictionary{}, err
	}
	now := service.clock().UTC()
	dictionary := NotesDictionary{Words: cleanWords, Corrections: cleanCorrections, Revision: current.Revision + 1, UpdatedAt: now}
	eventID, err := service.newID()
	if err != nil {
		return NotesDictionary{}, fmt.Errorf("generate audit event ID: %w", err)
	}
	event, err := service.buildAuditEvent(eventID, "", AuditActionNotesDictionaryUpdated, actor, now, nil, source, "", nil, dictionary, []string{"dictionary"}, dictionary.Revision, "", eventID)
	if err != nil {
		return NotesDictionary{}, err
	}
	if err := service.repository.SaveNotesDictionary(ctx, dictionary, event); err != nil {
		return NotesDictionary{}, err
	}
	return dictionary, nil
}

func normalizeDictionary(words []string, corrections []DictionaryCorrection) ([]string, []DictionaryCorrection, error) {
	if len(words) > maxDictionaryWords || len(corrections) > maxDictionaryCorrections {
		return nil, nil, ErrInvalidInput
	}
	cleanWords := make([]string, 0, len(words))
	seenWords := map[string]bool{}
	for _, word := range words {
		word, ok := dictionaryEntry(word)
		if !ok {
			return nil, nil, ErrInvalidInput
		}
		if key := strings.ToLower(word); !seenWords[key] {
			seenWords[key] = true
			cleanWords = append(cleanWords, word)
		}
	}
	cleanCorrections := make([]DictionaryCorrection, 0, len(corrections))
	seenCorrections := map[string]bool{}
	for _, correction := range corrections {
		from, fromOK := dictionaryEntry(correction.From)
		to, toOK := dictionaryEntry(correction.To)
		if !fromOK || !toOK || from == to || (correction.Mode != DictionaryModeAlways && correction.Mode != DictionaryModeContext) {
			return nil, nil, ErrInvalidInput
		}
		if key := strings.ToLower(from); !seenCorrections[key] {
			seenCorrections[key] = true
			cleanCorrections = append(cleanCorrections, DictionaryCorrection{From: from, To: to, Mode: correction.Mode})
		}
	}
	return cleanWords, cleanCorrections, nil
}

func dictionaryEntry(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxDictionaryEntryRunes {
		return "", false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return value, true
}

// ParseDictionaryText reads the import format, one entry per line:
//
//	word: Supabase
//	always: ZEVDISK -> sevDesk
//	context: Note -> Node
//
// A line without a prefix is a word, or a context correction when it holds
// "->". Empty lines and lines starting with # are skipped.
func ParseDictionaryText(text string) ([]string, []DictionaryCorrection, error) {
	words := make([]string, 0)
	corrections := make([]DictionaryCorrection, 0)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kind, rest := "", line
		if prefix, value, found := strings.Cut(line, ":"); found && !strings.ContainsAny(prefix, " ->") {
			kind, rest = strings.ToLower(strings.TrimSpace(prefix)), strings.TrimSpace(value)
		}
		switch kind {
		case "word":
			words = append(words, rest)
		case "always", "context":
			from, to, found := strings.Cut(rest, "->")
			if !found || strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
				return nil, nil, ErrInvalidInput
			}
			corrections = append(corrections, DictionaryCorrection{From: strings.TrimSpace(from), To: strings.TrimSpace(to), Mode: DictionaryMode(kind)})
		case "":
			if from, to, found := strings.Cut(rest, "->"); found {
				corrections = append(corrections, DictionaryCorrection{From: strings.TrimSpace(from), To: strings.TrimSpace(to), Mode: DictionaryModeContext})
			} else {
				words = append(words, rest)
			}
		default:
			return nil, nil, ErrInvalidInput
		}
	}
	return words, corrections, nil
}

// ApplyDictionary replaces the "always" corrections in a transcript: whole
// words only, ignoring case, the longest entry first, in one pass from left
// to right, so a replacement is never replaced again. "Note" becomes "Node"
// in "die Note ist", but "Schulnote" stays as it is.
func ApplyDictionary(text string, corrections []DictionaryCorrection) string {
	type entry struct {
		from       []rune
		to         string
		wordStart  bool
		wordFinish bool
	}
	entries := make([]entry, 0, len(corrections))
	for _, correction := range corrections {
		if correction.Mode != DictionaryModeAlways || correction.From == "" {
			continue
		}
		from := []rune(correction.From)
		entries = append(entries, entry{from: from, to: correction.To, wordStart: isWordRune(from[0]), wordFinish: isWordRune(from[len(from)-1])})
	}
	if len(entries) == 0 || text == "" {
		return text
	}
	sort.SliceStable(entries, func(left, right int) bool { return len(entries[left].from) > len(entries[right].from) })
	runes := []rune(text)
	var builder strings.Builder
	for index := 0; index < len(runes); {
		matched := false
		for _, candidate := range entries {
			end := index + len(candidate.from)
			if end > len(runes) ||
				(candidate.wordStart && index > 0 && isWordRune(runes[index-1])) ||
				(candidate.wordFinish && end < len(runes) && isWordRune(runes[end])) ||
				!strings.EqualFold(string(runes[index:end]), string(candidate.from)) {
				continue
			}
			builder.WriteString(candidate.to)
			index = end
			matched = true
			break
		}
		if !matched {
			builder.WriteRune(runes[index])
			index++
		}
	}
	return builder.String()
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
