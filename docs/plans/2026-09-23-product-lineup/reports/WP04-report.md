# WP04 Report: `statectl reminder` als CLI-Ausweichweg

**Status:** DONE
**Branch:** wp/04-statectl-reminder-cli
**Letzter Commit:** `git log -1` auf dem Branch, dazu dieser Report; letzter Umsetzungs-Commit `95dc555 fix: reject a broken reminder schedule before connecting`

## Ergebnis in drei Sätzen

Die CLI kann Reminder jetzt anlegen, Kontext ergänzen, umplanen, anzeigen und suchen, und sie ruft dabei ausschließlich dieselben MCP-Tools auf wie ein Agent (`create_reminder`, `add_comment`, `get_reminder`, `update_reminder`, `search_reminders`). Die Argumenterzeugung und Validierung liegt testbar in `internal/statectl`, das Flag-Parsing und die Ausgabe in `cmd/statectl`, kein direkter Datenbankzugriff, keine zweite Persistenz. Alle sechs Tasks sind erledigt, `gofmt`, `go vet` und `go test -race ./...` sind grün.

## Erledigte Tasks

- [x] Task 1: Argumente bauen und validieren. `BuildSchedule` und `BuildCreateReminderArguments`, tabellengetriebener Test `TestBuildCreateReminderArguments` mit allen Zeilen der Plan-Tabelle plus Zusatzfällen.
- [x] Task 2: `ReminderService` mit Fake-ToolCaller. Alle sechs im Plan genannten Tests vorhanden, dazu weitere für Kontext, Clear-Flags, Show und Limit.
- [x] Task 3: Integrationstest gegen einen echten In-Memory-Server. Alle fünf Schritte abgedeckt, kein Blocker.
- [x] Task 4: CLI-Befehle `create`, `add-context`, `schedule`, `show`, `search` plus 13 netzwerkfreie Tests für Flags, Schedule-Vorprüfung, Dateigröße und Ausgabeformat.
- [x] Task 5: README-Unterabschnitt `### Terminal fallback`.
- [x] Task 6: Gesamtprüfung, Report, Commit.

## Geänderte Dateien

| Datei | Änderung |
| --- | --- |
| `internal/statectl/reminder.go` | Neu. `ToolCaller`, `ScheduleOptions`, `CreateReminderOptions`, `BuildSchedule`, `BuildCreateReminderArguments`, `StoredReminder`, `ReminderService` mit `Create`, `AddContext`, `Reschedule`, `Show`, `Search`, `decodeToolResult` |
| `internal/statectl/reminder_test.go` | Neu. Tabellentest der Validierung, Fake-ToolCaller, sechs Service-Tests, zwei Integrationstests gegen `httptest` plus `mcpserver.NewHandler` |
| `cmd/statectl/reminder.go` | Neu. `runReminder` mit fünf Unterbefehlen, Flag-Validierung vor jeder Verbindung, Ausgabe in Text oder JSON |
| `cmd/statectl/reminder_test.go` | Neu. 12 netzwerkfreie Tests für Flags, Dateigröße und Ausgabeformat |
| `cmd/statectl/main.go` | Nur der `reminder`-Case im Switch und das Usage-Argument |
| `README.md` | Neuer Unterabschnitt `### Terminal fallback` im Pairing-Abschnitt |
| `docs/plans/2026-09-23-product-lineup/reports/WP04-report.md` | Dieser Report |

## Dateien außerhalb meines Bereichs

- `README.md`: von Task 5 ausdrücklich verlangt. Nur der neue Unterabschnitt `### Terminal fallback` zwischen "Useful commands" und "## MCP tools". WP01 besitzt am README nur den Abschnitt Components, es überlappt also nichts.
- `docs/plans/2026-09-23-product-lineup/reports/WP04-report.md`: der Report-Pfad aus dem Master-Plan. Der Ordner `docs/plans/` liegt nicht auf `origin/main`, deshalb wurde er in diesem Branch neu angelegt.
- `cmd/statectl/main.go` gehörte zu meinem Bereich, aber nur für Switch und Usage. `git diff origin/main -- cmd/statectl/main.go` zeigt genau zwei Zeilen.

## Prüfungen

| Befehl | Ergebnis |
| --- | --- |
| `gofmt -l ./cmd ./internal` | leer |
| `go vet ./...` | grün |
| `go test -race ./...` | grün, 15 Pakete (cmd/state-relay, cmd/state-runner, cmd/state-server, cmd/statectl, internal/api, internal/auth, internal/mcpserver, internal/push, internal/pushcrypto, internal/relay, internal/runner, internal/securefile, internal/state, internal/statectl, internal/store) |
| `go test -race -count=1 ./internal/statectl/ ./cmd/statectl/` | grün |
| `go build -o /tmp/wp04-statectl ./cmd/statectl` | Binary gebaut |
| `/tmp/wp04-statectl reminder` | Exit 1, `usage: statectl reminder <create|add-context|schedule|show|search>` |

## Abweichungen vom Plan

