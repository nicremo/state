# CLI-04: Hilfe, Dokumentation, Regeltext und End-to-End-Test

**Welle:** 3 (erst wenn CLI-01, CLI-02 und CLI-03 auf `main` sind) · **Slug:** `docs-e2e` · **Branch:** `cli/04-docs-e2e`
**Besitzt:** `cmd/statectl/main.go` (nur Hilfetext), neu `cmd/statectl/help.go`, neu `docs/cli.md`, `README.md` (nur Abschnitt "Terminal fallback"), `internal/statectl/rules.go`, `internal/statectl/rules_test.go`, neu `scripts/cli-e2e.sh`, `cmd/statectl/errors_test.go` (neu)
**Umfang:** klein bis mittel

## Goal-Prompt

> Lies `docs/plans/2026-09-23-cli-parity/README.md` vollständig und halte dich strikt an das Arbeitsprotokoll in Abschnitt 5. Setze danach `docs/plans/2026-09-23-cli-parity/CLI-04-docs-rules-e2e.md` Task für Task um.

## Ziel

Ein Agent, der nur ein Terminal hat, findet mit `statectl --help` jeden Befehl, versteht ihn aus einem Beispiel und bekommt bei Fehlern eine Meldung, die die Ursache nennt. Die Agentenregeln verweisen auf den vollständigen Fallback.

## Task 1: `statectl --help` und `statectl help <befehl>`

**Dateien:** `cmd/statectl/help.go`, `cmd/statectl/main.go`

- `statectl`, `statectl --help` und `statectl help` geben eine Übersicht mit **allen** Befehlen aus, gruppiert: Reminder, Überblick, Runner, Einrichtung (pair, mcp, doctor, install, uninstall, rotate, revoke, unpair, project, version). Heute liefert ein Aufruf ohne Argumente nur eine einzeilige Fehlermeldung, das ersetzt die Übersicht (Exit-Code 0 bei `--help`, 1 ohne Argumente).
- `statectl help reminder complete` zeigt dieselbe Ausgabe wie `statectl reminder complete --help` plus ein Beispiel.
- Die Befehlsliste wird aus `statectl.ToolCommands` plus einer festen Liste der Einrichtungsbefehle erzeugt, damit ein neuer Befehl nicht vergessen werden kann.

Test: Die Übersicht enthält jeden Wert aus `statectl.ToolCommands`.

## Task 2: Verständliche Fehler

**Datei:** `cmd/statectl/errors_test.go` plus minimale Änderungen dort, wo Fehler entstehen

Prüfe mit gezielten Tests und passe die Meldungen an:
- Server nicht erreichbar: `cannot reach the State server at <url>: <ursache>. Is the Mac Server running?`
- Profil fehlt: `profile "<name>" not found; pair it with statectl pair or set STATECTL_PROFILE`
- `stored` nicht bestätigt: bleibt wie bisher eindeutig.
- 409 / veraltete Revision: `the reminder changed meanwhile; run the command again`

## Task 3: Doku

**Dateien:** `docs/cli.md`, `README.md`

`docs/cli.md` (Englisch): pro Befehl ein Absatz und ein kopierbares Beispiel, eine Tabelle MCP-Werkzeug zu CLI-Befehl (aus `ToolCommands`), Abschnitt "When MCP is unavailable", Abschnitt "Scripting" (`--json` mit `jq`, `STATECTL_PROFILE`, Exit-Codes). Im README den Abschnitt "Terminal fallback" auf `docs/cli.md` verweisen lassen.

## Task 4: Regeltext

**Dateien:** `internal/statectl/rules.go`, `internal/statectl/rules_test.go`

Die Zeile "Without MCP" in `DefaultAgentRules()` ersetzen durch:

```text
- Without MCP, statectl does everything the MCP tools do: statectl briefing, reminder create|update|schedule|complete|snooze|add-context|show|search, changes (statectl --help).
```

Die Längenbegrenzung aus `TestDefaultAgentRulesStayBounded` (1400 Byte) muss eingehalten bleiben. Den Pflichtbegriff im Test von `statectl reminder create` auf `statectl briefing` umstellen.

## Task 5: End-to-End-Skript

**Datei:** `scripts/cli-e2e.sh` (ausführbar, `set -euo pipefail`)

Das Skript startet einen **eigenen** Server mit temporärem Datenordner (`state-server serve --data $TMP --http 127.0.0.1:18090`), richtet über das Bootstrap-Token einen Owner ein, koppelt ein Test-Profil in einer **eigenen** statectl-Konfiguration (`--config $TMP/statectl.json`), und durchläuft: `reminder create`, `briefing`, `reminder update`, `reminder complete`, `reminder snooze` (auf einem zweiten, monatlichen Reminder), `changes`, `reminder show --json`. Am Ende `E2E_OK`, der Server wird beendet, der Temp-Ordner gelöscht.

**Achtung Schlüsselbund:** statectl speichert Zugangsdaten im macOS-Schlüsselbund. Prüfe in `internal/statectl/secrets.go`, ob es einen Weg gibt, einen Datei- oder Umgebungs-Speicher zu nutzen. Gibt es keinen, ergänze einen **nur für Tests** aktivierbaren Speicher (`STATECTL_SECRET_STORE=file:<pfad>`), damit das Skript Fabians echten Schlüsselbund nie anfasst. Diese Datei gehört dann zusätzlich zu deinem Bereich, im Report nennen.

## Task 6: Abschluss

Pflichtprüfung plus `scripts/cli-e2e.sh` (Ausgabe `E2E_OK`), Report `reports/CLI-04-report.md`, Draft-PR.
