package state

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
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
}

func TestNoteDerivationMatchesTheGoldenFile(t *testing.T) {
	cases := make([]noteDerivationCase, 0, len(noteDerivationInputs))
	for _, document := range noteDerivationInputs {
		cases = append(cases, noteDerivationCase{
			Document:  document,
			PlainText: NotePlainText(document),
			Title:     DeriveNoteTitle(document),
			Summary:   DeriveNoteSummary(document),
		})
	}
	path := filepath.Join("testdata", "note_derivation.json")
	if *updateGolden {
		encoded, err := json.MarshalIndent(cases, "", "  ")
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
