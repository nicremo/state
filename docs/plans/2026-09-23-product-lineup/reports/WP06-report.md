# WP06 Report: macOS-Client-Target und Plattform-Schicht

**Status:** DONE
**Branch:** wp/06-macos-client-target
**Letzter Commit:** 4f4b88c feat: add a native macos target that shares the ios code

## Ergebnis in drei Sätzen

Es gibt jetzt ein natives macOS-Target `StateMac` im selben Xcode-Projekt, das denselben Swift-Code wie die iPhone-App kompiliert, startet und die Verbindungsmaske nativ auf dem Mac zeigt. Alle UIKit-, VisionKit- und DeviceCheck-Aufrufe liegen hinter einer kleinen Plattform-Schicht (`ios/State/Sources/Platform/Platform.swift`) oder in `#if os(iOS)`-Blöcken, sodass die iPhone-App kein sichtbares Verhalten ändert. Das Layout bleibt in diesem WP unverändert bei den Tabs, die Seitenleiste für iPad und Mac kommt in WP09.

## Erledigte Tasks

- [x] Task 1: Plattform-Schicht `ios/State/Sources/Platform/Platform.swift` angelegt (Pasteboard, Gerätename, Notification-Settings-URL, QR-Unterstützung, `Color(light:dark:)`, `stateListStyle`, `stateInlineNavigationTitle`, `stateNoAutocapitalization`, `stateSentenceAutocapitalization`, `stateURLKeyboard`, `stateLeading`).
- [x] Task 2: Alle UIKit-Stellen umgestellt. Die Kontrolle `grep` liefert keine ungeschützten Treffer mehr, zusätzlich per Skript über die `#if`-Verschachtelung geprüft (0 Treffer außerhalb von `#if os(iOS)`).
- [x] Task 3: App-Delegate, Benachrichtigungen und Push getrennt. `NotificationCenterDelegate` ist eigenständig, `StateAppDelegate` ist iOS-only, `StateMacAppDelegate` neu, `PushRegistrationService` und `NotificationCoordinator` sind plattformfähig, `SharedKeychain` nutzt auf dem Mac den Data-Protection-Keychain.
- [x] Task 4: Target `StateMac` in XcodeGen, Info.plist, Entitlements und Mac-App-Icon erzeugt. `** BUILD SUCCEEDED **`.
- [x] Task 5: Mac-App startet und überlebt 10 Sekunden (`RUNNING_OK`), Screenshot geprüft, siehe unten.
- [x] Task 6: Gesamtprüfung, siehe Abschnitt Prüfungen.
- [x] Report und Draft-PR.

## Geänderte Dateien

| Datei | Änderung |
| --- | --- |
| `ios/State/Sources/Platform/Platform.swift` | Neu. Die wenigen Plattformdienste der geteilten UI |
| `ios/State/Sources/UI/StateTheme.swift` | `import UIKit` entfernt, vier Farben über `Color(light:dark:)` mit identischen Zahlen |
| `ios/State/Sources/UI/ConnectView.swift` | `import UIKit`/`VisionKit` in `#if os(iOS)`, `PairingScannerView` iOS-only, Scanner-Knopf iOS-only, auf dem Mac ein Feld für den Kopplungslink über dieselbe Auswertungsfunktion |
| `ios/State/Sources/UI/DocumentationView.swift` | `import UIKit` entfernt, List-Stil und Titel über die Plattform-Schicht, Pasteboard über `Platform` |
| `ios/State/Sources/UI/SettingsView.swift` | `import UIKit` entfernt, Pasteboard und Notification-Settings-URL über `Platform`, List-Stil und Titel über die Plattform-Schicht |
| `ios/State/Sources/UI/OnboardingFlowView.swift` | Seiten-`TabView` bleibt auf iOS, auf dem Mac zeigt eine `switch`-Ansicht dieselben Seiten |
| `ios/State/Sources/UI/MainTabView.swift`, `ActivityView.swift`, `ReminderDetailView.swift`, `RunDetailView.swift`, `PolicyEditorView.swift`, `ReminderEditorView.swift` | List-Stil, Titelmodus, Autokapitalisierung und Toolbar-Platzierung über die Plattform-Schicht |
| `ios/State/Sources/Notifications/StateNotificationNames.swift` | Neu. Die geteilten Notification-Namen und `StateNotificationAction` |
| `ios/State/Sources/Notifications/NotificationCenterDelegate.swift` | Neu. Das `UNUserNotificationCenterDelegate`-Verhalten, unverändert übernommen |
| `ios/State/Sources/Notifications/StateAppDelegate.swift` | Nur noch iOS-Teil, ganz in `#if os(iOS)`, Kategorien kommen aus `NotificationCenterDelegate.activate()` |
| `ios/State/Sources/Notifications/NotificationCoordinator.swift` | `import UIKit` und `registerForRemoteNotifications()` in `#if os(iOS)` |
| `ios/State/Sources/Notifications/PushRegistrationService.swift` | `import DeviceCheck` in `#if os(iOS)`, Registrierung kehrt auf dem Mac sofort zurück, reine Helfer bleiben geteilt |
| `ios/State/Sources/App/StateApp.swift` | `@UIApplicationDelegateAdaptor` bzw. `@NSApplicationDelegateAdaptor` je Plattform |
| `ios/State/Sources/App/StateRootView.swift` | `.stateAPNSToken`-Empfang iOS-only, Poll-Bedingung als `shouldPoll` (Mac pollt mit Session, iPhone wie bisher nur mit gepinntem Zertifikat) |
| `ios/State/Sources/Security/SharedKeychain.swift` | `kSecUseDataProtectionKeychain` auf macOS in `baseQuery`, damit alle vier Query-Arten ihn tragen |
| `ios/State/Resources/Localizable.xcstrings` | Drei neue Keys (Kopplungslink, Kopplungslink verwenden, Hilfetext), Format und Sortierung von Xcode beibehalten |
| `ios/project.yml` | `macOS: "15.0"`, neues Target `StateMac` |
| `ios/State.xcodeproj/**` | Nur per `xcodegen generate` erzeugt |
| `ios/StateMac/Sources/StateMacAppDelegate.swift`, `ios/StateMac/Info.plist`, `ios/StateMac/StateMac.entitlements`, `ios/StateMac/Resources/MacAssets.xcassets/**` | Neu |

