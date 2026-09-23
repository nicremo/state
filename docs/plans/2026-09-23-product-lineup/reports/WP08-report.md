# WP08 Report: Runner-Adapter für Pi Agent und DeepSeek Harness

**Status:** DONE
**Branch:** wp/08-runner-adapters
**Basis:** origin/main 4254d1b, nach Rebase auf den aktuellen Stand
**Letzter Commit:** 5acee62 docs: list the new runner adapters (dieser Report folgt als letzter Commit)

## Ergebnis in drei Sätzen

`state-runner` kennt jetzt fünf ausgelieferte Adapter: `codex`, `claude-code`, `opencode`, `pi-agent` und `deepseek-harness`. Die Aufrufsyntax der beiden neuen Adapter stammt aus den echten `--help`-Ausgaben (`pi --print` mit dem Prompt als Positionsargument, `dsh --profile headless` mit dem Task als Positionsargument), der Prompt bleibt in beiden Fällen genau ein argv-Element ohne Shell. Eine serverseitige Allowlist für Ausführungs-Adapter gibt es nicht, deshalb war in `internal/state` keine Änderung nötig; die wirksame Sperre ist die Registry in `DefaultAdapters()`, und die neuen Tests decken beide Ebenen ab.

## Erledigte Tasks

- [x] Task 0: Echte Aufrufsyntax ermittelt. Vollständige Ausgabe in Abschnitt "Task 0: Aufrufsyntax" unten.
- [x] Task 1: Server-Allowlist geprüft. Es gibt keine Allowlist für Ausführungs-Adapter, daher keine Produktionsänderung in `internal/state` und kein eigener Commit. Nachweis: `TestPolicyValidationAcceptsNewAdapterLabels` in `internal/runner/adapters_test.go` lief schon vor der Registrierung der Adapter grün, weil `ValidPolicyConfiguration` den Adapter nur über das Regex `ValidHarness` prüft.
- [x] Task 2: `pi-agent` und `deepseek-harness` in `DefaultAdapters()` registriert, Test zuerst (rot gesehen, danach grün).
- [x] Task 3: Kommentar über `DefaultAdapters()` erweitert, `README.md` im Abschnitt "Scheduled agent execution" ergänzt.
- [x] Task 4: `gofmt`, `go vet`, `go test -race ./...` siehe Prüfungen.
- [x] Report und Draft-PR.

## Geänderte Dateien

| Datei | Änderung |
| --- | --- |
| `internal/runner/adapters.go` | Zwei Einträge in `DefaultAdapters()` (`pi-agent` mit Binary `pi` und `-p`, `deepseek-harness` mit Binary `dsh` und `--profile headless`), Kommentar über der Funktion erweitert |
| `internal/runner/adapters_test.go` | `TestDefaultAdaptersIncludePiAgent`, `TestDefaultAdaptersIncludeDeepSeekHarness`, `TestShippedAdaptersReportMissingBinary`, `TestPolicyValidationAcceptsNewAdapterLabels`, Hilfsfunktion `shippedAdapterNames()`, bestehender Test `TestDefaultAdaptersContainShippedAdaptersOnly` auf die fünf Namen erweitert |
| `README.md` | Adapter-Liste und Aufruf der beiden neuen Adapter im Abschnitt "Scheduled agent execution" |

## Dateien außerhalb meines Bereichs

| Datei | Grund |
| --- | --- |
| `README.md` | Von Task 3 ausdrücklich verlangt. Der Abschnitt "Scheduled agent execution" ist die einzige Stelle, an der der Runner seine Adapter beschreibt. Der einzige `claude-code`-Treffer in der README (Zeile 93) beschreibt dagegen die Harness-Integration von `statectl` und wurde deshalb nicht angefasst. |
| `docs/plans/2026-09-23-product-lineup/reports/WP08-report.md` | Dieser Report, nach Master-Plan Abschnitt 4.4. |

Nicht geändert, obwohl im WP-Dokument als Möglichkeit genannt: `internal/state/execution_models.go`, `internal/state/execution.go`, `internal/state/harness.go`. Begründung in "Abweichungen vom Plan".

## Task 0: Aufrufsyntax

Ausgeführt am 23.09.2026 auf Fabians Mac, Ausgabe wörtlich:

```text
$ command -v pi && pi --help | head -60
/Users/nicremo/.npm-global/bin/pi
pi - AI coding assistant with read, bash, edit, write tools

Usage:
  pi [options] [--] [@files...] [messages...]

Options:
  ...
  --print, -p                    Non-interactive mode: process prompt and exit
  ...
$ pi --version
0.85.1

$ command -v deepseek-harness
(exit 1, kein Binary dieses Namens)

$ command -v dsh
/Users/nicremo/.npm-global/bin/dsh
$ dsh --version
0.1.2-rc.1
$ dsh --help
Usage: dsh [options] [command] [args...]

dsh: boot a DeepSeek Harness profile - an ordered stack of plugin-bundle patch
layers under your own overrides.
...
Examples:
  dsh --profile headless "run the tests"     answer one task, print the result, and exit
$ dsh --profile headless --help
Usage: dsh --profile headless [options] [task...]

Answer one task, stream reasoning to stderr, print the final assistant message,
and exit.

Arguments:
  task        the task text; multiple words are joined by spaces

$ ls ~/.local/bin ~/bin /opt/homebrew/bin 2>/dev/null | grep -iE "^pi$|deepseek|harness"
deepseek

$ grep -nE "^(alias|function) .*(pi|deepseek|harness)" ~/.zshrc
543:alias deepseek="$HOME/.local/bin/deepseek"
544:alias dsh="$HOME/.local/bin/deepseek"
```

Ableitung je Agent:

| Agent | Adapter-Name | Binary | Nicht-interaktiver Aufruf | argv |
| --- | --- | --- | --- | --- |
| Pi Agent | `pi-agent` | `pi` | `--print, -p` laut `--help`, Prompt ist eine Positions-Message laut Usage-Zeile | `["-p", prompt]` |
| DeepSeek Harness | `deepseek-harness` | `dsh` | `dsh --profile headless "<task>"`, laut `--help` "answer one task, print the result, and exit" | `["--profile", "headless", prompt]` |

Anmerkungen zu Task 0:

1. `deepseek-harness` ist kein Binary. Der offizielle CLI-Name des DeepSeek Harness ist `dsh` (`/Users/nicremo/.npm-global/bin/dsh`, Version 0.1.2-rc.1). Der Adapter-Name bleibt trotzdem `deepseek-harness`, weil das das Harness-Label aus WP05 und der Registry-Name ist; nur das Binary heißt `dsh`. Ein Wrapper-Skript wurde nicht gebaut.
2. `~/.local/bin/deepseek` ist ein Shell-Wrapper und startet laut eigenem Kopfkommentar nur die Web-UI (`dsh web`). Er hat keinen nicht-interaktiven Modus und kommt für den Runner nicht in Frage.
3. Die Aliase in `~/.zshrc` (Zeilen 543 und 544) zeigen, wie Fabian den Harness normal aufruft: `deepseek` und `dsh` zeigen beide auf `~/.local/bin/deepseek`. Für den Runner zählen sie nicht, weil er ohne Shell startet. In einer Nicht-Login-Shell löst `command -v dsh` deshalb auf `/Users/nicremo/.npm-global/bin/dsh` auf, also auf den echten Launcher.
4. Hinweis zur Sorgfalt: der weite Grep-Ausdruck aus dem WP-Dokument hat zusätzlich eine unabhängige Alias-Zeile mit Zugangsdaten getroffen. Diese Zeile ist hier bewusst nicht zitiert und nicht in den Report übernommen.
5. Keine echte Agent-Session gestartet. Es wurden nur `--help`, `--version` und `command -v` benutzt.

## Task 1: Server-Allowlist

Suchbefehl und Ergebnis:

```text
$ grep -rn '"claude-code"' internal/state internal/api internal/mcpserver
internal/state/harness.go:12:var knownHarnesses = []string{"codex", "claude-code", "opencode"}
internal/state/service_test.go:20, internal/state/execution_test.go:396,
internal/state/harness_test.go:8, internal/api/handler_test.go:27,72,
internal/mcpserver/multi_agent_test.go:32, internal/mcpserver/server_test.go:131,157
```

Es gibt **keine** Allowlist für Ausführungs-Adapter:

1. `ValidPolicyConfiguration` prüft `policy.Adapter` mit `ValidHarness` (`internal/state/execution_models.go:104`).
2. `validateRunnerScopes` prüft die Adapter eines Runners ebenfalls mit `ValidHarness` (`internal/state/execution.go:405`).
3. `ValidHarness` ist ein Regex: `^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$` (`internal/state/harness.go:8,15`). `pi-agent` und `deepseek-harness` erfüllen es.
4. `knownHarnesses` (`internal/state/harness.go:12`) ist **keine** Adapter-Allowlist, sondern die Liste der Harnesses, für die `statectl` die Konfiguration selbst schreibt (`KnownHarness`, benutzt in `internal/statectl/installer.go:68` und `:106`). Bewusst nicht erweitert: WP05 legt `pi-agent` und `deepseek-harness` ausdrücklich als manuelle Integrationen fest (`WP05-agent-capture-rules.md`, Zeilen 125, 126, 191), und `internal/state/harness.go` sowie `internal/statectl` liegen außerhalb des WP08-Bereichs.
5. Die wirksame Sperre ist die Registry im Runner: `cmd/state-runner/main.go:131` reicht `runner.DefaultAdapters()` an den Runner durch, `internal/runner/run.go:121` sucht `runner.Adapters[run.Adapter]` und meldet sonst `adapter_unavailable` ("adapter %q is not registered in this runner").
6. `unknown-agent` wird serverseitig akzeptiert, weil es die Form eines Harness-Labels hat. Abgelehnt wird es dort, wo es zählt: `TestDefaultAdaptersContainShippedAdaptersOnly` prüft, dass die Registry nur die fünf ausgelieferten Adapter enthält und `unknown-agent` nicht registriert ist.

