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

## 7. Verifikationsrunden (24. und 25.09.2026)

Zwei weitere unabhängige Reviews haben die Fixes angegriffen und jeden Befund
mit einem fehlschlagenden Test belegt. Alle Beweistests sind als
Regressionstests übernommen (`ios/StateTests/NoteSyncTests.swift`,
`internal/mcpserver/notes_runner_test.go`, `internal/state/notes_control_test.go`).

- **Laufende Anfrage wird exakt wiederholt:** Jede Notiz speichert die gesendete,
  noch unbeantwortete Anfrage (Methode, Pfad, Body, Version). Ein Retry sendet
  genau diesen Body; erst nach der Antwort geht der Rest der Änderung unter
  einer neuen Request-ID raus. So kann die Idempotenz des Servers nie einen
  anderen Inhalt beantworten.
- **Neueste Serverfassung:** Ein Pull, der eine Notiz mit ungesendeten
  Änderungen nicht anwenden darf, merkt sich die Fassung. Sie greift, wenn die
  lokale Änderung zurückgenommen wurde oder eine wiederholte Anfrage eine ältere
  Fassung zurückliefert.
- **Konfliktkopien:** entstehen in derselben Transaktion aus dem aktuellen Text,
  auch aus Text, der während des Uploads getippt wurde. Ein offener Editor
  schreibt in die bereits angelegte Kopie weiter, statt eine zweite anzulegen.
- **Editor:** Titel und Text, die der Editor nicht verändert hat, behalten den
  gespeicherten Wert (Agenten-Änderungen bleiben). Leere Notizen werden nur beim
  Abschließen archiviert. Speichervorgänge werden verkettet, "Fertig" wartet auf
  das laufende Speichern.
- **Runner:** sehen Notiz-Ereignisse weder über `/changes` und `/briefing` noch
  über `get_execution_context`; die Regel steht zentral in `state.VisibleChanges`.
- **Parität:** Die Golden-Datei enthält zusätzlich 3000 zufällige, aber
  deterministische Dokumente mit Markern, allen Leerzeichenarten, kombinierenden
  Zeichen, Emoji und Steuerzeichen; Go und Swift vergleichen auf Ebene der
  Unicode-Skalare.

Bekannt und nicht Teil dieser Stufe: `TestDesktopLogsAddressAndVersion` in
`cmd/state-server` scheitert selten beim Aufräumen des Testordners (Race beim
Beenden des Desktop-Servers, unveränderter Code).

## 8. Stufe B: Medien und Notiz-KI (25.09.2026)

Umgesetzt nach [`superpowers/plans/2026-09-25-notes-ai.md`](superpowers/plans/2026-09-25-notes-ai.md)
und dem Originalwortlaut in [`ai-managed-notes-transcript.md`](ai-managed-notes-transcript.md).
Die offenen Entscheidungen aus Abschnitt 4 sind mit Standardwerten belegt, alle in
der App änderbar: Monatslimit 10 USD, Agent `deepseek/deepseek-v4.1-flash`,
Transkription `openai/whisper-large-v3-turbo`, Medien unter `<data>/media`,
Einwilligung in den Einstellungen, Schlüssel nur in `<data>/state_secrets/openrouter.key`.

**Verbindliche Entscheidungen dieser Stufe**

- KI-Ergebnisse (Titel, Zusammenfassung, OCR, Transkript, Verknüpfungen,
  Vorschläge, Status) liegen neben der Notiz und erhöhen ihre Revision nicht.
  Die KI kann dadurch keine Konfliktkopie in der App auslösen. Einzige
  revisionierte KI-Änderung: das leere Dokument einer Foto- oder Sprachnotiz,
  nur wenn die Revision seit Jobbeginn gleich ist. Sonst wird der Text als
  Vorschlag angeboten.
- KI-Titel und -Zusammenfassung gelten nur für den Text, aus dem sie entstanden
  sind (`source_hash`). Nach einer Textänderung erscheinen die abgeleiteten
  Werte, bis der nächste Durchlauf 45 Sekunden nach dem Tippen fertig ist. Ein
  Häkchen ändert den Text nicht und löst keinen bezahlten Durchlauf aus.
- Die Bildgrenze ist das Minimum aus Server-Regel (10 Bilder, 8 MB, 40 MB) und
  dem Platz im Kontextfenster des Modells. OpenRouter veröffentlicht keine
  Bildanzahl pro Modell (geprüft an `/models` und `/models/{id}/endpoints`).
- Formatierung wie iPhone-Notizen in Markdown: `++unterstrichen++`,
  `==hervorgehoben==`, `~~durchgestrichen~~`, GFM-Tabellen, `#`/`##`/`###`,
  einklappbare Abschnitte unter jeder Überschrift. Gedankenstrich-Listen teilen
  sich die Syntax mit Aufzählungen, weil Markdown sie nicht unterscheidet.
