# WP11 Report: Release-Pipeline für iPhone, iPad und Mac

**Status:** PARTIAL
**Branch:** wp/11-release-pipeline
**Letzter Commit:** 24c3460 docs: extend the release checklist for ipad and mac

## Ergebnis in drei Sätzen

Die Welle-3-Vorbedingung ist nicht erfüllt: WP06, WP09 und WP10 sind nicht auf `main`, und WP09 hat noch nicht einmal einen Branch. Ich habe deshalb alles umgesetzt und geprüft, was ohne diese drei Pakete geht: die macOS-Lanes `mac_test`, `mac_build` und `mac_beta`, den iPad-Build in der iOS-Lane `test`, das iPad-Gerät in der Snapfile, die iPad-Frame-Pipeline und den Abschnitt "Mac and iPad" der Release-Checkliste. Offen und an WP06 beziehungsweise WP09 gebunden bleiben das Signing für `StateMac` und die iPad-Navigation der Screenshot-Tests, unverifiziert bleiben `mac_build` und `mac_beta`, weil sie ein Distributionszertifikat und einen Store-Zugang brauchen.

## Vorbedingung: Welle 3 ist noch nicht freigegeben

Stand 23.09.2026 gegen 02:55 Uhr, `origin/main = e62f515`:

| Prüfung | Befehl | Ergebnis |
| --- | --- | --- |
| WP06 auf main | `git merge-base --is-ancestor origin/wp/06-macos-client-target origin/main` | nein, der Branch `origin/wp/06-macos-client-target` hat aber inzwischen einen fertigen Report (`b16d56e docs: add WP06 report`) |
| WP09 auf main | `git branch --list "wp/09*"` | nein, der Worktree `wp09-adaptive-layout` arbeitet auf Basis von WP06 an `AdaptiveRootView.swift` und `SplitRootView.swift` |
| WP10 auf main | `git merge-base --is-ancestor wp/10-runner-service origin/main` | ja, gemergt als #44 |
| WP07 auf main | `git log --oneline origin/main` | ja, gemergt als #46 |
| `StateMac` in `ios/project.yml` | `git show origin/main:ios/project.yml \| grep -c StateMac` | 0 |

`origin/main` ist während dieser Sitzung mehrfach weitergezogen (4254d1b, debdee4, fe74fde, 63e0895, e62f515), und die Worker von WP06, WP07, WP08, WP09 und WP10 liefen parallel in ihren Worktrees.

Mein Branch ist auf dem aktuellen `origin/main` gemergt (Merge ohne Konflikte, die Dateien dieses WP hat niemand sonst angefasst), und die Prüfungen wurden danach wiederholt.

Folge für dieses WP: Auf meinem Branch gibt es kein Target `StateMac`, also auch kein Scheme `StateMac`. Damit sind Task 2 und der Mac-Teil von Task 3 auf dieser Basis nicht lauffähig. `fastlane mac_test` scheitert genau daran:

```
xcodebuild: error: The project named "State" does not contain a scheme named "StateMac".
The "-list" option can be used to find the names of the schemes in the project.
```

## Erledigte Tasks

- [x] Task 1: Lesende Diagnose. `fastlane lanes` läuft grün und listet 25 iOS-Lanes plus die drei neuen macOS-Lanes. Bei `fastlane diagnose` läuft die Werkzeugkette grün und der Scan scheitert am Simulator, nicht an Zugangsdaten; die genaue Meldung steht unter "Prüfungen".
- [ ] Task 2: Signing für `StateMac`. **BLOCKED.** Es gibt auf dieser Basis kein Target `StateMac`, an das die Einstellungen geschrieben werden könnten. Ein `settings`-Block ohne Target lässt `xcodegen generate` scheitern (`Parsing project spec failed: Unknown Target platform:`), das hätte die Pflichtprüfung `cd ios && xcodegen generate` rot gemacht. Der fertige Diff steht unten unter "Manuelle Schritte".
- [x] Task 3: Fastlane-Lanes für macOS. `mac_test`, `mac_build` und `mac_beta` sind angelegt, die iOS-Lane `test` baut zusätzlich für das iPad. `mac_test` ist bis zum Scheme-Fehler verifiziert, `mac_build` und `mac_beta` sind bewusst nicht ausgeführt.
- [x] Task 4.1: Snapfile. iPad-Gerät ergänzt. `iPad Pro 13-inch (M4)` existiert als Gerätetyp und als Simulator-Instanz auf iOS 18.5, die Laufzeit ist installiert, es muss nichts nachinstalliert werden.
- [ ] Task 4.2: Navigation der Screenshot-Tests. **Nicht nötig auf dieser Basis und deshalb nicht geändert.** Ohne WP09 gibt es keine Split-Ansicht, das iPad benutzt weiter die `TabView`, und `StateScreenshots.swift` findet seine `app.tabBars`. Die Datei gehört ohnehin nicht mir, siehe unten.
- [x] Task 4.3: Mac-Screenshots. Keine Lane, sondern die im Plan vorgesehene manuelle Anleitung in `docs/RELEASE_CHECKLIST.md`. Begründung unten.
- [x] Task 5: Release-Checkliste. Abschnitt "Mac and iPad" ergänzt.
- [x] Task 6: Gesamtprüfung, soweit sie auf dieser Basis möglich ist. Einzelheiten unter "Prüfungen".

