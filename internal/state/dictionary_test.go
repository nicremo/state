package state

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func always(from string, to string) DictionaryCorrection {
	return DictionaryCorrection{From: from, To: to, Mode: DictionaryModeAlways}
}

func inContext(from string, to string) DictionaryCorrection {
	return DictionaryCorrection{From: from, To: to, Mode: DictionaryModeContext}
}

func TestApplyDictionaryReplacesWholeWordsOnly(t *testing.T) {
	t.Parallel()
	corrections := []DictionaryCorrection{
		always("ZEVDISK", "sevDesk"),
		always("Cloud.md", "CLAUDE.md"),
		always("Cloud Code", "Claude Code"),
		always("Note", "Node"),
		always("Wörsel", "Vercel"),
		always("Fetsien.", "shad/cn"),
		inContext("Cloud", "Claude"),
		inContext("Wurzel", "Vercel"),
	}
	for _, test := range []struct {
		input string
		want  string
	}{
		{"Rechnung in ZEVDISK anlegen", "Rechnung in sevDesk anlegen"},
		{"rechnung in zevdisk", "rechnung in sevDesk"},
		{"Ich habe meine Cloud.md angepasst", "Ich habe meine CLAUDE.md angepasst"},
		{"Mit Cloud Code gebaut", "Mit Claude Code gebaut"},
		{"Die Cloud ist voll", "Die Cloud ist voll"},
		{"Die Wurzel im Garten", "Die Wurzel im Garten"},
		{"Die Schulnote war gut", "Die Schulnote war gut"},
		{"Mein Notebook", "Mein Notebook"},
		{"Die Note ist fertig", "Die Node ist fertig"},
		{"Note.", "Node."},
		{"Wörselchen bleibt", "Wörselchen bleibt"},
		{"Deploy auf Wörsel", "Deploy auf Vercel"},
		{"Nimm Fetsien. Dann", "Nimm shad/cn Dann"},
		{"", ""},
	} {
		if got := ApplyDictionary(test.input, corrections); got != test.want {
			t.Errorf("ApplyDictionary(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestApplyDictionaryDoesNotReplaceItsOwnOutput(t *testing.T) {
	t.Parallel()
	corrections := []DictionaryCorrection{always("Claude", "Claude Code"), always("Code", "Kode")}
	if got := ApplyDictionary("Claude hilft", corrections); got != "Claude Code hilft" {
		t.Fatalf("got %q", got)
	}
}

func TestParseDictionaryText(t *testing.T) {
	t.Parallel()
	words, corrections, err := ParseDictionaryText("# Kommentar\nword: Supabase\n\nalways: ZEVDISK -> sevDesk\ncontext: Note -> Node\nWispr Flow\nAlicid->Elicit\n")
	if err != nil {
		t.Fatalf("ParseDictionaryText() error = %v", err)
	}
	if strings.Join(words, "|") != "Supabase|Wispr Flow" {
		t.Fatalf("words = %q", words)
	}
	want := []DictionaryCorrection{always("ZEVDISK", "sevDesk"), inContext("Note", "Node"), inContext("Alicid", "Elicit")}
	if len(corrections) != len(want) {
		t.Fatalf("corrections = %#v", corrections)
	}
	for index := range want {
		if corrections[index] != want[index] {
			t.Fatalf("correction %d = %#v, want %#v", index, corrections[index], want[index])
		}
	}
	for _, bad := range []string{"always: nur ein Wort", "sometimes: A -> B", "always:  -> B"} {
		if _, _, err := ParseDictionaryText(bad); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("ParseDictionaryText(%q) error = %v, want invalid input", bad, err)
		}
	}
}

func TestUpdateNotesDictionaryRightsRevisionAndAudit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, repository, _ := newAINoteService(t, true)
	empty, err := service.GetNotesDictionary(ctx)
	if err != nil || empty.Revision != 0 || empty.Words == nil || empty.Corrections == nil {
		t.Fatalf("empty dictionary = %#v, %v", empty, err)
	}
	input := UpdateNotesDictionaryInput{Words: []string{" Supabase ", "supabase", "BLUNATECH"}, Corrections: []DictionaryCorrection{always("ZEVDISK", "sevDesk")}}
	for _, actor := range []Actor{noteHarness, noteRunner} {
		if _, err := service.UpdateNotesDictionary(ctx, actor, input); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s may change the dictionary: %v", actor.Kind, err)
		}
	}
	stored, err := service.UpdateNotesDictionary(ctx, noteDevice, input)
	if err != nil {
		t.Fatalf("UpdateNotesDictionary() error = %v", err)
	}
	if stored.Revision != 1 || strings.Join(stored.Words, "|") != "Supabase|BLUNATECH" || len(stored.Corrections) != 1 {
		t.Fatalf("stored = %#v", stored)
	}
	stale := int64(0)
	input.ExpectedRevision = &stale
	if _, err := service.UpdateNotesDictionary(ctx, noteOwner, input); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision error = %v", err)
	}
	current := int64(1)
	input.ExpectedRevision = &current
	if second, err := service.UpdateNotesDictionary(ctx, noteOwner, input); err != nil || second.Revision != 2 {
		t.Fatalf("second update = %#v, %v", second, err)
	}
	changes, _ := repository.ListChanges(ctx, 0, 100)
	updates := 0
	for _, change := range changes {
		if change.Event.Action == AuditActionNotesDictionaryUpdated {
			updates++
		}
	}
	if updates != 2 {
		t.Fatalf("dictionary audit events = %d, want 2", updates)
	}
	for _, change := range VisibleChanges(noteRunner, changes) {
		if change.Event.Action == AuditActionNotesDictionaryUpdated {
			t.Fatal("a runner sees the dictionary")
		}
	}
}

func TestUpdateNotesDictionaryValidates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, _, _ := newAINoteService(t, true)
	for name, input := range map[string]UpdateNotesDictionaryInput{
		"empty word":        {Words: []string{"  "}},
		"control character": {Words: []string{"a\x07b"}},
		"too long":          {Words: []string{strings.Repeat("x", 81)}},
		"empty target":      {Corrections: []DictionaryCorrection{always("A", " ")}},
		"same text":         {Corrections: []DictionaryCorrection{always("Node", "Node")}},
		"unknown mode":      {Corrections: []DictionaryCorrection{{From: "A", To: "B", Mode: "sometimes"}}},
		"too many words":    {Words: manyWords(501)},
	} {
		if _, err := service.UpdateNotesDictionary(ctx, noteOwner, input); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: error = %v, want invalid input", name, err)
		}
	}
}

func manyWords(count int) []string {
	words := make([]string, count)
	for index := range words {
		words[index] = "Wort" + strings.Repeat("x", index%70) + string(rune('A'+index%26)) + string(rune('a'+index/26%26))
	}
	return words
}

func TestSeedNotesDictionaryOnlyOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, _, _ := newAINoteService(t, true)
	seeded, err := service.SeedNotesDictionary(ctx, "word: Supabase\nalways: ZEVDISK -> sevDesk\ncontext: Note -> Node\n")
	if err != nil || !seeded {
		t.Fatalf("first seed = %v, %v", seeded, err)
	}
	again, err := service.SeedNotesDictionary(ctx, "word: Anderes\n")
	if err != nil || again {
		t.Fatalf("second seed = %v, %v", again, err)
	}
	dictionary, _ := service.GetNotesDictionary(ctx)
	if strings.Join(dictionary.Words, "|") != "Supabase" || len(dictionary.Corrections) != 2 || dictionary.Revision != 1 {
		t.Fatalf("dictionary = %#v", dictionary)
	}
}
