# State Product Lineup: Master-Plan und Arbeitsprotokoll

**Stand:** 23.09.2026
**Koordinator:** Die Hauptsession (Review, Fixes, Merges, Deployments, Releases)
**Worker:** Einzelne Sessions, je ein Arbeitspaket (WP), je ein eigener Git-Worktree

Diese Datei ist die Grundlage für jedes Arbeitspaket. Jeder Worker liest sie VOLLSTÄNDIG, bevor er mit seinem WP anfängt.

---

## 1. Ziel

State wird so fertiggestellt, dass Fabian es täglich nutzen kann. Das Produkt besteht aus vier Teilen:

| Produkt | Was es ist | Code heute |
| --- | --- | --- |
| **State für iPhone und iPad** | Die SwiftUI-App. iPhone mit Tabs, iPad mit Seitenleiste | `ios/State` (Target `State`, iOS 18) |
| **State für Mac** (Laptop-App) | Dieselbe App als natives macOS-Target, für den Mac optimiert. Optional der Agent-Runner für diesen Mac | Neu: Target `StateMac` in `ios/project.yml` |
| **State Mac Server** | Menüleisten-App, die nur den Go-Server lokal startet (LAN, QR-Pairing mit Zertifikats-Pin) | `macos/` + `cmd/state-server/desktop.go` |
| **State VPS Server** | Derselbe Go-Server plus `state-relay` für Push, als Docker Compose hinter Traefik | `deploy/` |

Grundprinzip: **ein Server-Code, ein Client-Code.** Mac Server und VPS Server sind dasselbe Go-Binary `state-server` (Modus `desktop` bzw. `serve`). iPhone, iPad und Mac teilen denselben Swift-Code.

Die fachliche Spezifikation der Agenten-Erfassung steht in `docs/universal-agent-todo-capture.md`. Das Architektur-Dokument zum Produkt-Lineup entsteht in WP01 als `docs/product-lineup.md`.

## 2. Festgelegte Architektur-Entscheidungen

Diese Entscheidungen sind verbindlich. Kein Worker ändert sie. Wer glaubt, dass eine davon falsch ist, schreibt das in seinen Report unter "Offene Fragen" und arbeitet trotzdem danach.

1. **Mac-Client ist ein natives macOS-Target**, kein Mac Catalyst und kein "Designed for iPad". Gleiche Bundle-ID `com.fabincrm.state`, damit ein App-Store-Eintrag alle Plattformen abdeckt.
2. **iPad und Mac nutzen `NavigationSplitView`** (Seitenleiste + Liste + Detail). Das iPhone behält die `TabView`.
3. **Der Runner (`state-runner`) läuft als eigener LaunchAgent des Benutzers** (`state-runner service install`), nicht im App-Bundle. Grund: Die Mac-App kommt über TestFlight und den App Store und läuft deshalb in der App Sandbox. Aus der Sandbox heraus darf sie weder `claude`, `codex` oder `opencode` in Projektordnern starten noch LaunchAgents anlegen. Die App (iPhone, iPad, Mac) zeigt den Runner-Status und liefert Pairing-Code und den fertigen Installationsbefehl. Die Mac-Server-App bleibt reiner Server.
4. **Ein Client ist mit genau einem Server verbunden.** Wechsel über die Einstellungen, keine parallelen Server-Verbindungen.
5. **Push:** Nur iPhone und iPad bekommen APNs-Pushes über `state-relay` (App Attest ist Voraussetzung, das gibt es auf dem Mac praktisch nicht). Die Mac-App arbeitet mit lokalen Mitteilungen aus synchronisierten Daten und Sync-Polling, solange sie läuft.
6. **Mac Server und VPS-Relay dürfen kombiniert werden:** Der Mac Server kann optional ein öffentliches Relay (auf dem VPS) für Pushes nutzen. Die Relay-Adresse muss dafür im iPhone einstellbar sein (heute wird sie aus der Server-Adresse abgeleitet: `state.X` wird zu `relay.X`).
7. **Sicherheitsgrenzen aus `docs/universal-agent-todo-capture.md` gelten weiter:** Der Server schickt nie Shell-Befehle an Rechner, der Runner holt sich Arbeit nur ausgehend ab, keine Secrets in Reminders, `.state/` oder Push-Payloads.

## 3. Arbeitspakete und Wellen

Pakete einer Welle laufen **gleichzeitig** in getrennten Worktrees. Die nächste Welle startet erst, wenn der Koordinator die vorherige gemergt hat.

### Welle 1 (sofort parallel)