## Geänderte Dateien

| Datei | Änderung |
| --- | --- |
| `ios/fastlane/Fastfile` | `before_all` von `platform :ios` auf die Dateiebene verschoben, damit auch die macOS-Lanes das Projekt neu erzeugen. Neuer Helfer `request_api_key!`, die private Lane `connect_api_key` ruft ihn auf. Lane `test` baut zusätzlich für `iPad (A16)` mit OS 18.5. Neue Lanes `mac_test`, `mac_build`, `mac_beta` mit den Konstanten `MAC_SCHEME`, `MAC_DERIVED_DATA_PATH`, `MAC_OUTPUT_PATH`, `MAC_PKG_PATH`. |
| `ios/fastlane/README.md` | Von fastlane neu erzeugt, listet jetzt die drei macOS-Lanes und die neue Beschreibung der Lane `test`. |
| `ios/fastlane/Snapfile` | `"iPad Pro 13-inch (M4)"` als zweites Gerät ergänzt, mit Kommentar dazu, warum die Lane nur die iPhone-Dateien in den Frame-Ordner kopiert. |
| `ios/fastlane/compose_ipad_frames.py` | Quelle und Ziel sind jetzt Argumente mit sinnvollen Vorgaben (`ios/screenshots` und `ios/fastlane/screenshots`). Die Datei hatte feste Pfade in einem gelöschten `/private/tmp/claude-501/...`-Verzeichnis und war damit nicht mehr lauffähig. Außerdem findet sie die Aufnahmen von `snapshot`, die den Gerätenamen als Präfix tragen. |
| `docs/RELEASE_CHECKLIST.md` | Neuer Abschnitt "Mac and iPad": App-Store-Connect-Einrichtung für macOS, Befehle, Mac-Screenshots von Hand, Prüfpunkte, Export-Compliance und Datenschutz. |

## Dateien außerhalb meines Bereichs

- `ios/StateUITests/StateScreenshots.swift`: **nicht angefasst.** Die Anpassung an die Seitenleiste ist erst nach WP09 sinnvoll, weil es ohne die Split-Ansicht keine Seitenleiste und damit nichts anzupassen gibt. Sobald WP09 auf `main` ist, müssen die vier Zugriffe auf `app.tabBars.buttons.element(boundBy:)` durch die Einträge der Seitenleiste ersetzt werden, die vorher per `XCUIApplication().debugDescription` zu bestimmen sind (`app.buttons["Today"]` und so weiter). Ich habe die Datei deshalb bewusst unverändert gelassen, statt sie auf einen Stand umzubauen, den es noch nicht gibt.
- `ios/project.yml`: **nicht angefasst**, siehe Task 2.
- `ios/fastlane/README.md` ist zwar in meinem Bereich, die Neuerzeugung hat aber auch zwei iOS-Lanes nachgetragen (`upload_local_update`, `local_update_status`), die schon vorher im Fastfile standen und nur in der Datei fehlten.

## Prüfungen

