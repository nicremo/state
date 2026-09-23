# WP05 Report: Capture-Regeln für Agenten und manuelle Harness-Integration

**Status:** DONE
**Branch:** wp/05-agent-capture-rules
**Letzter Commit:** 764d4e1 docs: explain how every agent is paired with state

## Ergebnis in drei Sätzen

`DefaultAgentRules()` enthält jetzt das vollständige Capture-Protokoll aus Abschnitt 6 und 7 der Spezifikation, von der Session-Begrüßung über die Intent-Erkennung und die Pflicht-Rückfragen bis zum Runner-Verhalten. `ManualInstructions()` gibt für Pi Agent und DeepSeek Harness je einen produktspezifischen Hinweisblock plus eine Prüfzeile aus, ohne deren Konfigurationsdateien anzufassen. Die vier zentralen MCP-Tool-Beschreibungen und der `jsonschema`-Tag von `createReminderInput.Description` tragen dieselben Regeln, und `docs/agent-integration.md` beschreibt den kompletten Weg vom Pairing-Code bis zu `statectl doctor`.

## Erledigte Tasks

- [x] Task 1: Neue Standardregeln in `internal/statectl/rules.go`, neue Testdatei `internal/statectl/rules_test.go` (vier Tests, TDD: erst rot gesehen, dann grün).
- [x] Task 2: Produktspezifische Hinweise in `ManualInstructions()`, Prüfzeile `Verify with: <command> doctor --profile <profile>`, drei neue Tests in `internal/statectl/installer_test.go`.
- [x] Task 3: Vier Tool-Beschreibungen und der `jsonschema`-Tag in `internal/mcpserver/server.go`, sonst nichts in dieser Datei.
- [x] Task 4: `docs/agent-integration.md` mit den sechs geforderten Abschnitten, plus ein verlinkender Satz in `docs/universal-agent-todo-capture.md` Abschnitt 6.3.
- [x] Task 5: `gofmt`, `go vet`, `go test -race ./...` grün.

## Geänderte Dateien

| Datei | Änderung |
| --- | --- |
| `internal/statectl/rules.go` | `DefaultAgentRules()` durch den vorgegebenen englischen Regeltext ersetzt, 2800 Bytes |
| `internal/statectl/rules_test.go` | Neu: Capture-Protokoll-Abdeckung, Größengrenze 4000 Bytes, Verbot von Halbgeviert- und Geviertstrichen, Idempotenz von `UpsertRuleBlock` |
| `internal/statectl/installer.go` | `ManualInstructions()` um Hinweisblock und Prüfzeile erweitert, neue Hilfsfunktion `manualHarnessHint()` |
| `internal/statectl/installer_test.go` | Drei neue Tests für Pi Agent, DeepSeek Harness, unbekannte Harness; zusätzlich prüft der Codex-Test, dass die Regeldatei `DefaultAgentRules()` enthält |
| `internal/mcpserver/server.go` | Nur vier `Description`-Strings in `registerTools` und der `jsonschema`-Tag von `createReminderInput.Description` |
| `docs/agent-integration.md` | Neu: Pairing, geschriebene Dateien, Backup, manuelle Agenten, Capture-Protokoll, Verifikation |
| `docs/universal-agent-todo-capture.md` | Ein Satz in Abschnitt 6.3 mit Link auf `agent-integration.md` |

## Dateien außerhalb meines Bereichs

- `docs/universal-agent-todo-capture.md`: ein Satz in Abschnitt 6.3, ausdrücklich in Task 4 gefordert. Änderung ist additiv, keine bestehende Zeile wurde verändert.

`cmd/statectl/**` und `internal/statectl/reminder*.go` (WP04) wurden nicht angefasst.

## Prüfungen

