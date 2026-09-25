// Command fake-openrouter serves the scripted OpenRouter stand-in for local
// end-to-end runs of the notes AI, so the app and server can be exercised in
// the simulator without a real key or real costs.
//
//	go run ./tools/fake-openrouter -addr 127.0.0.1:18091
//	STATE_OPENROUTER_BASE_URL=http://127.0.0.1:18091/api/v1 state-server serve ...
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/nicremo/state/internal/notesai/fakeopenrouter"
)

var candidatePattern = regexp.MustCompile(`(?m)^- ([0-9a-f-]{36}):`)

func main() {
	address := flag.String("addr", "127.0.0.1:18091", "listen address")
	flag.Parse()
	fake := fakeopenrouter.New()
	fake.DefaultChat = reply
	defer fake.Close()
	log.Printf("fake OpenRouter, base URL http://%s/api/v1", *address)
	log.Fatal(http.ListenAndServe(*address, fake.Config.Handler))
}

// reply answers like a careful notes agent: it links the first related note
// it is offered, then submits a result that depends on the material.
func reply(body map[string]any) fakeopenrouter.Reply {
	messages, _ := body["messages"].([]any)
	content := ""
	usedTool := false
	for _, raw := range messages {
		message, _ := raw.(map[string]any)
		if message["role"] == "tool" {
			usedTool = true
		}
		if text, ok := message["content"].(string); ok {
			content += text
			continue
		}
		encoded, _ := json.Marshal(message["content"])
		content += string(encoded)
	}
	if !usedTool {
		if match := candidatePattern.FindStringSubmatch(strings.ReplaceAll(content, `\n`, "\n")); match != nil {
			return fakeopenrouter.ToolCall("link-1", "link_related_note", map[string]any{
				"note_id": match[1], "reason": "Beide Notizen behandeln dasselbe Thema.", "confidence": 0.8,
			}, 0.0004)
		}
	}
	switch {
	case strings.Contains(content, "data:image"):
		return fakeopenrouter.ToolCall("submit", "submit_result", map[string]any{
			"title":       "Notizbuchseite",
			"summary":     "Handschriftliche Seite mit drei Aufgaben für diese Woche.",
			"document":    "## Notizbuchseite\n- [ ] Monatsreport an Karla\n- [ ] Zahlen prüfen\n- [x] Termin bestätigt\n\n==Wichtig:== Report bis **Freitag**.",
			"image_texts": []map[string]any{{"index": 1, "text": "Monatsreport an Karla, Zahlen prüfen, Termin bestätigt. Wichtig: Report bis Freitag."}},
		}, 0.0021)
	case strings.Contains(content, "<transcript>"):
		return fakeopenrouter.ToolCall("submit", "submit_result", map[string]any{
			"title":    "Sprachnotiz Einkauf",
			"summary":  "Einkauf und ein Anruf bei Karla.",
			"document": "- [ ] Milch kaufen\n- [ ] Karla anrufen",
		}, 0.0012)
	default:
		return fakeopenrouter.ToolCall("submit", "submit_result", map[string]any{
			"title":   "Von der Notiz-KI benannt",
			"summary": "Kurze Zusammenfassung der Notiz durch die Notiz-KI.",
		}, 0.0008)
	}
}