| WP | Datei | Titel | Besitzt (darf ändern) |
| --- | --- | --- | --- |
| WP01 | `WP01-product-lineup-doc.md` | Architektur-Dokument | `docs/product-lineup.md`, `README.md` (nur Abschnitt Components) |
| WP02 | `WP02-mac-server-app.md` | Mac Server App stabil machen | `macos/**`, `cmd/state-server/desktop.go`, `cmd/state-server/desktop_test.go`, `Package.swift` |
| WP03 | `WP03-vps-deploy-kit.md` | VPS Deploy-Kit | `deploy/**`, `docs/deploy-vps.md`, `scripts/vps/**` |
| WP04 | `WP04-statectl-reminder-cli.md` | `statectl reminder` Befehle | `cmd/statectl/main.go`, `cmd/statectl/reminder.go`, `cmd/statectl/reminder_test.go`, `internal/statectl/reminder.go`, `internal/statectl/reminder_test.go` |
| WP05 | `WP05-agent-capture-rules.md` | Capture-Regeln + manuelle Harness-Integration | `internal/statectl/rules.go`, `internal/statectl/rules_test.go`, `internal/statectl/installer.go`, `internal/statectl/installer_test.go`, `internal/mcpserver/server.go` (nur Tool-Beschreibungen), `docs/agent-integration.md` |
| WP06 | `WP06-macos-client-target.md` | macOS-Target + Plattform-Abstraktion | `ios/project.yml`, `ios/State.xcodeproj/**` (nur per XcodeGen), `ios/State/Sources/**`, `ios/StateMac/**`, `ios/StateTests/**` |
| WP08 | `WP08-runner-adapters.md` | Runner-Adapter für Pi Agent und DeepSeek Harness | `internal/runner/adapters.go`, `internal/runner/adapters_test.go` |

### Welle 2 (nach Merge von Welle 1)

| WP | Datei | Titel | Abhängig von |
| --- | --- | --- | --- |
| WP07 | `WP07-relay-for-local-server.md` | Relay-URL einstellbar, Mac Server mit VPS-Relay | WP02, WP06 |
| WP09 | `WP09-adaptive-layout.md` | iPad- und Mac-Layout mit `NavigationSplitView` | WP06 |
| WP10 | `WP10-runner-in-mac-app.md` | Runner als LaunchAgent, Status und Befehl in der App | WP06 |

### Welle 3 (nach Merge von Welle 2)

| WP | Datei | Titel | Abhängig von |
| --- | --- | --- | --- |
| WP11 | `WP11-release-pipeline.md` | Fastlane für macOS + iPad, TestFlight vorbereiten | WP06, WP09, WP10 |

### Nur Koordinator (nicht an Worker vergeben)

- Integrations-Branch `feat/local-mac-server-and-ios-refresh` nach `main` bringen (Phase 1).
- Dependabot-PRs #29 bis #33 prüfen und mergen.
- Echtes VPS-Deployment mit Domain, TLS, APNs-Key (nutzt das Kit aus WP03).
- `statectl` auf Fabians Mac installieren und Claude Code, Codex, OpenCode, DeepSeek Harness, Pi Agent pairen.
- TestFlight-Uploads (brauchen Apple-Zugangsdaten).
- Review, Fixes und Merge jedes WP.

## 4. Arbeitsprotokoll für Worker (VERPFLICHTEND)

### 4.1 Vorbereitung

```bash
cd ~/Desktop/state
git fetch origin
git config user.name    # muss "Fabian" liefern
git config user.email   # muss fb200386@gmail.com liefern
```

Wenn `user.name` oder `user.email` leer ist: **STOPP.** Nichts committen, im Report als BLOCKED melden.

Worktree anlegen. `<nn>` ist die WP-Nummer, `<slug>` steht in deinem WP-Dokument:

```bash
git worktree add ~/Desktop/state-worktrees/wp<nn>-<slug> -b wp/<nn>-<slug> origin/main
cd ~/Desktop/state-worktrees/wp<nn>-<slug>
```

Ab jetzt arbeitest du **ausschließlich** in diesem Worktree. Nie in `~/Desktop/state` selbst, nie auf `main`.

### 4.2 Regeln während der Arbeit

