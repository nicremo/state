# WP05 Report: Capture-Regeln für Agenten und manuelle Harness-Integration

**Status:** DONE
**Branch:** wp/05-agent-capture-rules
**Basis:** `ae6ab23` (Stand beim Anlegen des Worktrees)
**Commits:** c3c6bc0, bab4aed, c34d6fd, 764d4e1, be11312, a6ad25a, danach dieser Report-Nachtrag

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
| Byte-Prüfung aller geänderten Dateien auf Halbgeviert- und Geviertstriche | keine Treffer, auch nicht in `rules_test.go`, das die Zeichen als `\u2013\u2014` escapet |
| Unabhängige Prüfung durch einen zweiten Review-Durchlauf | Aufgaben 1 bis 3 bestanden, vier Punkte an Doku und Test gefunden und vor diesem Report behoben |

## Abweichungen vom Plan

1. **Task 2, erwarteter JSON-Ausschnitt.** Der Plan rechnete mit `"mcp", "--profile", "pi-main"` in einer Zeile und wies bereits darauf hin, dass die echte Formatierung abweichen kann. `encodeJSONObject` nutzt `json.MarshalIndent`, deshalb steht jedes Argument in einer eigenen Zeile. Der Test prüft jetzt die drei Fragmente einzeln und dekodiert zusätzlich den ausgegebenen JSON-Block und vergleicht das Args-Array mit `["mcp", "--profile", "pi-main"]`. Das ist strenger als der Plan und folgt seiner Vorgabe, den Test an die echte Formatierung anzupassen.
2. **Harness-Label für Pi.** Der Plan nennt `pi-agent`, die App führt im `HarnessCatalog` aber das Preset `pi` (Label "Pi"). Beide Labels erhalten den Hinweis, beide sind getestet. Die Doku nennt `pi` und vermerkt `pi-agent` als ebenfalls akzeptiert.
3. **`statectl reminder create` in den Regeln.** Der Befehl entsteht parallel in WP04 und existiert auf meinem Basisstand noch nicht. Das ist laut Plan gewollt, der Koordinator mergt beide. Gegenprobe im WP04-Worktree: dessen `cmd/statectl/reminder.go` kennt genau die Flags, die der Regeltext nennt (`--profile`, `--title`, `--source-text`, `--date`, `--time`, `--tz`, `--repeat`), plus weitere. Der Satz im Regeltext stimmt also mit der realen WP04-Oberfläche überein, eine Korrektur nach dem Merge ist nicht nötig.
4. **Mac Server App.** `macos/` liegt mittlerweile über PR #38 auf `origin/main`. Deren Pairing-Auswahl bietet nur iPhone, Claude Code, Codex und OpenCode, also keine freien Labels. Die Doku nennt das jetzt ausdrücklich und verweist für Pi Agent, DeepSeek Harness und eigene Labels auf die State App.
5. **Bestehende Tests mit altem Regeltext.** Es gab keinen: `TestMarkedRuleBlockIsIdempotentAndRemovable` in `config_test.go` arbeitet mit eigenem Inline-Text. Statt einer Umstellung prüft der Codex-Installertest jetzt zusätzlich, dass die geschriebene Regeldatei `DefaultAgentRules()` enthält.
6. **`internal/mcpserver/server_test.go`.** Nicht angefasst, weil nur `server.go` in meinem Bereich liegt. Kein Test erwartete den alten Beschreibungstext.
7. **Nachtrag nach unabhängiger Prüfung.** Ein unabhängiger Review-Durchlauf hat vier Punkte gefunden, die danach korrigiert wurden: der Dash-Test in `rules_test.go` enthielt die beiden Dash-Zeichen als Literale statt als Escapes, und drei Formulierungen in `docs/agent-integration.md` waren ungenau (markierter Block auch für die JSON-Harness behauptet, "Nothing on disk is touched", sowie die Mac-Server-Pairing-Auswahl). Die Korrekturen stehen in a6ad25a.

## Basisstand und Merge

- Mein Branch basiert auf `ae6ab23`. Während der Arbeit ist `origin/main` auf `4254d1b` weitergezogen (PR #38, Integration des Mac Servers und des Produkt-Lineup-Plans). Deshalb zeigt `git diff origin/main` lokal viele fremde Änderungen als Rücknahme an. Das ist Basisversatz, keine Änderung dieses WP.
- Der echte WP05-Diff ist `git diff ae6ab23 HEAD`. GitHub berechnet den PR-Diff über den Merge-Base, PR #37 zeigt daher korrekt nur die acht WP05-Dateien.
- PR #38 berührt keine WP05-Codedatei. In `docs/plans/2026-09-23-product-lineup/` kommt nur die neue Reportdatei hinzu, ein Konflikt ist unwahrscheinlich. Das Angleichen an das aktuelle `main` ist laut Protokoll Abschnitt 5 Aufgabe des Koordinators.
- Mit dem aktualisierten `main` liegen die Plandokumente jetzt im Repo. Abschnitt 5 Punkt 3 der README nennt die Merge-Reihenfolge WP01, WP08, WP03, WP05, WP04, WP02, WP06, also WP05 vor WP04, und ergänzt danach den Satz zum CLI-Fallback.

## Offene Fragen und Risiken

- Der Regeltext ist mit 2800 Bytes deutlich unter der Testgrenze von 4000 Bytes. Der Test schlägt erst an, wenn der Text um mehr als 40 Prozent wächst.
- Der Hinweisblock steht in der Ausgabe zwischen dem Kommandozeilen-Beispiel und dem Regeln-Block. Für ein reines Copy-Paste-Werkzeug ist das eine zusätzliche Zeile, die nicht kopiert werden darf.
- `docs/plans/` liegt seit PR #38 auf `main`. Der Report kommt als neue Datei im Ordner `reports/` hinzu, der bislang keinen WP05-Report enthält, also ohne Konflikt.

## Manuelle Schritte für Fabian oder den Koordinator

- Merge-Reihenfolge laut Master-Plan Abschnitt 5 Punkt 3: WP01, WP08, WP03, WP05, WP04, WP02, WP06. WP05 kommt also vor WP04. Nach dem Merge von WP04 prüfen, ob dessen reale Flags vom Regeltext abweichen, und den im Plan vorgesehenen Satz zum CLI-Fallback nur dann anpassen.
- Nach dem Merge `statectl pair` für Claude Code, Codex, OpenCode, DeepSeek Harness und Pi Agent auf dem Mac ausführen und die Regelblöcke in den echten Instruktionsdateien sichten.