iOS-App, feste Adapter-Auswahl vorhanden, **nicht** geändert (Bereich von WP06), siehe "Offene Fragen":

```text
ios/State/Sources/App/AppModel.swift:21:    var adapter = "claude-code"
ios/State/Sources/App/AppModel.swift:808:            harness: "claude-code",
ios/State/Sources/App/AppModel.swift:901:            adapter: "claude-code",
ios/State/Sources/App/AppModel.swift:918:            adapters: ["claude-code", "codex"],
ios/State/Sources/Models/HarnessCatalog.swift:38:    static let shippedIntegrations: Set<String> = ["codex", "claude-code", "opencode"]
```

## Prüfungen

| Befehl | Ergebnis |
| --- | --- |
| `gofmt -l ./cmd ./internal` | leer |
| `go vet ./...` | grün |
| `go test -race ./...` | grün, 15 Pakete, Ausgabe unten |
| `go test ./internal/runner/ -run ... -v` (rot vor der Implementierung) | `pi-agent adapter missing`, `deepseek-harness adapter missing`, `DefaultAdapters() misses pi-agent` |
| `go test ./internal/runner/ -run ... -v` (grün nach der Implementierung) | 5 Tests grün |

Der Beweis, dass die neuen Tests wirklich greifen: nach dem Schreiben der Tests und vor der Implementierung schlugen `TestDefaultAdaptersIncludePiAgent`, `TestDefaultAdaptersIncludeDeepSeekHarness`, `TestShippedAdaptersReportMissingBinary` und `TestDefaultAdaptersContainShippedAdaptersOnly` fehl. `TestPolicyValidationAcceptsNewAdapterLabels` lief bereits vorher grün, was der Beleg für Task 1 ist.

Die Prüfungen liefen zweimal: einmal auf dem ursprünglichen Basis-Commit `ae6ab23` und nach dem Rebase auf den aktuellen `origin/main` (`4254d1b`) erneut. Beide Läufe waren vollständig grün. Ausgabe des zweiten Laufs:

```text
$ gofmt -l ./cmd ./internal
(keine Ausgabe)

$ go vet ./...
(keine Ausgabe, Exit 0)

$ go test -race ./...
ok  	github.com/nicremo/state/cmd/state-relay	(cached)
ok  	github.com/nicremo/state/cmd/state-runner	1.595s
ok  	github.com/nicremo/state/cmd/state-server	2.752s
ok  	github.com/nicremo/state/cmd/statectl	2.066s
ok  	github.com/nicremo/state/internal/api	6.006s
ok  	github.com/nicremo/state/internal/auth	5.028s
ok  	github.com/nicremo/state/internal/mcpserver	5.490s
ok  	github.com/nicremo/state/internal/push	5.520s
ok  	github.com/nicremo/state/internal/pushcrypto	(cached)
ok  	github.com/nicremo/state/internal/relay	(cached)
ok  	github.com/nicremo/state/internal/runner	6.658s
ok  	github.com/nicremo/state/internal/securefile	(cached)
ok  	github.com/nicremo/state/internal/state	(cached)
ok  	github.com/nicremo/state/internal/statectl	4.406s
ok  	github.com/nicremo/state/internal/store	(cached)
```

## Abweichungen vom Plan

