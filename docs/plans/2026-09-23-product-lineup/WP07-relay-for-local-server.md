# WP07: Relay-Adresse einstellbar, Mac Server mit VPS-Relay

**Welle:** 2 (erst starten, wenn WP02 und WP06 auf `main` sind) · **Slug:** `relay-for-local-server` · **Branch:** `wp/07-relay-for-local-server`
**Besitzt:** `cmd/state-server/desktop.go`, `cmd/state-server/desktop_test.go`, `macos/Sources/**`, `ios/State/Sources/Session/SessionRepository.swift`, `ios/State/Sources/Notifications/PushRegistrationService.swift`, `ios/State/Sources/UI/SettingsView.swift` (nur neuer Abschnitt), `ios/State/Sources/UI/ConnectView.swift` (nur Übernahme des Relay-Parameters), `ios/StateTests/**`, `docs/LOCAL_MAC_CONNECTION.md`, `macos/README.md`
**Geschätzter Umfang:** mittel (Go + Swift)

## Goal-Prompt (so an die Worker-Session geben)

> Lies `docs/plans/2026-09-23-product-lineup/README.md` vollständig und halte dich an das Arbeitsprotokoll in Abschnitt 4. Setze danach `docs/plans/2026-09-23-product-lineup/WP07-relay-for-local-server.md` Task für Task mit TDD um. Schließe mit Report und Draft-PR ab.

## Ausgangslage (Stand vor Welle 1, im Code nachprüfen)

- Das iPhone registriert sich beim Relay und teilt dem Server die Route mit. Der Server schickt verschlüsselte Pakete an die Relay-URL aus dieser Route (`internal/push/sender.go`).
- Die Relay-URL bestimmt das iPhone in `PushRegistrationService.resolvedRelayURL(serverURL:)`: gespeicherter Wert aus `UserDefaults` (Schlüssel `state.relay-url`), sonst aus dem Server-Host abgeleitet (`state.X` wird `relay.X`, sonst `relay.` + Host).
- **Fehler 1:** Für einen Mac Server (`https://name.local:9847`) entsteht `https://relay.name.local:9847`. Die Adresse gibt es nicht.
- **Fehler 2:** Der abgeleitete Wert wird dauerhaft gespeichert. Nach einem Serverwechsel bleibt die alte Relay-Adresse aktiv.
- Der Pairing-QR des Mac Servers ist `state://pair?server=...&code=...&fingerprint=...` (`desktop.go`, Suche nach `url.Values{"server"`). `PairingPayload` in `SessionRepository.swift` liest diese Parameter.

## Ziel

1. Die Relay-URL gehört zur Server-Sitzung (`ServerSession.relayURL`), nicht zu einem globalen Cache.
2. Der Mac Server kann eine Relay-URL konfigurieren. Sie steht im QR-Code (`relay=`) und wird beim Pairing übernommen.
3. Das iPhone leitet für `.local`-Server und IP-Adressen **keine** Relay-URL ab. Ohne Relay registriert es sich nicht für Push und zeigt das in den Einstellungen an.
4. In den iPhone-Einstellungen kann die Relay-URL angezeigt, gesetzt und entfernt werden (nur `https`).

## Task 1: Go, Relay-URL im Desktop-Modus

**Dateien:** `cmd/state-server/desktop.go`, `cmd/state-server/desktop_test.go`

1. Neues Flag in `runDesktop`: `relayURL := flags.String("relay-url", "", "optional public push relay, for example https://relay.example.com")`.
2. Validierung: leer ist erlaubt. Sonst muss es eine absolute `https`-URL ohne Benutzerinfo, ohne Query und ohne Fragment sein. Ungültig führt zu einem Fehler beim Start. Extrahiere dafür eine Funktion `validDesktopRelayURL(value string) bool`.
3. `desktopStatus` bekommt `RelayURL string \`json:"relay_url,omitempty"\``.
4. Beim Erzeugen des Pairing-Links `relay` ergänzen, wenn gesetzt: `values.Set("relay", relayURL)`.

Tests zuerst:
- `TestValidDesktopRelayURL` (tabellengetrieben): `""` ok, `https://relay.example.com` ok, `http://relay.example.com` nein, `https://user@relay.example.com` nein, `https://relay.example.com/?x=1` nein, `relay.example.com` nein.
- Ein Test, der den Pairing-Link baut und prüft, dass `relay=` enthalten ist. Wenn der Link-Bau heute inline in `runDesktop` steckt, extrahiere ihn in `desktopPairingURL(serverURL, code, fingerprint, relayURL string) string` und teste diese Funktion.

`go test -race ./cmd/state-server/`

Commit: `git commit -m "feat: let the desktop server advertise an optional push relay"`

## Task 2: Mac Server App, Einstellung für das Relay

**Dateien:** `macos/Sources/ServerController.swift`, `macos/Sources/ServerView.swift`, `macos/README.md`

