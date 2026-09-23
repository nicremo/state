# CLI-02: `statectl reminder update`, `complete`, `snooze`

**Welle:** 2 (erst nach CLI-00 auf `main`) · **Slug:** `reminder-edit` · **Branch:** `cli/02-reminder-edit`
**Besitzt:** `cmd/statectl/reminder_edit.go`, neu `cmd/statectl/reminder_edit_test.go`, neu `internal/statectl/reminder_edit.go`, neu `internal/statectl/reminder_edit_test.go`
**Umfang:** mittel (Go)

## Goal-Prompt

> Lies `docs/plans/2026-09-23-cli-parity/README.md` vollständig und halte dich strikt an das Arbeitsprotokoll in Abschnitt 5. Setze danach `docs/plans/2026-09-23-cli-parity/CLI-02-reminder-update-complete-snooze.md` Task für Task mit TDD um.

## Ziel

Titel und Beschreibung ändern, ein Vorkommen erledigen und ein Vorkommen zurückstellen, genau wie mit den MCP-Werkzeugen `update_reminder`, `complete_occurrence` und `snooze_occurrence`.

## Serververhalten (nachlesen in `internal/mcpserver/server.go`)

- `update_reminder`: `reminder_id`, `title` (*string, omitempty), `description` (*string, omitempty), `expected_revision`, `client_request_id`, `source_text`. Termin und Wiederholung deckt der vorhandene Befehl `reminder schedule` ab. Nicht anfassen.
- `complete_occurrence`: `occurrence_id`, `expected_revision` (Revision **des Vorkommens**), `client_request_id`, `source_text`. Antwort `{"stored": true, "occurrence": {...}}`.
- `snooze_occurrence`: wie oben plus `until` (RFC 3339, UTC). Antwort wie oben.
- `get_reminder` liefert `occurrences` (bis zu 500) mit `id`, `status` (`pending`, `completed`, `snoozed`), `local_date`, `local_time`, `revision`. Prüfe die genaue Antwortform in `reminderDetail`.

Eine Beschreibung lässt sich mit einem leeren String leeren. Prüfe in `internal/state/service.go` (Update-Validierung), ob ein leerer String als "leeren" akzeptiert wird. Wenn nicht, im Report festhalten und `--clear-description` mit einer verständlichen Fehlermeldung ablehnen.

## Task 1: Vorkommen auswählen (reine Funktion)

**Dateien:** `internal/statectl/reminder_edit.go`, `internal/statectl/reminder_edit_test.go`

```go
// OccurrenceRef is the part of an occurrence the CLI needs to act on it.
type OccurrenceRef struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	LocalDate string `json:"local_date"`
	LocalTime string `json:"local_time"`
	Revision  int64  `json:"revision"`
}

// NextOpenOccurrence returns the earliest pending or snoozed occurrence, or
// the one named by id when id is set.
func NextOpenOccurrence(occurrences []OccurrenceRef, id string) (OccurrenceRef, error)
```

Regeln, jede mit eigenem Testfall:
- `id` gesetzt und vorhanden: dieses Vorkommen, auch wenn es nicht das früheste ist.
- `id` gesetzt, aber nicht vorhanden: Fehler, der die ID nennt.
- `id` gesetzt, Vorkommen schon `completed`: Fehler `occurrence is already completed`.
- ohne `id`: frühestes nach `(local_date, local_time)` mit Status `pending` oder `snoozed`. Ein leeres `local_time` sortiert vor jeder Uhrzeit am selben Tag.
- keine offenen Vorkommen: Fehler `reminder has no open occurrence`.

Commit: `git commit -m "feat: pick the occurrence a statectl command acts on"`

## Task 2: Service-Methoden

**Datei:** `internal/statectl/reminder_edit.go`