1. **Kein `title` in `update_reminder`.** Der Plan fragt, ob das Feld weggelassen werden darf. `updateReminderInput.Title` ist ein `*string` mit `omitempty`, und `state.UpdateReminder` lässt `nil` unverändert. `Reschedule` sendet deshalb nur `reminder_id`, `expected_revision`, `client_request_id`, `source_text` und die geänderten Schedule-Felder. `TestRescheduleUsesCurrentRevision` prüft, dass weder `title` noch `clear_schedule` mitgeschickt wird.
2. **`add_comment` liefert kein `reminder`.** Der Server antwortet `{"stored": true, "comment": {...}}`. `decodeStoredReminder` hätte dort immer "server did not return the reminder" gemeldet. Deshalb gibt es jetzt zwei Stufen: `decodeConfirmation` liest nur `stored`, `decodeStoredReminder` verlangt zusätzlich `reminder`. `AddContext` nutzt die erste Stufe.
3. **`mode` ist serverseitig kein Pflichtfeld.** `state.CreateReminderInput` validiert Schedule und Recurrence nicht, `validateMutation` prüft nur Actor, Titel und Client-Request-ID. Die CLI validiert deshalb vollständig selbst, wie im Plan verlangt.
4. **Mehr Validierung als die Plantabelle.** Zusätzlich abgelehnt werden: `--until` ohne `--repeat`, negatives `--prewarning`, ungültige `--request-id`, `--clear-repeat` zusammen mit `--repeat`, leere `--query`, leere `--id` und ein Schedule, der ohne `--clear`, `--clear-repeat` oder `--date` aufgerufen wird. Alle Fehler kommen vor dem Netzwerkaufruf.
5. **Zeitzonen-Erkennung liegt im CLI-Paket, der Fehler in der Service-Schicht.** `detectLocalTimeZone` liefert `time.Local.String()` ohne führenden Doppelpunkt und, wenn das `Local` ergibt, den Wert von `TZ`. Ist beides nicht auflösbar, gibt sie einen leeren String zurück. Den Fehler `cannot determine local time zone, pass --tz` wirft `BuildSchedule`; `create` und `schedule` rufen es zusätzlich als Vorprüfung (`preflightSchedule`) auf, damit der Fehler vor dem Verbindungsaufbau kommt. `show` und `search` prüfen keinen Schedule und funktionieren dadurch auch auf Rechnern ohne benennbare Zone. Auf diesem Mac ist `TZ` leer und `time.Local.String()` liefert `Local`, ein Datum ohne `--tz` bricht also mit genau dieser Meldung ab.
6. **`Search` klemmt das Limit.** `--limit` über 100 wird auf 100 gekappt statt abgelehnt, `0` oder negativ wird zu 20. Der Plan sagt "Default 20, maximal 100".
7. **`docs/plans/` fehlt auf `origin/main`.** Der Report wurde im Worktree neu angelegt. Da der Koordinator die Pläne laut Master-Plan vor Welle 1 nach `main` bringt, ist mit einem Konflikt nur zu rechnen, wenn er dieselbe Report-Datei dort schon angelegt hat.
8. **`--clear-repeat` ohne `--date` ist erlaubt.** Die Usage-Zeile im Plan verlangt `(--clear | --date ...)` und erlaubt `--clear-repeat` nur als Zusatz. Die Umsetzung akzeptiert auch `--clear-repeat` allein, weil das eine sinnvolle Teiländerung ist und kein Risiko erzeugt.

## Offene Fragen und Risiken

1. Der lokale Zeitzonenname hängt davon ab, ob `TZ` gesetzt ist oder `time.Local.String()` einen echten Namen liefert. Auf einem Mac ohne gesetztes `TZ` ergibt das `Local`, also verlangt jede Datumsangabe ohne `--tz` den Schalter. Das ist die im Plan beschriebene Regel, aber es ist eine Hürde im Alltag. Der Koordinator kann später einen Profil-Standard oder ein `--tz` aus der Konfiguration ergänzen.
2. Die Idempotenz beruht auf `client_request_id` plus Actor. Ein zweiter Aufruf mit derselben Request-ID und demselben Profil liefert denselben Reminder, auch wenn sich Titel oder Datum unterscheiden. Für die CLI ist das gewollt, kann einen Nutzer aber überraschen, der `--request-id` wiederholt.
3. Der CLI-Pfad selbst wird nur über Flag-Fehler getestet, nicht über eine echte Verbindung, weil `loadProfileAndCredential` den System-Keychain liest und Tests dort nichts anfassen dürfen. Die Verdrahtung Flag-Parsing zu Service ist damit nur durch Lesen und durch die Tests von `internal/statectl` belegt.
4. `internal/statectl/reminder_test.go` importiert `internal/mcpserver`, `internal/store`, `internal/auth` und PocketBase. Das erweitert die Testabhängigkeiten des Pakets, erzeugt aber keinen Importzyklus.

## Manuelle Schritte für Fabian oder den Koordinator

1. Keine. Es wurden keine externen Systeme verändert, kein Keychain-Eintrag gelesen, kein Deployment gemacht.
2. Beim Merge beachten: WP05 ergänzt laut Master-Plan in `DefaultAgentRules()` einen Satz zum CLI-Fallback `statectl reminder`. Das berührt `internal/statectl/rules.go`, das ich nicht angefasst habe.
