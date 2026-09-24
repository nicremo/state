package state

import (
	"encoding/json"
	"flag"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/note_derivation.json")

// noteDerivationCase is one row of the golden file the app's NoteText tests
// read as well, so the server and the app derive the same title and summary.
type noteDerivationCase struct {
	Document  string `json:"document"`
	PlainText string `json:"plain_text"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
}

var noteDerivationInputs = []string{
	"# Einkauf\n\nMilch und **Brot** holen.\n- [ ] Eier",
	"Erste Zeile\nZweite Zeile",
	"## Plan\n1. `go test` *jetzt*\n- [x] erledigt\n---\n> Zitat\n```\n# code\n```",
	"2 * 3 = 6 and a*b",
	"snake_case_name and __fett__ and _kursiv_",
	"~~~\n# keine Überschrift\n~~~\nText danach",
	"#hashtag bleibt\nzweite",
	"~**~ seltsam",
	"١. arabische Ziffer\n#\u00a0kein Leerzeichen",
	"Titel\r\nmit CRLF\r\n- [ ] Aufgabe",
	"   eingerückt   \n\n\n  zweite  ",
	"Ein sehr langer erster Satz " + string(make([]byte, 0)) + "mit Umlauten äöü und Emoji 👨‍👩‍👧‍👦 der weiter geht und weiter geht und weiter geht und weiter geht und weiter geht und weiter geht und weiter geht und weiter geht und weiter geht und weiter geht\nkurz",
	"Titel\n" + "Zusammenfassung mit vielen Wörtern die über die Grenze hinausgeht damit abgeschnitten wird und ein Auslassungszeichen am Ende steht, das ist wichtig für die Liste in der App und die CLI Ausgabe gleichermaßen",
	"- [ ]\n- [x] leer davor",
	"Titel\x1b[31m mit  Escape\nZeile\x07 zwei\tTab",
	"\x1b\nEinkaufsliste Milch",
	"Bidi \u202eoverride\u202c und \u0085 C1",
}

// fuzzTokens build random documents from the characters where the Go and
// Swift derivations are most likely to disagree: markers, all whitespace
// kinds, combining marks, emoji sequences and control characters.
var fuzzTokens = []string{"#", "## ", "# ", "-", "- ", "* ", "+ ", "> ", "[ ]", "[x]", " ", "  ", "\t", "\n", "\n", "\r\n", "\r",
	"*", "**", "_", "__", "~~", "~", "`", "```", "~~~", "---", "***", "1. ", "12) ", "\u0661. ", "a", "b", "Wort", "äöü",
	"e\u0301", "\u00e9", "\u00a0", "\u2003", "\u0085", "\u200b", "\u3000", "👨‍👩‍👧", "🇩🇪", "x_y", "2 * 3", "\x1b", "\u0301",
	"\ufeff", "\u202e", "\u009b"}

// fuzzDocuments is deterministic, so the golden file only changes when the
// derivation does.
func fuzzDocuments(count int) []string {
	random := rand.New(rand.NewSource(42))
	documents := make([]string, 0, count)
	for index := 0; index < count; index++ {
		var builder strings.Builder
		for token := 0; token < 1+random.Intn(25); token++ {
			builder.WriteString(fuzzTokens[random.Intn(len(fuzzTokens))])
		}
		if random.Intn(20) == 0 {
			builder.WriteString(strings.Repeat("ab ", 70+random.Intn(10)) + "👨‍👩‍👧")
		}
		documents = append(documents, builder.String())
	}
	return documents
}

func TestNoteDerivationMatchesTheGoldenFile(t *testing.T) {
	inputs := append(append([]string{}, noteDerivationInputs...), fuzzDocuments(3000)...)
	cases := make([]noteDerivationCase, 0, len(inputs))
	for _, document := range inputs {
		cases = append(cases, noteDerivationCase{
			Document:  document,
			PlainText: NotePlainText(document),
			Title:     DeriveNoteTitle(document),
			Summary:   DeriveNoteSummary(document),
		})
	}
	path := filepath.Join("testdata", "note_derivation.json")
	if *updateGolden {
		encoded, err := json.MarshalIndent(cases, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden file: %v (run go test ./internal/state -run Golden -update)", err)
	}
	var want []noteDerivationCase
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(cases) {
		t.Fatalf("golden file has %d cases, code has %d; rerun with -update", len(want), len(cases))
	}
	for index := range cases {
		if cases[index] != want[index] {
			t.Errorf("case %d:\n got %#v\nwant %#v", index, cases[index], want[index])
		}
	}
}
