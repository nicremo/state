# WP08: Runner-Adapter für Pi Agent und DeepSeek Harness

**Welle:** 1 · **Slug:** `runner-adapters` · **Branch:** `wp/08-runner-adapters`
**Besitzt:** `internal/runner/adapters.go`, `internal/runner/adapters_test.go`, und nur falls nötig die Adapter-Allowlist in `internal/state/execution_models.go` bzw. `internal/state/execution.go` (siehe Task 1)
**Geschätzter Umfang:** klein (Go)

## Goal-Prompt (so an die Worker-Session geben)

> Lies `docs/plans/2026-09-23-product-lineup/README.md` vollständig und halte dich an das Arbeitsprotokoll in Abschnitt 4. Setze danach `docs/plans/2026-09-23-product-lineup/WP08-runner-adapters.md` Task für Task mit TDD um. Du startest keine echte Agent-Session mit einem echten Auftrag. Schließe mit Report und Draft-PR ab.

## Ziel

`state-runner` kann neben `codex`, `claude-code` und `opencode` auch **Pi Agent** und **DeepSeek Harness** als Adapter starten, damit fällige Aufgaben mit diesen Agenten ausgeführt werden können.

## Pflichtlektüre

1. `internal/runner/adapters.go` (vollständig: `cliAdapter`, `DefaultAdapters`, `BuildPrompt`, Validierung)
2. `internal/runner/adapters_test.go`
3. `internal/state/execution_models.go` und `internal/state/execution.go`: Suche, ob Adapter-Namen serverseitig gegen eine Liste geprüft werden:
   ```bash
   grep -rn '"claude-code"' internal/state internal/api internal/mcpserver
   ```
4. `docs/universal-agent-todo-capture.md` Abschnitt 10 (Policy-Grenzen)

## Task 0: Echte Aufrufsyntax ermitteln (nicht raten)

Führe aus und kopiere die relevanten Zeilen in den Report:

```bash
command -v pi && pi --help 2>&1 | head -60
command -v deepseek-harness && deepseek-harness --help 2>&1 | head -60
command -v dsh && dsh --help 2>&1 | head -60
ls ~/.local/bin ~/bin /opt/homebrew/bin 2>/dev/null | grep -iE "^pi$|deepseek|harness"
grep -nE "^(alias|function) .*(pi|deepseek|harness)" ~/.zshrc 2>/dev/null
```

Gesucht wird je Agent: **Name des Binaries** und der **nicht-interaktive Modus**, der einen Prompt als Argument annimmt, die Arbeit im aktuellen Verzeichnis erledigt und sich danach mit einem Exit-Code beendet (vergleichbar mit `claude -p <prompt>` oder `codex exec <prompt>`).

Regeln:
- Nimm nur Optionen, die in der `--help`-Ausgabe stehen.
- Wenn ein Agent keinen solchen Modus hat oder das Binary nicht installiert ist: Adapter für diesen Agenten **nicht** bauen, im Report begründen. Kein Wrapper-Skript erfinden.
- Aliase und Shell-Funktionen aus `~/.zshrc` gelten nicht als Binary (der Runner startet ohne Shell). Notiere sie aber im Report, weil sie zeigen, wie Fabian den Agenten normal aufruft.

## Task 1: Adapter-Namen festlegen und Server-Allowlist prüfen

Adapter-Namen: `pi-agent` und `deepseek-harness` (gleiche Labels wie die Harness-Labels in `docs/agent-integration.md` und WP05).

Wenn Pflichtlektüre Punkt 3 eine serverseitige Liste erlaubter Adapter zeigt: die zwei Namen dort ergänzen, **Test zuerst** (in der passenden `*_test.go`, z.B. ein Policy mit Adapter `pi-agent` wird akzeptiert, `unknown-agent` weiterhin abgelehnt). Wenn es keine Liste gibt: nichts tun, im Report festhalten.

Prüfe außerdem, ob die iOS-App eine feste Adapter-Auswahl hat (`grep -rn "claude-code" ios/State/Sources`). Wenn ja: **nicht** ändern (Bereich von WP06), aber im Report unter "Offene Fragen" für den Koordinator notieren.

Commit (falls Änderung): `git commit -m "feat: allow pi agent and deepseek harness as execution adapters"`

## Task 2: Adapter registrieren

**Test zuerst** in `internal/runner/adapters_test.go`. Orientiere dich an den bestehenden Tests für `codex` und `claude-code` (gleicher Stil, gleiche Hilfsfunktionen). Beispiel, **Argumente an die in Task 0 gefundene Syntax anpassen**:

```go
func TestDefaultAdaptersIncludePiAgent(t *testing.T) {
    adapter, ok := DefaultAdapters()["pi-agent"]
    if !ok {
        t.Fatal("pi-agent adapter missing")
    }
    cli, ok := adapter.(*cliAdapter)
    if !ok {
        t.Fatalf("pi-agent adapter has type %T", adapter)
    }
    if cli.binary != "pi" {
        t.Fatalf("binary = %q", cli.binary)
    }
    if got := cli.args("do the thing"); !reflect.DeepEqual(got, []string{"-p", "do the thing"}) {
        t.Fatalf("args = %#v", got)
    }
}
```

Genauso `TestDefaultAdaptersIncludeDeepSeekHarness`.

Wenn die bestehenden Tests prüfen, dass ein fehlendes Binary zu `ErrAdapterUnavailable` führt: einen Test ergänzen, der das auch für `pi-agent` zeigt (mit leerem `PATH` über `t.Setenv("PATH", t.TempDir())`).

Implementierung in `DefaultAdapters()` nach dem Muster der vorhandenen Einträge:

```go
"pi-agent": &cliAdapter{
    slug:   "pi-agent",
    binary: "pi",
    args:   func(prompt string) []string { return []string{"-p", prompt} },
},
```

(Werte aus Task 0.) Der Prompt bleibt **ein** argv-Element. Niemals Shell-Strings bauen, niemals `sh -c`.

Commit: `git commit -m "feat: launch pi agent and deepseek harness from the runner"`

## Task 3: Doku und Hinweise

Im Kommentar über `DefaultAdapters()` die zwei neuen Adapter ergänzen, und in `README.md` (Datei außerhalb deines Bereichs, im Report nennen) dort, wo die Runner-Adapter aufgezählt werden (`grep -n "claude-code" README.md`), die neuen Namen ergänzen.

Commit: `git commit -m "docs: list the new runner adapters"`

## Task 4: Gesamtprüfung

```bash
gofmt -l ./cmd ./internal
go vet ./...
go test -race ./...
```

Report und Draft-PR nach Master-Plan Abschnitt 4.4 und 4.5. Im Report die `--help`-Auszüge aus Task 0.
