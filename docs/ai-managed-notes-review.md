# Review: AI-managed Notes (PR #67)

**Stand:** 24.09.2026
**Gegenstand:** [`ai-managed-notes.md`](ai-managed-notes.md) und [`ai-managed-notes-transcript.md`](ai-managed-notes-transcript.md)
**Ergebnis:** Die Spezifikation ist in der Richtung richtig und in der Sicherheit sorgfältig. Sie beschreibt aber zwei unabhängige Systeme auf einmal und enthält fünf Widersprüche zum bestehenden State. Umgesetzt wird deshalb in zwei Stufen mit je eigenem Plan.

## 1. Was nachgeprüft wurde

| Behauptung der Spezifikation | Nachgemessen am 24.09.2026 | Ergebnis |
| --- | --- | --- |
| `deepseek/deepseek-v4.1-flash` nimmt Text und Bild, kann Tool Calling, 1.048.576 Token Kontext | OpenRouter Models API, öffentlicher Endpunkt `GET /api/v1/models` | stimmt |
| DeepSeek V4.1 Flash ist kein Transkriptionsmodell | dieselbe Liste, Ausgabemodalität nur Text | stimmt |
| `openai/whisper-large-v3` ist über den Transkriptions-Katalog verfügbar | `GET /api/v1/models?output_modalities=transcription`, 23 Modelle | stimmt, dazu `openai/whisper-large-v3-turbo`, `nvidia/parakeet-tdt-0.6b-v3` und weitere |
| Alte Clients stolpern nicht über neue Audit-Ereignisse | `ios/State/Sources/Sync/SyncEngine.swift`: der Pull gruppiert nur Ereignisse mit `reminder_id` | stimmt, Notiz-Ereignisse ohne `reminder_id` rücken nur den Cursor vor |
| Die Audit-Kette bleibt gültig, wenn ein Feld dazukommt | `sealAuditEvent` hasht `json.Marshal(event)` | stimmt, solange das neue Feld `omitempty` ist |

## 2. Befunde

### 2.1 Zwei Systeme in einem Dokument

Die Spezifikation beschreibt Notizen (Daten, Sync, Editor, REST, MCP, CLI) und eine KI-Verarbeitung (Medienablage, Upload, OpenRouter-Gateway, Vision, Transkription, Agent mit Werkzeugen, Kostenbremsen). Das zweite System hängt vollständig vom ersten ab, das erste braucht das zweite nicht. Abschnitt 10 sagt das selbst: jede Stufe muss Notizen ohne die späteren KI-Stufen nützlich lassen.

**Entscheidung:** Stufe A "Notes Core" wird jetzt gebaut, Stufe B "Medien und KI" bekommt einen eigenen Plan und startet erst nach den Entscheidungen in Abschnitt 4.

### 2.2 Kanonisches Dokumentformat

Die Spezifikation verlangt ein versioniertes, strukturiertes Rich-Text-Dokument, kein HTML, kein Apple-Archiv, verlustfrei in Klartext für Suche, MCP und CLI. Ein eigenes JSON-Blockschema erfüllt das, kostet aber einen Parser und Renderer auf drei Plattformen und ist für Agenten unhandlich.

**Entscheidung:** Das kanonische Dokument ist **Markdown** (CommonMark plus Aufgabenlisten `- [ ]` und Trennlinie `---`). Es ist semantisch, textbasiert, von jedem Agenten les- und schreibbar, und die App rendert es seit PR #66 bereits blockweise (`MarkdownBlocks`). Der Umfang des Editors in Stufe A: Überschriften, fett, kursiv, Listen, nummerierte Listen, Checklisten, Trennlinie. Tabellen, Unterstreichen, Hervorheben und einklappbare Abschnitte sind nicht Teil von Stufe A, weil Markdown sie nicht oder nur uneinheitlich kennt. Das Dokument bekommt kein eigenes Versionsfeld, weil Markdown selbst das stabile Format ist.