- Der Agent ist eine eigene, schlanke Go-Schleife über die OpenAI-kompatible
  API, kein Mastra oder Vercel AI SDK: Der Server ist in Go, und ein
  Node-Prozess würde die Vertrauensgrenze erweitern, ohne Code zu sparen.

**Reviews und Befunde**

Zwei unabhängige Reviews (Server, App), jeder Befund mit fehlschlagendem Test
belegt und als Regressionstest übernommen (`internal/notesai/review_regression_test.go`,
`internal/api/review_regression_test.go`, `ios/StateTests/NoteMediaReviewRegressionTests.swift`).

| Bereich | Befund | Lösung |
| --- | --- | --- |
| Server | Ein vom Notizregelwerk abgelehntes KI-Dokument ließ den Job ewig auf "processing" und nach Neustart erneut bezahlen | Unzulässiger Text wird verworfen, ein nicht speicherbares Ergebnis beendet den Job als fehlgeschlagen |
| Server | Ein Teil des OpenRouter-Schlüssels konnte über das Kürzen der Fehlermeldung in Notiz und Audit gelangen | Erst schwärzen (Schlüssel und jedes `sk-or-`-Muster), dann kürzen |
| Server | Abgelehnte Uploads blieben auf der Platte | Uploads werden erst übernommen, wenn die Notiz den Anhang angenommen hat |
| Server | Bezahlte, aber leere oder unlesbare Antworten wurden nicht aufs Budget gezählt | Kosten oder Schätzung werden immer verbucht |
| Server | Ein verspäteter Retry einer älteren Verarbeitungsanfrage startete einen zweiten bezahlten Job | Die letzten 20 Anfrage-IDs pro Notiz sind idempotent |
| Server | `source_hash` beschrieb den Text zum Speicherzeitpunkt, nicht den Ausgangstext | Hash vom Ausgangstext, Anzeige nur bei passendem Text |
| Server | Eine stille Aufnahme wurde bei jeder Anfrage erneut transkribiert | Die Transkriptart markiert erledigte Aufnahmen |
| App | Tippen während des Anlegens einer Fotonotiz löschte Aufnahmeart, Anhänge und Status | Serverfassung mit den lokalen Textfeldern darüber |
| App | Ein Upload-Fehler stoppte den ganzen Sync samt Erinnerungen | Medien laufen nach dem Pull, vorübergehende Fehler warten auf den nächsten Sync |
| App | "Erneut versuchen" konnte zwei Verarbeitungsanfragen senden | Alle Aliasse werden aufgelöst |
| App | Nach einem Abbruch zwischen Verschieben und Markieren galt ein hochgeladenes Foto als fehlend | Erst markieren, dann verschieben, Cache als Rückfall |
| App | Abbrechen ließ das laufende Aufnahmesegment liegen, Anrufe und Sperre stoppten die Aufnahme stumm | Segment wird gelöscht, Unterbrechungen schließen das Segment, Bildschirm bleibt an, Hintergrund-Audio |
| App | Bilder wurden auf dem Main Thread und in voller Größe dekodiert | Vorbereitung, Hashen und Vorschaubilder laufen im Hintergrund in Anzeigegröße |
| App | Die App bot ein Foto an, wenn das Servermodell keine Bilder liest | Foto-Aufnahme wird dann gesperrt und erklärt |
| App | Verwaiste Dateien und endloses Laden bei alten Servern | Aufräumen beim Verwerfen, Hinweis "Auf diesem Server nicht verfügbar" |
| MobAI | Wischen zum Aktualisieren meldete "CancellationError" (auch in der Liste aus Stufe A) | Abbrüche sind keine Fehler, der Sync läuft vom Wischen entkoppelt |
| MobAI | Auf dem iPad im Hochformat war der Plus-Button bei eingeblendeter Listenspalte unsichtbar | Auf iPad und Mac sitzt der Plus-Button unten rechts an der Notizspalte |
| MobAI | Verwandte Notizen zeigten die erste Textzeile statt des KI-Titels, eine angenommene Erinnerung nannte die Notiz mit englischem Präfix | Sichtbarer Titel in Verknüpfungen, Erinnerung nennt die Notiz mit 📝 und ihrem Titel |

**End-to-End per MobAI** (iPhone 16 Pro mit iOS 18.5, iPad Pro 11) gegen einen lokalen
State-Server mit Fake-OpenRouter (`tools/fake-openrouter`): Plus-Menü, Text mit
Überschrift, Checkliste, Unterstreichen, Hervorheben, Tabelle, Trennlinie und
Einklappen, Foto aus der Mediathek mit Handschrift, OCR und verwandter Notiz,
Sprachaufnahme mit Transkript, Wiedergabe und Erinnerungsvorschlag, der nach
Bestätigung als Erinnerung unter "Geplant" erscheint, Einstellungen mit
Einwilligung, Limit, Modellen und Fotogrenze.