## Dateien außerhalb meines Bereichs

- `ios/State/Resources/Localizable.xcstrings`: Drei neue Keys für das Mac-Kopplungsfeld. Der Plan nennt den String-Katalog nicht unter "Besitzt", ohne die Keys wäre das neue Mac-Feld aber unübersetzt. Die Änderung beschränkt sich auf drei neue Einträge, der Diff ist 30 Zeilen lang und verändert keinen bestehenden Eintrag.
- `ios/State/Resources/de.lproj/` und `en.lproj/`: nicht angefasst.

## Prüfungen

| Befehl | Ergebnis |
| --- | --- |
| `cd ios && xcodegen generate` | grün, `git status` zeigt danach nur die erwartete `project.pbxproj` |
| `xcodebuild -scheme State -destination 'platform=iOS Simulator,name=iPhone 16 Pro,OS=18.5' test` | grün, 55 Tests, 0 Fehler, inklusive `StateUITests`. Ein früherer Lauf brach beim Bootstrap ab (Maschinenproblem, siehe unten), nach `xcrun simctl shutdown all` lief dieselbe Suite vollständig durch |
| `xcodebuild -scheme StateMac -destination 'platform=macOS' build` | `** BUILD SUCCEEDED **` |
| Mac-App starten, 10 Sekunden warten, `kill -0` | `RUNNING_OK`, kein Absturz |
| Skript über alle Swift-Dateien: jeder Treffer von `UIKit`, `UIDevice`, `UIPasteboard`, `UIApplication`, `UIColor`, `DeviceCheck`, `DCAppAttest`, `VisionKit`, `navigationBarTitleDisplayMode`, `textInputAutocapitalization`, `insetGrouped` innerhalb eines `#if os(iOS)`-Blocks | 0 ungeschützte Treffer |
| `git status` | sauber |

### Screenshot der Mac-App

Aufgenommen mit `screencapture -x -R <Fensterrahmen>` nach `screencapture`-Bild geprüft, Datei liegt unter `/tmp/wp06-mac.png` und ist **nicht** committet.

Zu sehen ist ein natives Mac-Fenster mit dem Titel `State` in dunkler Darstellung. Oben links das App-Icon und die Wortmarke `State`, darunter die Karte `Noch kein Server? Hier starten`, danach das Feld `Serveradresse` mit dem Platzhalter `https://state.example.com`, der Segmentumschalter `Art der Verbindung` mit `Ersteinrichtung` und `Kopplungscode`, das Feld `Bootstrap-Token` und der Hilfetext zu `state-server bootstrap-token`. Die geteilte SwiftUI-Oberfläche rendert also unverändert auf dem Mac, inklusive der Farben aus `StateTheme`, die über `Color(light:dark:)` in dunkler Erscheinung aufgelöst werden.

Bekannte Kosmetik: Die Wortmarke sagt auf dem Mac `Verbinde dieses iPhone mit deinem eigenen Server`. Der Text ist iPhone-spezifisch, aber WP06 darf den sichtbaren Text der iPhone-App nicht ändern. Eine plattformabhängige Formulierung ist ein eigener kleiner Task und steht unter Offene Fragen.

## Abweichungen vom Plan