| Befehl | Ergebnis |
| --- | --- |
| `ruby -c ios/fastlane/Fastfile` | Syntax OK |
| `fastlane lanes` | grün, 25 iOS-Lanes und die drei macOS-Lanes. Die macOS-Lanes erscheinen als `fastlane mac_test`, `fastlane mac_build`, `fastlane mac_beta`, also genau in der Schreibweise, die der Plan und die Checkliste nennen. |
| `xcodegen generate` | grün, kein Diff an `ios/State.xcodeproj`, auch nach dem Merge von `origin/main` |
| `fastlane mac_test` | rot, und zwar an der Vorbedingung: `xcodebuild: error: The project named "State" does not contain a scheme named "StateMac".` Die Lane selbst wird also gefunden und gestartet, der Mac-Build kann ohne WP06 aber nicht grün sein. Vor und nach dem Merge von `origin/main` geprüft, beide Male dieselbe Meldung. |
| `xcodebuild ... -destination 'platform=iOS Simulator,name=iPad (A16),OS=18.5' CODE_SIGNING_ALLOWED=NO build` | grün, `** BUILD SUCCEEDED **`, vor und nach dem Merge von `origin/main`. Das ist genau der Befehl, den die neue Zeile der Lane `test` ausführt, deshalb ist der iPad-Teil der Lane einzeln belegt. |
| `fastlane test` | in beiden Läufen rot. Lauf 2 kam bis 41 Tests: 40 grün, 1 rot. Der rote Test ist `StateScreenshots/testAppStoreScreenshots()` mit `Test crashed with signal kill`, also ein Abschuss von außen und keine fehlgeschlagene Zusicherung. Derselbe Test läuft einzeln grün. Einzelheiten unten. |
| `fastlane diagnose` | Werkzeugkette grün (`ruby 4.0.5`, `Xcode 26.6`, `xcodegen 2.46.0`), danach rot im `scan`: `Invalid device state`, `Mach error -308 (ipc/mig) server died`, Testläufer abgeschossen. Es fehlen also keine Zugangsdaten, der Scan scheitert am Simulator. |
| `python3 ios/fastlane/compose_ipad_frames.py <roh> <ziel>` | grün mit Testaufnahmen im Namensschema von `snapshot` (`iPad Pro 13-inch (M4)-01-today.png`), acht Dateien in 2064x2752, zwei Sprachen |
| `git status --short` | sauber, keine `.env`, keine `.p8`, keine Build-Artefakte |

## Abweichungen vom Plan

1. **Kein `platform :mac`-Block, sondern Lanes auf Dateiebene.** Der Plan verlangt einen eigenen `platform :mac do ... end`-Block und prüft anschließend mit `fastlane mac_test`. Beides zusammen geht nicht: Der Fastfile setzt `default_platform(:ios)`, und fastlane löst einen Lanes-Namen ohne Plattformpräfix nur gegen Lanes außerhalb eines Platform-Blocks auf. Ein `platform :mac`-Block führt deshalb zu `Could not find lane 'ios mac_test'. Available lanes: ios ios_test, mac mac_test`. Das habe ich mit einem Fastfile in `/tmp` nachgestellt, um nicht zu raten. Die Lanes auf Dateiebene sind dafür nur präfixlos erreichbar, `fastlane mac mac_test` endet mit `Could not find lane 'mac mac_test'`. Ich habe der im Plan genannten Befehlsfolge den Vorrang gegeben, weil Task 3, Task 5 und Task 6 sie dreimal nennen, und die Abweichung im Fastfile kommentiert.
2. **`connect_api_key` als Helfer `request_api_key!`.** Die private Lane liegt in `platform :ios` und ist aus einer Lane auf Dateiebene nicht aufrufbar. Der Aufruf `app_store_connect_api_key(...)` steht jetzt einmal in `request_api_key!`, und die private Lane `connect_api_key` ruft nur noch diesen Helfer auf. Das Verhalten der iOS-Lanes ändert sich dadurch nicht.
3. **`before_all` auf Dateiebene.** Die macOS-Lanes brauchen das erzeugte Projekt genauso wie die iOS-Lanes. Ein Block auf Dateiebene läuft laut `runner.rb` auch für Lanes innerhalb eines Platform-Blocks, deshalb steht er jetzt genau einmal dort.
4. **`mac_beta` ist strenger als im Plan.** Zusätzlich zu `pkg:` und `skip_waiting_for_build_processing: true` stehen `app_platform: "osx"` und `distribute_external: false` in der Lane. Ohne die Plattformangabe liest pilot sie aus dem Paket, und wenn das misslingt, fragt es interaktiv nach. `distribute_external: false` entspricht der iOS-Lane `beta`.
5. **Keine Lane `mac_screenshots`.** Der Plan nennt als Bedingung, dass die App über ein Launch-Argument im Demo-Modus startet. Dieses Argument gibt es nicht: die UI-Tests nutzen `-stateUITesting` nur, um den Startbildschirm zu überspringen, und tippen den Demo-Modus danach über den Knopf `explore-demo` an (`ios/State/Sources/UI/ConnectView.swift`). Damit bräuchte eine Lane zusätzlich UI-Skripting mit Bedienungshilfen-Rechten sowie eine feste Fenstergröße von 2880x1800, was auf einem kleineren Display gar nicht geht. Ich habe deshalb die im Plan vorgesehene manuelle Anleitung geschrieben. Ein Launch-Argument `-stateDemo` in der App würde eine Lane später möglich machen, das ist aber eine Änderung an Produktivcode außerhalb meines Bereichs.
6. **`compose_ipad_frames.py` repariert.** Nicht im Task-Text, aber die Datei war mit ihren festen `/private/tmp`-Pfaden nicht mehr lauffähig, und damit wäre der iPad-Teil des WP-Ziels nicht erreichbar gewesen. Sie nimmt Quelle und Ziel jetzt als Argumente, die Vorgaben passen zur Snapfile-Ergänzung.
7. **iPad-Aufnahmen werden nicht in den Frame-Ordner kopiert.** Die Lane `screenshots` kopiert weiterhin nur die iPhone-Dateien nach `ios/fastlane/screenshots`. Die rohen iPad-Aufnahmen heißen `iPad Pro 13-inch (M4)-<name>.png`, die gerahmten Dateien heißen `iPad Pro 13-inch-<name>.png`. Ein Kopieren würde die gerahmten Dateien also nicht überschreiben, aber die Lane soll auch keine ungerahmten Bilder in den Ordner legen, den `deliver` hochlädt.