1. **Kein Commit für Task 1.** Das WP-Dokument rechnete mit einer serverseitigen Adapter-Liste. Die gibt es nicht, also wurde nach der Regel "Wenn es keine Liste gibt: nichts tun" verfahren und nur der Nachweis als Test ergänzt. Der Test liegt in `internal/runner/adapters_test.go`, weil `internal/state/execution_test.go`, `harness_test.go` und `internal/statectl` nicht in meinem Bereich liegen.
2. **`unknown-agent` wird serverseitig nicht abgelehnt.** Das WP-Dokument nahm an, eine Policy mit `unknown-agent` werde weiterhin abgelehnt. Realität: `ValidPolicyConfiguration` akzeptiert jedes Label in Harness-Form, auch `unknown-agent`. Die Ablehnung passiert erst im Runner über die Registry. Der Test prüft deshalb genau diese Grenze, die im Code existiert.
3. **`deepseek-harness` ist kein Binary, sondern `dsh`.** Das WP-Dokument ließ offen, welches Binary gemeint ist. Die Adapter-Beschriftung bleibt `deepseek-harness`, das Binary ist `dsh`.
4. **README.** Task 3 verlangt, "dort, wo die Runner-Adapter aufgezählt werden", die neuen Namen zu ergänzen. Eine solche Aufzählung existierte in der README nicht, auch kein `claude-code`-Treffer im Runner-Abschnitt. Statt einer falschen Ergänzung bei den `statectl`-Harnesses (Zeile 84, dort geht es um automatische Konfiguration, die es für die beiden neuen Agenten laut WP05 nicht gibt) wurde die Adapter-Liste im Abschnitt "Scheduled agent execution" ergänzt.
5. **Commit-Zuschnitt.** Der Kommentar über `DefaultAdapters()` wurde zusammen mit der Registry im Commit `feat: launch pi agent and deepseek harness from the runner` geändert, nicht erst im Doku-Commit. Inhaltlich gehört er zur Registrierung, und der Doku-Commit `docs: list the new runner adapters` enthält die README.
6. **Basis-Commit.** Der Worktree wurde nach Master-Plan Abschnitt 4.1 von `origin/main` angelegt, damals `ae6ab23`. Während der Arbeit hat der Koordinator die Welle 1 vorbereitet und `origin/main` auf `4254d1b` gebracht (PR #38, Integration des lokalen Mac-Servers und der Pläne). Der Branch wurde deshalb auf `4254d1b` rebased, ohne Konflikte, und alle Prüfungen liefen danach erneut. `internal/runner`, `internal/state`, `cmd/state-runner` und die README-Änderung betreffen nur die README, die neuen Zeilen liegen weiter oben und kollidieren nicht mit meiner Änderung.

## Offene Fragen und Risiken

1. **iOS-Auswahl ist fest und kennt die neuen Adapter nicht.** `ios/State/Sources/Models/HarnessCatalog.swift:38` listet nur `codex`, `claude-code`, `opencode`, und `AppModel.swift:918` bietet nur `["claude-code", "codex"]` zur Auswahl an. Solange das so bleibt, kann eine Policy über die App nicht auf `pi-agent` oder `deepseek-harness` zeigen, obwohl der Server sie akzeptiert. Das ist Bereich von WP06 und wurde hier bewusst nicht angefasst. Der Koordinator sollte entscheiden, ob WP06 oder ein Folge-WP die Auswahl erweitert.
2. **PATH des Runners.** Beide neuen Binaries liegen unter `/Users/nicremo/.npm-global/bin`. Startet der Runner aus einem LaunchAgent, sieht er diesen Pfad möglicherweise nicht und meldet dann korrekt `adapter_unavailable`. Das betrifft alle fünf Adapter gleichermaßen und gehört zu WP10 (Runner in der Mac-App). Der Runner hat heute keine Option für absolute Binary-Pfade.
3. **`pi -p` ist ein Schalter, der Prompt ist eine Positions-Message.** Die `pi`-Usage-Zeile erlaubt mehrere Messages (`[messages...]`). Der Adapter übergibt genau ein Element. Wenn `pi` in einer künftigen Version `-p` zu einem werttragenden Schalter ändert, muss die Argumentliste angepasst werden. Beide Tests pinnen die Argumentliste, ein solcher Bruch fällt also sofort auf.
4. **`deepseek-harness` ist an das Profil `headless` gebunden.** Das Profil existiert auf diesem Mac (`/Users/nicremo/.dsh/profiles/headless/`). Fehlt es auf einem anderen Rechner, startet `dsh` nicht mit einem sinnvollen Fehler, der Runner meldet dann einen Exit-Code statt `adapter_unavailable`.

## Manuelle Schritte für Fabian oder den Koordinator

1. Nichts an der laufenden Konfiguration von Fabian ändern. Es wurden keine echten Agent-Sessions gestartet und keine Dateien in `~/.dsh`, `~/.codex` oder `~/.claude.json` angefasst.
2. Beim Test auf einem anderen Rechner prüfen, ob `pi` und `dsh` im PATH des Runner-Prozesses liegen.
3. Entscheiden, ob die iOS-Adapter-Auswahl (WP06) die beiden neuen Namen bekommt.