1. In `ServerController` eine gespeicherte Einstellung: `var relayURL: String` mit `UserDefaults.standard` (Schlüssel `state.desktop.relay-url`).
2. Beim Start des Kindprozesses: Wenn `relayURL` nicht leer ist, `["--relay-url", relayURL]` an `child.arguments` anhängen.
3. In `ServerView` ein Bereich "Push unterwegs (optional)": Textfeld für die Relay-Adresse, Hinweistext "Mit einem öffentlichen State-Relay, z.B. auf deinem VPS, bekommt dein iPhone Mitteilungen auch außerhalb des WLANs. Die Inhalte bleiben Ende-zu-Ende verschlüsselt.", Button "Übernehmen" (speichert und startet den Server neu, `stop()` dann `start()`).
4. Ungültige Eingaben (nicht `https://`) zeigen einen Fehlertext und werden nicht gespeichert.
5. `macos/README.md`: Abschnitt "Optional push relay" mit 4 Sätzen.

Prüfung: `swift build && swift test`

Commit: `git commit -m "feat: configure an optional push relay in the mac server app"`

## Task 3: iOS, Relay-URL gehört zur Sitzung

**Dateien:** `ios/State/Sources/Session/SessionRepository.swift`, `ios/State/Sources/Notifications/PushRegistrationService.swift`, `ios/StateTests/**`

1. `ServerSession` bekommt `var relayURL: URL? = nil`. Da die Struktur `Codable` ist und alte gespeicherte Sitzungen das Feld nicht haben, **muss** das Dekodieren alter Daten weiter funktionieren (optionales Feld mit Default reicht bei synthetisiertem `Codable` nur, wenn der Decoder fehlende Schlüssel als `nil` akzeptiert: für `Optional` ist das so). Test dafür schreiben: altes JSON ohne `relayURL` dekodiert zu `relayURL == nil`.
2. `PairingPayload` bekommt `let relayURL: URL?`, gelesen aus dem Query-Parameter `relay`. Gültig nur mit Schema `https` und ohne Benutzerinfo, sonst ist der ganze Payload ungültig (`return nil`). Tests: Payload mit gültigem Relay, mit `http`-Relay (ungültig), ohne Relay.
3. Überall, wo aus einem `PairingPayload` eine `ServerSession` entsteht (suche `ServerSession(` in `ios/State/Sources`), `relayURL` übernehmen.
4. `PushRegistrationService.resolvedRelayURL`:
   - Reihenfolge: `session.relayURL` → abgeleitet nur, wenn der Host **kein** `.local`-Host und **keine** IP-Adresse ist → sonst `nil` (keine Registrierung).
   - Den dauerhaften Cache in `UserDefaults` (`relayURLKey`) **nicht mehr schreiben**. Beim ersten Start nach dem Update einen vorhandenen alten Wert einmalig löschen.
   - Extrahiere die Entscheidung als reine, testbare Funktion:
     ```swift
     static func relayURL(for serverURL: URL, configured: URL?) -> URL?
     ```
   Tests:
     | serverURL | configured | Ergebnis |
     | --- | --- | --- |
     | `https://state.example.com` | nil | `https://relay.example.com` |
     | `https://example.com` | nil | `https://relay.example.com` |
     | `https://mac.local:9847` | nil | nil |
     | `https://192.168.1.20:9847` | nil | nil |
     | `https://mac.local:9847` | `https://relay.example.com` | `https://relay.example.com` |
     | `https://state.example.com` | `https://push.other.org` | `https://push.other.org` |
5. Wenn `relayURL` nil ist: `registerIfSupported` kehrt ohne Fehler zurück. Es wird ein beobachtbarer Zustand gesetzt (z.B. `AppModel.pushStatus = .unavailableNoRelay` oder eine vorhandene Status-Property, prüfe was es gibt), damit die Einstellungen es anzeigen können.

iPhone-Tests (Befehl aus dem Master-Plan). Mac-Build (Befehl aus WP06).

Commit: `git commit -m "fix: tie the push relay to the server session and skip it for local servers"`

## Task 4: iOS, Einstellungen

**Datei:** `ios/State/Sources/UI/SettingsView.swift` (nur neuer Abschnitt, bestehende Abschnitte nicht umbauen)

Neuer Abschnitt "Push-Relay" (nur auf iOS anzeigen, `#if os(iOS)`):
- Zeigt die aktive Relay-Adresse oder "Kein Relay: Mitteilungen nur lokal und im WLAN".
- Textfeld plus "Speichern" setzt `session.relayURL` (nur `https`), speichert die Sitzung über `SessionRepository.save`, stößt die Push-Registrierung erneut an.
- "Entfernen" setzt `relayURL = nil`.
- Deutsche Texte mit Umlauten, englische Keys im String-Katalog analog zu den anderen Einträgen.

Commit: `git commit -m "feat: show and edit the push relay in settings"`

## Task 5: Doku und Gesamtprüfung

`docs/LOCAL_MAC_CONNECTION.md`: Abschnitt "Push outside the home network" (Relay auf dem VPS, Einstellung in der Mac Server App, QR enthält `relay=`, iPhone speichert es pro Sitzung).

```bash
go test -race ./...
swift test
cd ios && xcodegen generate && cd ..
xcodebuild -project ios/State.xcodeproj -scheme State -destination 'platform=iOS Simulator,name=iPhone 16 Pro,OS=18.5' -derivedDataPath build/DerivedData-wp07 CODE_SIGNING_ALLOWED=NO test 2>&1 | tail -5
xcodebuild -project ios/State.xcodeproj -scheme StateMac -destination 'platform=macOS' -derivedDataPath build/DerivedData-wp07 CODE_SIGNING_ALLOWED=NO build 2>&1 | tail -5
```

Report und Draft-PR nach Master-Plan Abschnitt 4.4 und 4.5.