## Offene Fragen und Risiken

1. **Welle 3 ist nicht freigegeben.** Task 2 und der iPad-Navigationsteil aus Task 4 können erst nach dem Merge von WP06, WP09 und WP10 nachgeholt werden. Ich habe den Branch bewusst nicht auf die WP06-Branches aufgesetzt, weil das ein Merge fremder, noch nicht abgeschlossener Arbeit wäre.
2. **`mac_build` und `mac_beta` sind unverifiziert.** Sie brauchen ein Mac-Distributionszertifikat und einen Zugang zu App Store Connect. Ich habe sie deshalb nicht ausgeführt, wie es der Plan für `mac_beta` ohnehin vorschreibt.
3. **`fastlane test` ist unter Last gescheitert.** Einzelheiten unten. Die neue iPad-Zeile der Lane `test` ist einzeln verifiziert, der volle Lauf muss nach dem Abklingen der parallelen Workouts wiederholt werden.
4. **Der Mac braucht eine eigene Store-Einrichtung.** Die bestehenden Metadata- und Review-Lanes sind über `editable_app_store_version!` auf `Platform::IOS` festgelegt. Für macOS fehlen eine Version, Screenshots und Metadaten. Das ist in der Checkliste beschrieben, aber noch nicht automatisiert.
5. **Build-Nummer.** iOS und macOS teilen sich `MARKETING_VERSION` und die Quelle der Build-Nummer (`BUILD_NUMBER`, sonst UTC-Stempel). Ob App Store Connect dieselbe Build-Nummer gleichzeitig für iOS und macOS akzeptiert, habe ich nicht geprüft, weil das einen echten Upload bräuchte.

## Manuelle Schritte für Fabian oder den Koordinator

### Vor dem nächsten Anlauf von WP11

1. WP06 mergen. Danach enthält `ios/project.yml` das Target `StateMac` mit dem Scheme `StateMac`, und Task 2 kann nachgeholt werden.
2. WP09 mergen. Danach muss `ios/StateUITests/StateScreenshots.swift` auf die Seitenleiste umgestellt werden.
3. WP10 mergen. Danach ist der Runner-Befehl in der App kopierbar, einer der Prüfpunkte der Checkliste.
4. Die Abnahmebefehle auf einem ruhigen Rechner nachziehen: `cd ios && fastlane mac_test && fastlane test`, danach `git status --short`.

### Task 2, fertiger Diff für `ios/project.yml` nach dem Merge von WP06

Das iOS-Target benutzt automatisches Signing: kein `CODE_SIGN_STYLE`, kein `PROVISIONING_PROFILE_SPECIFIER`, dafür global `DEVELOPMENT_TEAM: 5DKU7FFK4X`, und die Lane `build` archiviert mit `-allowProvisioningUpdates`. Es gibt also keine manuellen Profilnamen, die man für den Mac übernehmen müsste, und die im Plan genannte Platzhaltervariable `$(STATE_MAC_PROFILE)` ist damit nicht nötig. Beim Target `StateMac` sind deshalb nur diese Zeilen zu ergänzen:

```yaml
    settings:
      base:
        CODE_SIGN_STYLE: Automatic
        CODE_SIGN_IDENTITY: "Apple Development"
        # die vorhandenen Zeilen bleiben
      configs:
        Release:
          CODE_SIGN_IDENTITY: "Apple Distribution"
```

Danach `cd ios && xcodegen generate` und `fastlane mac_test`.

### In App Store Connect, bevor `fastlane mac_beta` funktioniert