| Befehl | Ergebnis |
| --- | --- |
| `gofmt -l ./cmd ./internal` | leer |
| `go vet ./...` | grün |
| `go test -race ./...` | grün, 15 Pakete |
| `go test ./internal/statectl/ ./internal/mcpserver/ -v` | 35 Tests bestanden |
| `go test ./internal/statectl/ -run 'Rules\|RuleBlock'` | vor der Implementierung rot (vier fehlende Pflichtbegriffe, ein Gedankenstrich), danach grün |
| `grep -rn "Create exactly one idempotent reminder" internal cmd` | kein Treffer, kein Test erwartete den alten Text |
| Byte-Vergleich Regeltext gegen Plan (Zeilen 31 bis 62) | identisch bis auf das abschließende Newline, das die Funktion bewusst nicht liefert |
| Byte-Vergleich der vier Tool-Beschreibungen und des `jsonschema`-Tags gegen die Plantabellen | identisch |
| Laufzeitprüfung der registrierten Tools über einen echten MCP-Client | alle vier neuen Beschreibungen und die neue Schema-Beschreibung kommen beim Client an |
| Laufzeitprüfung der Hinweisblöcke | beide Hinweise entsprechen dem Plan Zeichen für Zeichen, keine Halbgeviert- oder Geviertstriche in der Ausgabe |

## Abweichungen vom Plan

1. **Task 2, erwarteter JSON-Ausschnitt.** Der Plan rechnete mit `"mcp", "--profile", "pi-main"` in einer Zeile und wies bereits darauf hin, dass die echte Formatierung abweichen kann. `encodeJSONObject` nutzt `json.MarshalIndent`, deshalb steht jedes Argument in einer eigenen Zeile. Der Test prüft jetzt die drei Fragmente einzeln und dekodiert zusätzlich den ausgegebenen JSON-Block und vergleicht das Args-Array mit `["mcp", "--profile", "pi-main"]`. Das ist strenger als der Plan und folgt seiner Vorgabe, den Test an die echte Formatierung anzupassen.
2. **Harness-Label für Pi.** Der Plan nennt `pi-agent`, die App führt im `HarnessCatalog` aber das Preset `pi` (Label "Pi"). Beide Labels erhalten den Hinweis, beide sind getestet. Die Doku nennt `pi` und vermerkt `pi-agent` als ebenfalls akzeptiert.
3. **`statectl reminder create` in den Regeln.** Der Befehl entsteht parallel in WP04 und existiert auf `origin/main` noch nicht. Das ist laut Plan gewollt, der Koordinator mergt beide.
4. **Mac Server App.** Der Ordner `macos/` liegt nur auf dem Integrationsbranch, nicht auf `origin/main`. Die Doku nennt die Mac Server App deshalb ohne Pfadangabe.
5. **Bestehende Tests mit altem Regeltext.** Es gab keinen: `TestMarkedRuleBlockIsIdempotentAndRemovable` in `config_test.go` arbeitet mit eigenem Inline-Text. Statt einer Umstellung prüft der Codex-Installertest jetzt zusätzlich, dass die geschriebene Regeldatei `DefaultAgentRules()` enthält.
6. **`internal/mcpserver/server_test.go`.** Nicht angefasst, weil nur `server.go` in meinem Bereich liegt. Kein Test erwartete den alten Beschreibungstext.

## Offene Fragen und Risiken

- Der Regeltext ist mit 2800 Bytes deutlich unter der Testgrenze von 4000 Bytes. Der Test schlägt erst an, wenn der Text um mehr als 40 Prozent wächst.
- Der Hinweisblock steht in der Ausgabe zwischen dem Kommandozeilen-Beispiel und dem Regeln-Block. Für ein reines Copy-Paste-Werkzeug ist das eine zusätzliche Zeile, die nicht kopiert werden darf.
- `docs/plans/` ist im Haupt-Worktree nicht committet, der Report liegt daher als neue Datei auf diesem Branch.

## Manuelle Schritte für Fabian oder den Koordinator

- Merge-Reihenfolge laut Master-Plan Abschnitt 5.3: WP05 nach WP04 mergen. Danach in `DefaultAgentRules()` den vom Plan vorgesehenen Satz zum CLI-Fallback `statectl reminder` ergänzen, falls WP04 abweichende Flags liefert.
- Nach dem Merge `statectl pair` für Claude Code, Codex, OpenCode, DeepSeek Harness und Pi Agent auf dem Mac ausführen und die Regelblöcke in den echten Instruktionsdateien sichten.
