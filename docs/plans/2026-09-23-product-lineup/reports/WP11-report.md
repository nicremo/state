# WP11 Report: Release-Pipeline für iPhone, iPad und Mac

**Status:** DONE
**Branch:** wp/11-release-pipeline
**Letzter Commit:** ee26e7c chore: configure signing for the macos target
**Draft-PR:** https://github.com/nicremo/state/pull/50

## Ergebnis in drei Sätzen

Der Mac bekommt eigene Fastlane-Lanes: `mac_test` baut `StateMac` ohne Signing, `mac_build` erzeugt ein signiertes App-Store-Paket und `mac_beta` lädt es zu TestFlight hoch, mit derselben Build-Nummernquelle wie iOS. Die iOS-Lane `test` baut zusätzlich für das iPad, die Snapfile nimmt das iPad Pro 13-inch mit auf, die Screenshot-Tests bedienen jetzt wahlweise die Tab-Leiste oder die Seitenleiste, und `compose_ipad_frames.py` ist wieder lauffähig. Alle Prüfbefehle des Pakets sind grün, nur `mac_build` und `mac_beta` sind bewusst nicht ausgeführt, weil sie ein Mac-Distributionszertifikat und einen Store-Zugang brauchen.

## Stand der Welle

| Paket | Stand am Ende der Sitzung |
| --- | --- |
| WP06 | auf `main` gemergt (#49), Target und Scheme `StateMac` sind vorhanden |
| WP07 | auf `main` gemergt (#46) |
| WP10 | auf `main` gemergt (#44) |
| WP09 | **noch nicht auf `main`**, die Arbeit läuft im Worktree `wp09-adaptive-layout` |

**Wichtig für die Merge-Reihenfolge:** Dieser Branch muss nach WP09 auf `main`. Ohne die Split-Ansicht zeigt das iPad die `TabView` von iPadOS 18, und deren Knöpfe liegen als einfache `Button`-Elemente im Baum, nicht in einer `TabBar`. Vor WP09 scheitert der iPad-Lauf der Screenshot-Tests also an der alten Tab-Leiste, nach WP09 bedient derselbe Test die Seitenleiste (beides gemessen, siehe unten).

## Erledigte Tasks

- [x] Task 1: Lesende Diagnose. `fastlane lanes` listet 25 iOS-Lanes und die drei macOS-Lanes, `fastlane diagnose` ist grün (Werkzeugkette, 55 Unit-Tests, 1 UI-Test, keine Fehler).
- [x] Task 2: Signing für `StateMac`. `CODE_SIGN_STYLE: Automatic` und `CODE_SIGN_IDENTITY: "Apple Development"` in `settings.base`, `CODE_SIGN_IDENTITY: "Apple Distribution"` unter `configs.Release`. Das iOS-Target nutzt automatisches Signing: kein `CODE_SIGN_STYLE`, kein `PROVISIONING_PROFILE_SPECIFIER`, dafür global `DEVELOPMENT_TEAM: 5DKU7FFK4X` und `-allowProvisioningUpdates` in der Lane `build`. Der Stil ist damit derselbe, und die im Plan genannte Platzhaltervariable `$(STATE_MAC_PROFILE)` ist nicht nötig. `xcodegen generate` und `fastlane mac_test` sind grün.
- [x] Task 3: Fastlane-Lanes für macOS. `mac_test`, `mac_build` und `mac_beta` sind angelegt, die Lane `test` baut zusätzlich für `iPad (A16)` mit OS 18.5. `mac_test` und `test` sind grün, `mac_build` und `mac_beta` sind bewusst nicht ausgeführt.
- [x] Task 4.1: `iPad Pro 13-inch (M4)` in der Snapfile. Gerätetyp und Simulator-Instanz existieren, die Laufzeit iOS 18.5 ist installiert, es muss nichts nachinstalliert werden.
- [x] Task 4.2: Navigation der Screenshot-Tests. Auf dem iPad tippt der Test jetzt die Einträge der Seitenleiste an, auf dem iPhone weiter die Tab-Leiste. Beides gemessen: gegen WP09 grün auf dem iPad, auf diesem Branch grün auf dem iPhone.
- [x] Task 4.3: Mac-Screenshots. Keine Lane, sondern die im Plan vorgesehene manuelle Anleitung in `docs/RELEASE_CHECKLIST.md`, mit Begründung unten.
- [x] Task 5: Release-Checkliste. Abschnitt "Mac and iPad" ergänzt.
- [x] Task 6: Gesamtprüfung. `xcodegen generate`, `fastlane mac_test`, `fastlane test`, `fastlane diagnose` und `git status --short` sind grün.

## Geänderte Dateien

| Datei | Änderung |
| --- | --- |
| `ios/fastlane/Fastfile` | `before_all` von `platform :ios` auf die Dateiebene verschoben, damit auch die macOS-Lanes das Projekt neu erzeugen. Neuer Helfer `request_api_key!`, die private Lane `connect_api_key` ruft ihn auf. Lane `test` baut zusätzlich für das iPad. Neue Lanes `mac_test`, `mac_build`, `mac_beta` mit `MAC_SCHEME`, `MAC_DERIVED_DATA_PATH`, `MAC_OUTPUT_PATH`, `MAC_PKG_PATH`. |
| `ios/fastlane/README.md` | Von fastlane neu erzeugt, listet die drei macOS-Lanes und die neue Beschreibung der Lane `test`. |
| `ios/fastlane/Snapfile` | `"iPad Pro 13-inch (M4)"` als zweites Gerät, mit Kommentar dazu, warum die Lane nur die iPhone-Dateien in den Frame-Ordner kopiert. |
| `ios/fastlane/compose_ipad_frames.py` | Quelle und Ziel sind jetzt Argumente mit sinnvollen Vorgaben. Die Datei hatte feste Pfade in einem gelöschten `/private/tmp/claude-501/...`-Verzeichnis und war nicht mehr lauffähig. |
| `ios/project.yml` | Signing-Einstellungen des Targets `StateMac` (Task 2). |
| `docs/RELEASE_CHECKLIST.md` | Neuer Abschnitt "Mac and iPad": App-Store-Connect-Einrichtung für macOS, Befehle, Mac-Screenshots von Hand, Prüfpunkte, Export-Compliance und Datenschutz. |
| `ios/StateUITests/StateScreenshots.swift` | Seitenleiste oder Tab-Leiste, je nach Layout (Task 4.2, Datei außerhalb meines Bereichs). |
| `ios/State.xcodeproj/project.pbxproj` | Vom `xcodegen generate` neu erzeugt, 5 Zeilen aus den neuen Signing-Einstellungen. |

## Dateien außerhalb meines Bereichs

- `ios/StateUITests/StateScreenshots.swift`: geändert, weil Task 4.2 das ausdrücklich verlangt. Die Anpassung ist rückwärtskompatibel, sie benutzt die Tab-Leiste weiter, wenn es eine gibt.
- `ios/State.xcodeproj/project.pbxproj`: Das Projekt wird im Repository mitgeführt, deshalb habe ich die fünf erzeugten Zeilen mitcommittet, statt einen unaufgeräumten Arbeitsbaum zu hinterlassen. Erzeugt wird die Datei weiterhin nur aus `ios/project.yml`.
- `ios/fastlane/README.md` ist in meinem Bereich, die Neuerzeugung hat aber auch zwei iOS-Lanes nachgetragen (`upload_local_update`, `local_update_status`), die schon vorher im Fastfile standen und nur in der Datei fehlten.

## Prüfungen

| Befehl | Ergebnis |
| --- | --- |
| `ruby -c ios/fastlane/Fastfile` | Syntax OK |
| `fastlane lanes` | grün, 25 iOS-Lanes und die drei macOS-Lanes. Sie erscheinen als `fastlane mac_test`, `fastlane mac_build`, `fastlane mac_beta`, also genau in der Schreibweise, die der Plan und die Checkliste nennen. |
| `cd ios && xcodegen generate` | grün, danach kein Diff am Projekt |
| `fastlane mac_test` | grün gegen das echte Scheme `StateMac`: `** BUILD SUCCEEDED **` |
| `fastlane test` | grün, zweimal gelaufen: 55 Unit-Tests und 1 UI-Test ohne Fehler, danach der iPad-Build mit `** BUILD SUCCEEDED **` |
| `fastlane diagnose` | grün: ruby 4.0.5, Xcode 26.6, xcodegen 2.46.0, 55 Unit-Tests und 1 UI-Test ohne Fehler |
| iPad-Screenshot-Test gegen WP09 | grün, 23,2 Sekunden, Seitenleiste statt Tabs |
| iPhone-Screenshot-Test auf diesem Branch | grün, 19,9 Sekunden, Tab-Leiste |
| `python3 ios/fastlane/compose_ipad_frames.py <roh> <ziel>` | grün mit Testaufnahmen im Namensschema von `snapshot` (`iPad Pro 13-inch (M4)-01-today.png`), acht Dateien in 2064x2752, zwei Sprachen |
| `git status --short` | sauber, keine `.env`, keine `.p8`, keine Build-Artefakte |

## Vorabprüfung gegen WP06 und WP09

Bevor WP06 auf `main` war, habe ich einen Wegwerf-Worktree unter `/tmp/wp11-verify` angelegt, darin den WP09-Branch (`wp/09-adaptive-layout`, der WP06 enthält) ausgecheckt und meinen Fastfile-Commit darauf gesetzt. Nichts davon ist in meinen Branch oder nach `main` geflossen, der Worktree ist wieder entfernt. Damit ließ sich prüfen, was auf `main` noch nicht prüfbar war:

1. `fastlane mac_test` gegen das echte Scheme `StateMac`: grün, `** BUILD SUCCEEDED **`.
2. Der Task-2-Diff in `ios/project.yml`: `xcodegen generate` erzeugt `CODE_SIGN_IDENTITY = "Apple Distribution"` und `CODE_SIGN_STYLE = Automatic` für Release und `Apple Development` für Debug, der Build bleibt grün.
3. Der iPad-Screenshot-Test gegen die Split-Ansicht: Der Test scheitert ohne Anpassung an `StateScreenshots.swift:16` (`app.tabBars` gibt es dort nicht), mit der Anpassung läuft er in 23,2 Sekunden grün.

## iPad-Navigation (Task 4.2)

Die Seitenleiste ist ein `List(selection:)` mit `Label`-Zeilen ohne eigene Accessibility-Identifier. Der Test konnte sich also nicht auf einen Identifier stützen. Die Struktur aus dem Accessibility-Baum der Split-Ansicht:

```
CollectionView, label: 'Sidebar'
  Cell  -> Image (identifier: 'sun.max.fill') und StaticText (label: 'Today')
  Cell  -> Image (identifier: 'calendar')     und StaticText (label: 'Planned')
  Cell  -> Image (identifier: 'clock.arrow.circlepath') und StaticText (label: 'Activity')
  Cell  -> Image (identifier: 'gearshape')    und StaticText (label: 'Settings')
```

Die Reihenfolge ist in beiden Layouts gleich, die Titel sind übersetzt. Die Screenshot-Läufe laufen außerdem zweisprachig, deshalb wählt der Test nach Position statt nach Name:

```swift
private func sectionEntry(_ app: XCUIApplication, _ index: Int) -> XCUIElement {
    let tabBar = app.tabBars.firstMatch
    if tabBar.exists {
        return tabBar.buttons.element(boundBy: index)
    }
    return app.collectionViews["Sidebar"].cells.element(boundBy: index)
}
```

Zusätzlich prüft der Test jetzt zuerst, dass die Demo-Erinnerung da ist, und benutzt das als Bereitschaftssignal. Vorher hing die Bereitschaft an der Tab-Leiste, die es auf dem iPad nicht gibt.

Vor WP09 gilt weiterhin: Auf dem iPad liegen die Knöpfe der `TabView` als einfache `Button`-Elemente mit den System-Image-Namen als Identifier im Baum (`sun.max.fill`, `calendar`, `clock.arrow.circlepath`, `gearshape`). Deshalb ist der iPad-Lauf dort rot, und deshalb gehört WP09 vor diesen Branch.

## Mac-Screenshots (Task 4.3)

Der Plan nennt als Bedingung, dass die App über ein Launch-Argument im Demo-Modus startet. Dieses Argument gibt es nicht: Die UI-Tests nutzen `-stateUITesting` nur, um Startbildschirm und Einführung zu überspringen, und tippen den Demo-Modus danach über den Knopf `explore-demo` an (`ios/State/Sources/UI/ConnectView.swift`). Eine Lane bräuchte deshalb zusätzlich UI-Skripting mit Bedienungshilfen-Rechten, und die feste Fenstergröße von 2880x1800 setzt einen entsprechend großen Bildschirm voraus. Ich habe deshalb die im Plan vorgesehene manuelle Anleitung geschrieben (`docs/RELEASE_CHECKLIST.md`, Abschnitt "Mac screenshots"): Build starten, Demo-Modus antippen, Fenster auf 1440x900 Punkte ziehen, mit `screencapture -o -w` aufnehmen, Datei als `Mac-<name>.png` ablegen. Ein Launch-Argument `-stateDemo` in der App würde eine Lane später möglich machen, das wäre aber eine Änderung an Produktivcode außerhalb meines Bereichs.

## Abweichungen vom Plan

1. **Kein `platform :mac`-Block, sondern Lanes auf Dateiebene.** Der Plan verlangt einen `platform :mac do ... end`-Block und prüft anschließend mit `fastlane mac_test`. Beides zusammen geht nicht: Der Fastfile setzt `default_platform(:ios)`, und fastlane löst einen Lanes-Namen ohne Plattformpräfix nur gegen Lanes außerhalb eines Platform-Blocks auf. Ein `platform :mac`-Block führt deshalb zu `Could not find lane 'ios mac_test'. Available lanes: ios ios_test, mac mac_test`, die Lanes auf Dateiebene sind dafür nur präfixlos erreichbar (`fastlane mac mac_test` endet mit `Could not find lane 'mac mac_test'`). Beides habe ich mit einem Fastfile in `/tmp` nachgestellt, um nicht zu raten. Den Vorrang hat die im Plan dreimal genannte Befehlsfolge bekommen, die Abweichung ist im Fastfile kommentiert.
2. **`connect_api_key` als Helfer `request_api_key!`.** Die private Lane liegt in `platform :ios` und ist aus einer Lane auf Dateiebene nicht aufrufbar. Der Aufruf steht jetzt einmal in `request_api_key!`, die private Lane ruft nur noch diesen Helfer auf. Das Verhalten der iOS-Lanes ändert sich dadurch nicht.
3. **`before_all` auf Dateiebene.** Die macOS-Lanes brauchen das erzeugte Projekt genauso wie die iOS-Lanes. Ein Block auf Dateiebene läuft laut `runner.rb` auch für Lanes innerhalb eines Platform-Blocks, deshalb steht er genau einmal dort.
4. **`mac_beta` ist strenger als im Plan.** Zusätzlich zu `pkg:` und `skip_waiting_for_build_processing: true` stehen `app_platform: "osx"` und `distribute_external: false` in der Lane. Ohne die Plattformangabe liest pilot sie aus dem Paket, und wenn das misslingt, fragt es interaktiv nach.
5. **Keine Lane `mac_screenshots`**, siehe oben, mit der im Plan vorgesehenen Ersatzlösung.
6. **`compose_ipad_frames.py` repariert.** Nicht im Task-Text, aber die Datei war mit ihren festen `/private/tmp`-Pfaden nicht mehr lauffähig und damit der iPad-Teil des WP-Ziels nicht erreichbar.
7. **iPad-Aufnahmen werden nicht in den Frame-Ordner kopiert.** Die Lane `screenshots` kopiert weiterhin nur die iPhone-Dateien nach `ios/fastlane/screenshots`. Die rohen iPad-Aufnahmen heißen `iPad Pro 13-inch (M4)-<name>.png`, die gerahmten Dateien `iPad Pro 13-inch-<name>.png`, sie überschreiben sich also nicht. Ungerahmte Bilder sollen aber nicht in dem Ordner landen, den `deliver` hochlädt.
8. **`app.debugDescription` in der Bereitschaftsprüfung.** Der Test gibt bei einem Fehlschlag den Accessibility-Baum aus. Das hat die Analyse der Seitenleiste erst möglich gemacht und bleibt als Diagnosehilfe drin.

## Offene Fragen und Risiken

1. **Merge-Reihenfolge:** WP09 muss vor diesem Branch auf `main` sein, sonst ist der iPad-Lauf der Screenshot-Tests rot.
2. **`mac_build` und `mac_beta` sind unverifiziert.** Sie brauchen ein Mac-Distributionszertifikat und einen Store-Zugang. `mac_beta` habe ich nie ausgeführt, wie der Plan es verlangt.
3. **Der Mac braucht eine eigene Store-Einrichtung.** Die bestehenden Metadata- und Review-Lanes sind über `editable_app_store_version!` auf `Platform::IOS` festgelegt und fassen den macOS-Datensatz nicht an. Das ist in der Checkliste beschrieben, aber nicht automatisiert.
4. **Der iPad-Lauf der Screenshot-Suite ist unter Last empfindlich.** Die App-Store-Screenshot-Tests laufen am längsten und werden von fremden Simulator-Neustarts zuerst erwischt, siehe unten.
5. **Build-Nummer.** iOS und macOS teilen sich `MARKETING_VERSION` und die Quelle der Build-Nummer (`BUILD_NUMBER`, sonst UTC-Stempel). Ob App Store Connect dieselbe Nummer gleichzeitig für iOS und macOS akzeptiert, habe ich nicht geprüft, weil das einen echten Upload bräuchte.

## Läufe unter paralleler Last

Auf demselben Rechner liefen während der ganzen Sitzung die Worker von WP06, WP07, WP08, WP09 und WP10 in ihren Worktrees, dazu weitere Sitzungen. Die Last lag zeitweise über 100. Das erklärt die roten Läufe in der folgenden Liste; alle Fehlschläge betreffen den UI-Test, nie eine Zusicherung des Produktcodes:

| Lauf | Ergebnis |
| --- | --- |
| `fastlane test`, 02:24 Uhr | rot, UI-Test durch ein fremdes `xcrun simctl shutdown all` abgeschossen |
| `fastlane test`, 02:46 Uhr | 40 von 41 Tests grün, `StateScreenshots` mit `Test crashed with signal kill`, derselbe Test einzeln in 19 Sekunden grün |
| `fastlane diagnose`, 02:49 Uhr | Werkzeugkette grün, `scan` rot mit `Invalid device state` und `Mach error -308 (ipc/mig) server died` |
| `fastlane test`, 03:05 und 03:10 Uhr | grün |
| `fastlane diagnose`, 03:12 Uhr | Werkzeugkette und Unit-Tests grün, UI-Test wieder abgeschossen |
| `fastlane diagnose`, 03:13 Uhr | grün |

## Manuelle Schritte für Fabian oder den Koordinator

### In App Store Connect, bevor `fastlane mac_beta` funktioniert

1. Beim bestehenden App-Eintrag `com.fabincrm.state` die Plattform macOS hinzufügen. Die Bundle-ID bleibt gleich, damit greift Universal Purchase.
2. Ein Zertifikat "Mac App Store Distribution", ein Zertifikat "Mac Installer Distribution" für das `.pkg` und ein Provisioning-Profil "Mac App Store" für `com.fabincrm.state` anlegen. Mit angemeldeter Apple-ID, die die App verwalten darf, legt Xcode die fehlenden Ressourcen beim ersten `fastlane mac_build` selbst an, weil die Lane mit `-allowProvisioningUpdates` archiviert.
3. Dem Mac-Profil die Keychain-Gruppe `com.fabincrm.state.shared` mitgeben, sonst kann die App die geteilten Server-Zugangsdaten nicht lesen.
4. Für die macOS-Plattform eine Version anlegen und die Mac-Screenshots hochladen, siehe Abschnitt "Mac screenshots" der Checkliste.
5. Die Export-Compliance-Antwort prüfen. `ITSAppUsesNonExemptEncryption` ist auch im macOS-`Info.plist` auf `false` gesetzt und muss zu `submission_info_defaults` passen.

### Nach dem Merge von WP09

`cd ios && fastlane screenshots` und danach `python3 fastlane/compose_ipad_frames.py` laufen lassen, um die iPad-Aufnahmen gegen die echte Seitenleiste zu erneuern. Die Aufnahmen in `ios/fastlane/screenshots` stammen noch aus der Zeit vor der Split-Ansicht.