1. Beim bestehenden App-Eintrag `com.fabincrm.state` die Plattform macOS hinzufügen. Die Bundle-ID bleibt gleich, damit greift Universal Purchase.
2. Ein Zertifikat "Mac App Store Distribution", ein Zertifikat "Mac Installer Distribution" für das `.pkg` und ein Provisioning-Profil "Mac App Store" für `com.fabincrm.state` anlegen. Mit angemeldeter Apple-ID, die die App verwalten darf, legt Xcode die fehlenden Ressourcen beim ersten `fastlane mac_build` selbst an, weil die Lane mit `-allowProvisioningUpdates` archiviert.
3. Dem Mac-Profil die Keychain-Gruppe `com.fabincrm.state.shared` mitgeben, sonst kann die App die geteilten Server-Zugangsdaten nicht lesen.
4. Für die macOS-Plattform eine Version anlegen und die Mac-Screenshots hochladen. Die bestehenden Lanes fassen den macOS-Datensatz nicht an.
5. Die Export-Compliance-Antwort prüfen. `ITSAppUsesNonExemptEncryption` ist auch im macOS-`Info.plist` auf `false` gesetzt und muss zu `submission_info_defaults` passen.

## Läufe von `fastlane test` unter paralleler Last

Lauf 1, 02:24 Uhr. Der Lauf ist in `scan` gescheitert, also im iPhone-Test, noch bevor die neue iPad-Zeile überhaupt an die Reihe kam:

```
State encountered an error (Early unexpected exit, operation never finished bootstrapping -
no restart will be attempted. (Underlying Error: Test crashed with signal kill before establishing connection.))
StateUITests-Runner (79358) encountered an error (Early unexpected exit, operation never finished
bootstrapping - no restart will be attempted. (Underlying Error: Lost connection to testmanagerd))
** TEST FAILED **
```

Das ist kein Befund zu diesem WP: In derselben Minute liefen vier fremde `xcodebuild`-Prozesse aus den Worktrees von WP06, WP07 und WP10 auf demselben Simulatorgerät, die Last lag bei über 100, und eine fremde Sitzung hat währenddessen `xcrun simctl shutdown all` ausgeführt, was den Testläufer genau mit diesem Fehlerbild abschießt.

Lauf 2, 02:46 Uhr, nach einer Wartezeit auf freie Simulatoren. Ergebnis aus dem xcresult:

```
totalTestCount 41, passedTests 40, failedTests 1
testFailures: StateUITests/StateScreenshots/testAppStoreScreenshots()
              "Test crashed with signal kill."
```

Wieder ein Abschuss von außen und keine fehlgeschlagene Zusicherung. Derselbe Test läuft einzeln in 19 Sekunden grün:

```
cd ios
xcodebuild -project $PWD/State.xcodeproj -scheme State \
  -destination 'platform=iOS Simulator,name=iPhone 16 Pro,OS=18.5' \
  -derivedDataPath $PWD/build/DerivedData-wp11 CODE_SIGNING_ALLOWED=NO \
  test -only-testing:StateUITests/StateScreenshots
=> Executed 1 test, with 0 failures. ** TEST SUCCEEDED **
```

Weil `scan` beim ersten Fehler abbricht, lief die neue iPad-Zeile in keinem der beiden Läufe. Sie ist deshalb einzeln mit genau ihrem Befehl verifiziert, siehe "Prüfungen". Die App-Store-Screenshot-Tests sind unter dieser Last das empfindlichste Stück der Suite, weil sie am längsten laufen.

`fastlane diagnose` um 02:49 Uhr scheiterte im selben `scan`-Schritt, diesmal mit `Invalid device state` und `Mach error -308 (ipc/mig) server died`, also einem sterbenden CoreSimulator-Dienst. Die Schritte davor (`ruby --version`, `xcodebuild -version`, `xcodegen --version`) waren grün.

## Umgebung während dieser Sitzung

Auf demselben Rechner liefen parallel die Worker von WP06, WP07, WP08, WP09 und WP10 in ihren Worktrees, dazu weitere Sitzungen. Die Last lag zeitweise über 100, und Simulatoren wurden von fremden Sitzungen neu gestartet. Alle Läufe dieses Reports sind davon betroffen, die Ergebnisse sind entsprechend vorsichtig zu lesen.

Ein dritter Anlauf von `fastlane test` nach dem Merge mit `origin/main` wurde abgebrochen, weil WP09 zu diesem Zeitpunkt denselben Simulator für seine eigenen Tests benutzte und ich diesen Lauf nicht stören wollte. Er gehört in den nächsten Anlauf, siehe "Manuelle Schritte".