```go
type UpdateReminderOptions struct {
	ReminderID       string
	Title            *string
	Description      *string
	SourceText       string
	RequestID        string
}

func (service *ReminderService) Update(ctx context.Context, options UpdateReminderOptions) (StoredReminder, error)
func (service *ReminderService) Complete(ctx context.Context, reminderID, occurrenceID, sourceText, requestID string) (json.RawMessage, error)
func (service *ReminderService) Snooze(ctx context.Context, reminderID, occurrenceID string, until time.Time, sourceText, requestID string) (json.RawMessage, error)
```

Verhalten:
- **Update:** Ohne `Title` und ohne `Description` ein Fehler `nothing to update`. Leerer oder nur aus Leerzeichen bestehender Titel ein Fehler. Revision über die vorhandene Hilfe `currentRevision` holen, dann `update_reminder` nur mit den geänderten Feldern. Ergebnis über `decodeStoredReminder`.
- **Complete / Snooze:** `get_reminder` aufrufen, Vorkommen nach `occurrences` dekodieren, `NextOpenOccurrence` anwenden, dann das Werkzeug mit der Revision **des Vorkommens** aufrufen. Ergebnis über `decodeConfirmation` (verlangt `stored: true`).
- **Snooze:** `until` muss in der Zukunft liegen, sonst Fehler. Immer als UTC im Format RFC 3339 senden.
- Leerer `sourceText` ist bei allen drei ein Fehler.
- `requestID` leer: frische UUIDv7 über den `newID`-Generator des Service.

Tests zuerst:
1. Fake-Caller: richtige Werkzeuge, richtige Revision (Reminder-Revision für Update, Vorkommen-Revision für Complete und Snooze), `title` fehlt in den Argumenten, wenn nur die Beschreibung geändert wird.
2. In-Memory-Server (`newReminderTestSession`): Reminder mit Datum und `monthly` anlegen. Titel ändern und über `Show` prüfen. `Complete` ohne Vorkommen-ID: Das erste Vorkommen ist danach `completed`. Prüfe, ob der Server für monatliche Reminder ein weiteres Vorkommen erzeugt, und halte das Verhalten im Report fest. `Snooze` um eine Stunde: Das offene Vorkommen hat danach `snoozed_until`.

Commit: `git commit -m "feat: update, complete and snooze reminders through the service"`

## Task 3: CLI-Befehle

**Datei:** `cmd/statectl/reminder_edit.go` (Rümpfe aus CLI-00 ersetzen)

- `reminder update`: `--title` setzt den Titel, `--description` oder `--description-file` (über das vorhandene `resolveTextInput`) die Beschreibung, `--clear-description` setzt sie leer (siehe Hinweis oben). `--description` und `--clear-description` gleichzeitig ist ein Fehler.
- `reminder complete`: `--occurrence` optional.
- `reminder snooze`: genau eines von `--for` und `--until`. `--for` akzeptiert Go-Dauern wie `10m`, `2h` und zusätzlich `Nd` für Tage (`1d`, `3d`), das parst eine eigene kleine Funktion `parseSnoozeDuration`. `--until` im Format RFC 3339.
- Ausgabe ohne `--json`: `updated reminder <id> "<titel>"`, `completed occurrence <id> of reminder <id>`, `snoozed occurrence <id> until <lokale Zeit>`. Mit `--json` die rohe Antwort.
- Alle Validierungen vor dem Verbindungsaufbau.

Tests in `cmd/statectl/reminder_edit_test.go`, ohne Netzwerk:
- `parseSnoozeDuration`: `10m`, `2h`, `1d`, `3d` gültig; `0m`, `-1h`, `x`, leer ungültig.
- `snooze` mit `--for` und `--until` gleichzeitig: Fehler vor der Verbindung.
- `update` ohne `--title` und ohne Beschreibung: Fehler vor der Verbindung.

Commit: `git commit -m "feat: add statectl reminder update, complete and snooze"`

## Task 4: Abschluss

Pflichtprüfung, Report `reports/CLI-02-report.md` mit Beispielausgaben aller drei Befehle, Draft-PR.