### 2.3 `create_reminder_from_note` widerspricht dem bestehenden Vertrag

Das Werkzeug soll nur einen Vorschlag liefern, den der Owner in der App bestätigt. Jeder gekoppelte Agent darf aber heute schon mit `create_reminder` direkt eine Erinnerung anlegen, auf ausdrücklichen Wunsch des Owners. Ein zweiter, schwächerer Weg für dieselbe Handlung erzeugt nur Verwirrung.

**Entscheidung:** Kein `create_reminder_from_note` für Agenten. Ein Agent legt Erinnerungen wie bisher mit `create_reminder` an und nennt die Notiz in der Beschreibung. Der Bestätigungsweg bleibt für den KI-Agenten aus Stufe B bestehen, der ohne Owner im Gespräch arbeitet.

### 2.4 `archive_note` für Agenten widerspricht der Rechte-Regel

Abschnitt 1 und 8.3 sagen, die KI archiviert oder löscht nichts. Abschnitt 7 gibt jedem Agenten `archive_note`. Bei Erinnerungen dürfen Agenten ebenfalls nicht archivieren (`UpdateReminder` verlangt Owner oder Gerät).

**Entscheidung:** Archivieren nur durch Owner und Geräte, über REST und die App. Kein MCP-Werkzeug, kein CLI-Befehl.

### 2.5 CLI-Syntax weicht vom bestehenden `statectl` ab

Die Spezifikation schreibt `statectl note get <note-id>` mit Positionsargument und `--expected-revision` von Hand. `statectl reminder` nutzt `--id`, holt die Revision selbst und verlangt `--source-text`.

**Entscheidung:** `statectl note list|show|create|update` mit denselben Konventionen wie `statectl reminder`, Inhalte über `--document-file -` (stdin), nicht über Inline-Flags.

### 2.6 Eigener Archiv-Endpunkt

`POST /api/v1/notes/{id}/archive` dupliziert, was bei Erinnerungen `PATCH` mit `archived` erledigt.

**Entscheidung:** `PATCH /api/v1/notes/{id}` mit `archived`, genau wie bei Erinnerungen.

### 2.7 Suche

Die bestehende Volltextsuche `state_search` ist über `reminder_id` an Erinnerungen gebunden. Notizen dort hineinzumischen würde `search_reminders` verändern.

**Entscheidung:** Eigene FTS5-Tabelle `state_note_search`. Die Suche in der App läuft offline über den lokalen Cache.

## 3. Was aus der Spezifikation unverändert bleibt

- Eigene Entitäten statt `Reminder`, eigene Audit-Aktionen (`note.created`, `note.updated`, `note.archived`, `note.restored`), signierte Kette, Idempotenz über `client_request_id`, `expected_revision` bei Änderungen.
- Titel aus der ersten Zeile, wenn keiner angegeben ist. Zusammenfassung mit Herkunft `user` oder `derived`. Ein vom Owner gesetzter Titel oder eine gesetzte Zusammenfassung wird nie automatisch überschrieben.
- Eine flache Liste, neueste Änderung oben, Titel plus kurze Zusammenfassung, keine Ordner, Tags oder Kategorien.
- Notizen-Tab auf dem iPhone, Eintrag in der Seitenleiste auf iPad und Mac.
- Nie hart löschen. Archivierte Notizen bleiben wiederherstellbar.

## 4. Offene Entscheidungen für Stufe B

Diese Punkte kann nur Fabian entscheiden. Ohne sie ist Stufe B nicht umsetzbar.

1. **OpenRouter-Schlüssel** für den State-Server: eigener Schlüssel mit Ausgabelimit, abgelegt auf dem Mac Server und dem VPS, nie im Repo.
2. **Monatliches Kostenlimit** für die Notiz-KI.
3. **Medienablage:** auf dem Mac Server unter `~/Library/Application Support/State Server/media`, auf dem VPS in einem eigenen Volume. Größenlimits pro Bild und pro Aufnahme.
4. **Einwilligungstext** in den Einstellungen, der erklärt, dass Bilder und Audio an OpenRouter gehen.
5. **Transkriptionsmodell:** Vorschlag `openai/whisper-large-v3-turbo` (schnell, günstig), Alternative `nvidia/parakeet-tdt-0.6b-v3`.