1. **Der Plan legt `stateOpenNotificationSettings` und die anderen Notification-Namen in `StateAppDelegate.swift` ab.** Diese Datei ist jetzt komplett iOS-only, deshalb sind die Namen in die neue geteilte Datei `StateNotificationNames.swift` gewandert. Sonst gäbe es sie auf dem Mac nicht.
2. **Die Registrierung der Notification-Kategorien** lag im `didFinishLaunching` des iOS-Delegaten. Sie sitzt jetzt in `NotificationCenterDelegate.activate()`, das beide Delegaten aufrufen. Dadurch bekommt auch der Mac dieselben Aktionen `Complete`, `Snooze 10 minutes` und `Snooze 1 hour`, und das iOS-Verhalten bleibt identisch.
3. **`kSecUseDataProtectionKeychain`** steht in `baseQuery` statt in jeder einzelnen Query. Alle vier Operationen (add, copy, update, delete) bauen ihre Query über `baseQuery`, der Effekt ist derselbe, die Änderung ist eine Zeile.
4. **`ConnectView` auf dem Mac:** Der Plan verlangt ein Textfeld für den Kopplungslink, das durch dieselbe Auswertungsfunktion läuft. `applyScanned(_:)` wird dafür unverändert genutzt, die neue Funktion `applyPairingLink()` schneidet nur Leerraum ab.
5. **`OnboardingFlowView`:** `.tabViewStyle(.page(indexDisplayMode: .never))` gibt es auf macOS nicht. Auf iOS bleibt der `TabView` mit Seitenstil unverändert, auf dem Mac zeigt eine `switch`-Ansicht dieselben drei Seiten. Der Plan nennt diese Datei nicht, der Mac-Build bricht ohne die Anpassung aber ab.
6. **`ConnectView` und `SettingsView`:** `.navigationBarHidden(true)` und `UIApplication.openNotificationSettingsURLString` sind auf macOS nicht verfügbar. Beide Stellen sind jetzt über die Plattform-Schicht beziehungsweise `#if os(iOS)` geführt.
7. **Der Mac-Push-Status ist auf dem Mac `.unavailable`.** App Attest existiert dort nicht. Aufgerufen wird die Funktion auf dem Mac ohnehin nie, weil der APNs-Empfang iOS-only ist.

## Offene Fragen und Risiken

1. **Die iPhone-UI-Suite `StateUITests` ist auf dieser Maschine unzuverlässig.** Der Lauf bricht zeitweise beim Bootstrap ab, dann läuft kein Test (`Executed 0 tests`, `Early unexpected exit ... Test crashed with signal kill before establishing connection`). Belege: auf meinem Branch 4 von 8 Läufen grün, auf einem frischen Checkout von `origin/main` ohne eine Zeile WP06 ebenfalls 1 von 2 Läufen rot, und der WP10-Report auf `main` (Commit `aee782e`, PR #47) dokumentiert genau dasselbe Bild. Ursache ist die Maschine: die Platte war während der Läufe bei 99 bis 100 Prozent Belegung (6 bis 8 GB frei), und mehrere WP-Sessions haben gleichzeitig gebaut. Nach `xcrun simctl shutdown all` lief die vollständige Suite grün. Der Fehler ist nicht WP06. Empfehlung an den Koordinator: vor dem Merge von WP06 einen Lauf auf freier Platte ohne parallele Builds.
2. **Die Detail- und Inhaltsspalte fehlen noch.** Dieses WP liefert nur das Target und die Plattform-Schicht, das Layout bleibt bei den Tabs. Das ist so geplant, WP09 baut darauf auf.
3. **`StateMac` und `State` teilen Bundle-ID und Produktnamen.** Beide Targets heißen als Produkt `State` (`com.fabincrm.state`), das ist für einen App-Store-Eintrag gewollt. Im geteilten Derived-Data-Ordner liegen sie unter `Debug/` und `Debug-iphonesimulator/`. Beim Testen ist das unkritisch, aber ein Werkzeug, das nur nach `State.app` sucht, muss den Pfad beachten.
4. **Kosmetik auf dem Mac:** Die Verbindungsmaske spricht von "diesem iPhone", und der Knopf in den Notification-Einstellungen heißt `Open iOS notification settings`. Beides ist iPhone-Text, den WP06 nicht ändern darf.

## Manuelle Schritte für Fabian oder den Koordinator

1. WP06 gegen aktuelles `main` mergen. Der Branch ist auf `aee782e` rebased, der Konflikt in `ios/State.xcodeproj/project.pbxproj` ist aufgelöst, indem die Datei per `xcodegen generate` neu erzeugt wurde.
2. Nach dem Merge WP09 freigeben, das ist die Freigabe für Welle 2.
3. Für eine echte Mac-Signierung später: `ios/StateMac/StateMac.entitlements` trägt `app-sandbox`, `network.client` und die Keychain-Zugriffsgruppe `$(AppIdentifierPrefix)com.fabincrm.state.shared`, dieselbe Gruppe wie die iOS-App.
4. Vor dem nächsten großen Testlauf Platz schaffen. Die `build/DerivedData-*`-Ordner der abgeschlossenen WPs belegen mehrere Gigabyte.
