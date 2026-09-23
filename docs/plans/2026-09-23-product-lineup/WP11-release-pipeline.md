# WP11: Release-Pipeline für iPhone, iPad und Mac

**Welle:** 3 (erst starten, wenn WP06, WP09 und WP10 auf `main` sind) · **Slug:** `release-pipeline` · **Branch:** `wp/11-release-pipeline`
**Besitzt:** `ios/fastlane/**` (außer `metadata/**`-Texten, die nur ergänzt werden), `ios/project.yml` (nur Signing-Einstellungen des Targets `StateMac`), `docs/RELEASE_CHECKLIST.md`
**Geschätzter Umfang:** mittel (Ruby/Fastlane, Doku). **Kein Upload, keine Einreichung.**

## Goal-Prompt (so an die Worker-Session geben)

> Lies `docs/plans/2026-09-23-product-lineup/README.md` vollständig und halte dich an das Arbeitsprotokoll in Abschnitt 4. Setze danach `docs/plans/2026-09-23-product-lineup/WP11-release-pipeline.md` Task für Task um. Du lädst nichts zu App Store Connect hoch, reichst nichts zur Prüfung ein und änderst keine Einstellungen in App Store Connect. Lesende Fastlane-Lanes sind erlaubt. Schließe mit Report und Draft-PR ab.

## Ziel

Der Koordinator kann mit je einem Befehl einen signierten Build für iOS/iPadOS und für macOS erstellen und zu TestFlight hochladen. iPad-Screenshots und Mac-Screenshots lassen sich erzeugen.

## Pflichtlektüre

1. `ios/fastlane/Fastfile` vollständig (vor allem `connect_api_key`, `build`, `beta`, `screenshots`, `diagnose`)
2. `ios/fastlane/README.md`, `ios/fastlane/Appfile`, `ios/fastlane/.env.example`, `ios/fastlane/Snapfile`, `ios/fastlane/compose_ipad_frames.py`
3. `docs/RELEASE_CHECKLIST.md`
4. `ios/project.yml` (Targets `State` und `StateMac`)

Fastlane läuft lokal mit `fastlane` (Version 2.237 oder neuer, per Homebrew). Die App-Store-Connect-API-Keys liegen **außerhalb** des Repos, die `.env` liest du **nicht**.

## Task 1: Lesende Diagnose

```bash
cd ios
fastlane lanes 2>&1 | head -60
fastlane diagnose 2>&1 | tail -40
```

Schreibe das Ergebnis in den Report. `diagnose` darf fehlschlagen, wenn Zugangsdaten fehlen. Notiere dann nur den Grund.

## Task 2: Signing für `StateMac`

**Datei:** `ios/project.yml`

Beim Target `StateMac` unter `settings.base` ergänzen (gleiches Team wie iOS, siehe `DEVELOPMENT_TEAM` global):

```yaml
CODE_SIGN_STYLE: Automatic
CODE_SIGN_IDENTITY: "Apple Development"
```

und unter `configs.Release`:

```yaml
CODE_SIGN_IDENTITY: "Apple Distribution"
```

Prüfe, wie das iOS-Target Signing konfiguriert (manuell oder automatisch, Provisioning-Profile-Namen) und gleiche den Stil an. Wenn iOS manuelle Profile nutzt, übernimm das Muster und trage für den Mac die Profilnamen als Platzhalter-Variable `$(STATE_MAC_PROFILE)` ein. Dokumentiere im Report, welche Profile der Koordinator in App Store Connect anlegen muss (Mac App Store Distribution, gleiche App-ID `com.fabincrm.state`).

`cd ios && xcodegen generate`, dann Mac-Build ohne Signing wie in WP06 (muss weiter grün sein).

Commit: `git commit -m "chore: configure signing for the macos target"`

## Task 3: Fastlane-Lanes für macOS

**Datei:** `ios/fastlane/Fastfile`

Neue Lanes in einem eigenen `platform :mac do ... end`-Block (falls der Fastfile heute nur `platform :ios` nutzt; wenn es gar keinen Platform-Block gibt, die Lanes mit Präfix `mac_` im bestehenden Stil anlegen):