## 5. Pläne

- Stufe A: [`superpowers/plans/2026-09-24-notes-core.md`](superpowers/plans/2026-09-24-notes-core.md)
- Stufe B: wird nach den Entscheidungen in Abschnitt 4 geschrieben.

## 6. Code-Review der Umsetzung (24.09.2026)

Drei unabhängige Reviews (Server, App-Daten und Sync, App-Oberfläche) plus ein
Durchlauf per MobAI auf iOS 26.5 und iOS 18.5. Alle belegten Befunde sind
behoben und mit Regressionstests abgesichert.

| Bereich | Befund | Lösung |
| --- | --- | --- |
| Server | Runner lasen Notiz-Inhalte über `/changes` und `/briefing` | Notiz-Ereignisse werden für Runner aus beiden Antworten entfernt |
| Server | Wiederholtes Update mit derselben `client_request_id` lieferte 409 | Idempotenz-Nachschlag vor der Revisionsprüfung |
| Server | Jedes Audit-Ereignis speicherte das Dokument viermal | Snapshot ohne `plain_text`, vorherige Fassung nur als Hash und Länge |
| Server | Notizen nur aus Markern (`---`) hatten einen leeren Titel | Abgewiesen, ein lesbarer Titel ist Pflicht |
| Server | Einzelne `*` und `_` wurden überall entfernt, `~~~` nicht erkannt | Hervorhebung nur paarweise, beide Fence-Arten |
| Server | Steuerzeichen in Titeln, 64-KB-Grenze in `statectl note`, `create_note` verlangte ein Dokument | bereinigt, 256 KB, Dokument optional |
| App | Zweite Offline-Änderung nach einem Konflikt überschrieb eine fremde Änderung | Sync neu gebaut: Dirty-Markierung, Basis-Revision, Versionszähler statt PATCH-Kette |
| App | Abgelehnte Notizen blockierten die ganze Warteschlange | Notizen laufen nicht mehr über die Warteschlange; Ablehnungen werden markiert und übersprungen |
| App | Änderung während eines laufenden Uploads, vorläufige IDs | Stabile Request-IDs, Alias-Tabelle, Änderungen während des Uploads bleiben markiert |
| App | Schnelles Abhaken verlor Häkchen, Agenten-Änderungen wurden vom Editor überschrieben | Einziger Schreibweg in der Datenbank-Transaktion, Basis-Version im Editor erkennt fremde Änderungen |
| App | Kein Speichern beim Wechsel in den Hintergrund | Speichern bei Pause, Szenenwechsel und Verlassen |
| App | Veraltete Textauswahl konnte Zeichen zerteilen | Auswahl wird geprüft und beim Bearbeiten zurückgesetzt |
| App | Leere Checkliste, CRLF, Suche mit Umlauten, Barrierefreiheit, Tippflächen | behoben, Checklisten mit Label, Zustand und 44 Punkten Höhe |

**Bewusste Abweichung vom Plan:** Ein Konflikt wird nicht als Eintrag in der
Konfliktliste protokolliert, sondern als **Konfliktkopie** gelöst. Die
Serverfassung bleibt die Notiz, der lokale Text wird eine neue Notiz mit dem
Zusatz "(Konfliktkopie)". So geht kein Text verloren und es braucht keinen
Zusammenführungsdialog. Änderungen nur an Titel oder Archivstatus werden ohne
Kopie auf die neue Serverfassung übertragen.

**Bekannt und nicht Teil dieser Stufe:** Die Mutations-Warteschlange für
Erinnerungen blockiert weiterhin bei dauerhaften 4xx-Antworten. Notizen sind
davon entkoppelt; für Erinnerungen ist das ein eigener Fix.
