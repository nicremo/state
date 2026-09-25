package state

import "testing"

func TestVoiceDocumentProblem(t *testing.T) {
	t.Parallel()
	transcript := "Heute war ziemlich nervig. Ich habe meine CLAUDE.md angepasst, damit die besser laufen."
	for _, test := range []struct {
		name     string
		document string
		fails    bool
	}{
		{"screenshot: transcript under a placeholder heading", "## Sprachnotiz\nHeute war ziemlich nervig. Ich habe meine CLAUDE.md angepasst, damit die besser laufen.", true},
		{"transcript split into bullets", "- Heute war ziemlich nervig.\n- Ich habe meine CLAUDE.md angepasst, damit die besser laufen.", true},
		{"prose in own words", "Der Tag war nervig, die CLAUDE.md wurde angepasst.", true},
		{"placeholder heading over good bullets", "## Transkript\n- Nerviger Tag\n- CLAUDE.md angepasst", true},
		{"empty body", "", true},
		{"condensed bullets", "- Nerviger Tag\n- CLAUDE.md angepasst, damit sie besser laufen", false},
		{"checklist with topic heading", "## Setup\n- [x] CLAUDE.md angepasst\n- [ ] Prüfen, ob es besser läuft", false},
		// Review finding 1: models often write dash bullets; they are
		// normalised later and must not count as running text.
		{"en dash and em dash bullets", "\u2013 Nerviger Tag\n\u2014 CLAUDE.md angepasst", false},
		{"numbered list and table", "1. Tag war nervig\n2. CLAUDE.md optimiert\n\n| Datei | Stand |\n| --- | --- |\n| CLAUDE.md | angepasst |", false},
	} {
		problem := VoiceDocumentProblem(test.document, transcript)
		if (problem != "") != test.fails {
			t.Errorf("%s: problem = %q, want failure %v", test.name, problem, test.fails)
		}
	}
	if problem := VoiceDocumentProblem("", "   "); problem != "" {
		t.Errorf("silent recording: problem = %q", problem)
	}
	if problem := VoiceDocumentProblem("- Milch", "Milch kaufen"); problem != "" {
		t.Errorf("very short recording: problem = %q", problem)
	}
}