1. **Nur die Dateien ändern, die dein WP unter "Besitzt" auflistet.** Musst du eine andere Datei ändern, mach es minimal und nenne es im Report unter "Dateien außerhalb meines Bereichs" mit Begründung. Andere Worker arbeiten parallel an anderen Dateien, Übergriffe erzeugen Merge-Konflikte.
2. **Test zuerst (TDD):** Für jede Verhaltensänderung erst einen fehlschlagenden Test schreiben, ausführen, Fehlschlag sehen, dann implementieren.
3. **Nach jedem Task committen.** Format: `feat: ...`, `fix: ...`, `test: ...`, `docs: ...`, `refactor: ...`, `chore: ...`. Englisch, Kleinbuchstaben nach dem Doppelpunkt.
4. **Keine KI-Attribution**, nirgends: kein `Co-Authored-By`, kein "generated by", kein Modellname in Commits, Code, Kommentaren oder Doku.
5. **Code-Kommentare auf Englisch**, im Stil der umgebenden Datei. Keine Kommentare, die nur den Code nacherzählen.
6. **Keine Secrets anfassen:** keine `.env`, kein `~/.ssh`, keine Keychain-Einträge auslesen, keine echten Tokens in Tests.
7. **Keine echten Integrationen verändern:** Du veränderst nie `~/.claude.json`, `~/.codex/config.toml`, `~/.config/opencode/*`, `~/Library/LaunchAgents/*` oder die laufende `State Server.app`. Tests arbeiten immer mit temporären Verzeichnissen (`t.TempDir()` in Go).
8. **Kein Deployment, kein Push auf `main`, kein Merge, kein Force-Push.** Das macht der Koordinator.
9. **Nicht raten, sondern nachsehen:** Wenn dein Plan einen Typ oder eine Funktion nennt, öffne die genannte Datei und prüfe Namen und Felder, bevor du Code schreibst. Weicht die Realität vom Plan ab, gilt die Realität. Notiere die Abweichung im Report.
10. **Bei Blockade:** Drei ernsthafte Versuche pro Problem. Danach nicht weiter herumprobieren, sondern den Stand committen, das Problem im Report genau beschreiben (Befehl, vollständige Fehlermeldung, was versucht wurde) und das WP als BLOCKED oder PARTIAL abschließen.

### 4.3 Pflicht-Prüfungen vor dem Abschluss

Jedes WP nennt eigene Prüfbefehle. Zusätzlich gilt für alle, deren Änderungen Go berühren:

```bash
gofmt -l ./cmd ./internal     # muss leer sein
go vet ./...
go test -race ./...
```

Für alle, deren Änderungen Swift im `ios/`-Ordner berühren:

```bash
cd ios && xcodegen generate && cd ..
xcodebuild -project ios/State.xcodeproj -scheme State \
  -destination 'platform=iOS Simulator,name=iPhone 16 Pro,OS=18.5' \
  -derivedDataPath build/DerivedData-wp<nn> \
  CODE_SIGNING_ALLOWED=NO test
```

Alle Befehle müssen grün sein. Rote Tests, die schon auf `origin/main` rot waren, im Report mit Nachweis nennen (Befehl auf einem frischen Checkout von `origin/main` ausgeführt).

### 4.4 Report schreiben

Datei: `docs/plans/2026-09-23-product-lineup/reports/WP<nn>-report.md` im eigenen Branch. Sprache: Deutsch, korrekte Umlaute (ä ö ü ß), **keine Gedankenstriche** (weder Halbgeviertstrich noch Geviertstrich), nur Punkt, Komma, Doppelpunkt, Bindestrich.

Vorlage:

```markdown
# WP<nn> Report: <Titel>

**Status:** DONE | PARTIAL | BLOCKED
**Branch:** wp/<nn>-<slug>
**Letzter Commit:** <kurzer Hash> <Commit-Titel>

## Ergebnis in drei Sätzen

## Erledigte Tasks
- [x] Task 1: ...
- [ ] Task 3: ... (Grund, warum nicht erledigt)

## Geänderte Dateien
| Datei | Änderung |
| --- | --- |

## Dateien außerhalb meines Bereichs
(keine) oder Liste mit Begründung

## Prüfungen
| Befehl | Ergebnis |
| --- | --- |
| go test -race ./... | grün, 17 Pakete |

## Abweichungen vom Plan
Was im Plan anders stand als im Code vorgefunden, und wie entschieden wurde.

## Offene Fragen und Risiken
Was der Koordinator entscheiden oder prüfen muss.

## Manuelle Schritte für Fabian oder den Koordinator
z.B. "APNs-Key muss nach /srv/state/secrets kopiert werden".
```

### 4.5 Abschluss

```bash
git add docs/plans/2026-09-23-product-lineup/reports/WP<nn>-report.md
git commit -m "docs: add WP<nn> report"
git status            # muss sauber sein
git push -u origin wp/<nn>-<slug>
gh pr create --draft --base main --head wp/<nn>-<slug> \
  --title "WP<nn>: <Titel>" \
  --body-file docs/plans/2026-09-23-product-lineup/reports/WP<nn>-report.md
```