1. `mac_build`: `build_mac_app(project: "State.xcodeproj", scheme: "StateMac", configuration: "Release", export_method: "app-store", output_directory: "build/mac", output_name: "State")`. Build-Nummer wie bei iOS setzen (lies, wie `build` die Build-Nummer bestimmt, und nutze dieselbe Quelle).
2. `mac_beta`: `connect_api_key`, dann `mac_build`, dann `upload_to_testflight(pkg: ..., skip_waiting_for_build_processing: true)`. **Diese Lane führst du nicht aus.**
3. `mac_test`: baut `StateMac` ohne Signing (`xcodebuild ... CODE_SIGNING_ALLOWED=NO build`) als Rauchtest.

Die iOS-Lane `test` erweitern: zusätzlich ein Build für `iPad (A16)` mit OS 18.5 (nur Build, kein Test), damit das iPad-Layout jedes Mal kompiliert.

Prüfung (nur diese Lanes ausführen):

```bash
cd ios
fastlane mac_test
fastlane test
```

Commit: `git commit -m "feat: add fastlane lanes for the macos app"`

## Task 4: Screenshots für iPad und Mac

1. `ios/fastlane/Snapfile`: Prüfe, ob iPad-Geräte bereits eingetragen sind. Wenn nicht, `"iPad Pro 13-inch (M4)"` ergänzen. Prüfe mit `xcrun simctl list devicetypes | grep -i "iPad Pro"`, dass der Gerätename existiert. Fehlt die Laufzeit, den Befehl zum Nachinstallieren in den Report schreiben (nicht selbst installieren).
2. `ios/StateUITests/StateScreenshots.swift`: Prüfe, ob die Screenshot-Tests mit der Split-Ansicht (WP09) auf dem iPad funktionieren (Tab-Knöpfe gibt es dort nicht). Passe die Navigation an: auf dem iPad die Seitenleisten-Einträge statt Tabs antippen (`app.buttons["Today"]` o.ä., per `XCUIApplication().debugDescription` prüfen). Datei außerhalb deines Bereichs, im Report nennen.
3. Mac-Screenshots: Fastlane `snapshot` unterstützt macOS nicht. Lege eine Lane `mac_screenshots` an, die die App im Demo-Modus startet (Launch-Argument prüfen, das die UI-Tests für den Demo-Modus nutzen) und per `screencapture -l <windowid>` ein Fenster in 2880x1800 aufnimmt. Wenn das nicht zuverlässig automatisierbar ist: Lane weglassen und in `docs/RELEASE_CHECKLIST.md` eine manuelle Anleitung schreiben.

Screenshot-Läufe **nicht** hochladen.

Commit: `git commit -m "feat: capture ipad and mac screenshots"`

## Task 5: Release-Checkliste

**Datei:** `docs/RELEASE_CHECKLIST.md`

Abschnitt "Mac and iPad" ergänzen:
- App Store Connect: macOS-Plattform zur bestehenden App hinzufügen (gleiche App-ID, Universal Purchase), Mac-Distributions-Zertifikat, Provisioning-Profil.
- Befehle: `fastlane mac_beta`, `fastlane beta`.
- Prüfpunkte: iPad-Layout (Seitenleiste), Mac-Menübefehle ⌘N/⌘R, Mac ohne Push (erwartet), Runner-Befehl kopierbar.
- Export-Compliance und Datenschutz-Angaben auch für macOS.

Commit: `git commit -m "docs: extend the release checklist for ipad and mac"`

## Task 6: Gesamtprüfung

```bash
cd ios && xcodegen generate && cd ..
cd ios && fastlane mac_test && fastlane test && cd ..
git status --short   # keine .env, keine .p8, keine Build-Artefakte
```

Report und Draft-PR nach Master-Plan Abschnitt 4.4 und 4.5. Unter "Manuelle Schritte" genau auflisten, was der Koordinator in App Store Connect tun muss, bevor `fastlane mac_beta` funktioniert.