Danach ist das WP für dich beendet. Den Worktree nicht löschen, der Koordinator räumt auf.

## 5. Aufgaben des Koordinators

1. Vor Welle 1: Integrations-Branch mergen, Pläne auf `main` bringen, Dependabot erledigen.
2. Pro eingegangenem Draft-PR:
   1. Report lesen, Diff lesen (`gh pr diff <nr>`).
   2. Im Worktree des WP alle Prüfbefehle selbst ausführen.
   3. Fehler selbst korrigieren (Commits auf dem WP-Branch) oder den Worker mit einer konkreten Liste nacharbeiten lassen.
   4. Mit aktuellem `main` abgleichen: `git merge origin/main`, Konflikte lösen, Tests erneut.
   5. PR auf "ready" setzen und per Squash mergen, Branch löschen, Worktree entfernen.
3. Merge-Reihenfolge in Welle 1 (geringstes Konfliktrisiko zuerst): WP01, WP08, WP03, WP05, WP04, WP02, WP06.
   - WP04 und WP05 berühren beide `internal/statectl`, aber verschiedene Dateien. Nach dem Merge von WP04 ergänzt der Koordinator in `DefaultAgentRules()` (WP05) einen Satz zum CLI-Fallback `statectl reminder`.
4. Erst wenn Welle 1 komplett auf `main` ist: Welle 2 freigeben. Dann Welle 3.
5. Nach Welle 3: VPS-Deployment, Agenten-Pairing auf Fabians Mac, TestFlight, Abnahmetest (siehe Abschnitt 6).

## 6. Abnahme des Gesamtziels

Das Gesamtziel ist erreicht, wenn alle Punkte nachweislich funktionieren:

1. Die Mac Server App startet beim Login, lauscht auf 9847/9848, überlebt Ruhezustand und Neustart, und ein iPhone koppelt per QR.
2. Der VPS-Server läuft unter einer echten Domain mit TLS, Relay mit echtem APNs, und ein iPhone bekommt einen Push, auch außerhalb des WLANs.
3. Die iPhone-App, die iPad-App (Seitenleiste) und die Mac-App (natives Target) laufen aus demselben Code, alle als TestFlight-Build.
4. Claude Code, Codex, OpenCode, DeepSeek Harness und Pi Agent sind je mit eigener Identität gekoppelt. Ein Satz wie "ich muss jeden Monat ein Monatsreporting machen" im Chat erzeugt nach Rückfrage einen monatlichen Reminder.
5. `statectl reminder create` funktioniert als Ausweichweg ohne MCP.
6. Ein fälliger Reminder mit Policy startet über den Runner der Mac-App eine Agent-Session im richtigen Projektordner, und das Ergebnis erscheint auf dem iPhone.

## 7. Schnellstart: Goal-Prompts zum Kopieren

Jede Worker-Session startet im Repo `~/Desktop/state` und bekommt genau einen dieser Prompts. Welle 2 und 3 erst freigeben, wenn der Koordinator das meldet.

**Welle 1 (alle sieben gleichzeitig):**

```text
Lies docs/plans/2026-09-23-product-lineup/README.md vollständig und halte dich strikt an das Arbeitsprotokoll in Abschnitt 4 (eigener Worktree, nur eigene Dateien, TDD, Report, Draft-PR, kein Merge). Setze danach docs/plans/2026-09-23-product-lineup/WP01-product-lineup-doc.md Task für Task um.
```

Für die anderen Pakete denselben Text verwenden und nur den Dateinamen austauschen:

| Welle | Datei im Prompt |
| --- | --- |
| 1 | `WP01-product-lineup-doc.md` |
| 1 | `WP02-mac-server-app.md` |
| 1 | `WP03-vps-deploy-kit.md` |
| 1 | `WP04-statectl-reminder-cli.md` |
| 1 | `WP05-agent-capture-rules.md` |
| 1 | `WP06-macos-client-target.md` |
| 1 | `WP08-runner-adapters.md` |
| 2 | `WP07-relay-for-local-server.md` |
| 2 | `WP09-adaptive-layout.md` |
| 2 | `WP10-runner-in-mac-app.md` |
| 3 | `WP11-release-pipeline.md` |

Empfehlung zur Modellwahl: WP06 und WP09 sind die anspruchsvollsten Pakete (viele Swift-Dateien, Plattformgrenzen). Dafür das stärkste der günstigen Modelle nehmen. WP01, WP03, WP05 und WP08 sind mechanisch und eignen sich für das schnellste Modell.
