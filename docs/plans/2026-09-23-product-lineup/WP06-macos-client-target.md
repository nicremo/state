# WP06: macOS-Client-Target und Plattform-Schicht

**Welle:** 1 · **Slug:** `macos-client-target` · **Branch:** `wp/06-macos-client-target`
**Besitzt:** `ios/project.yml`, `ios/State.xcodeproj/**` (nur durch `xcodegen generate`, nie von Hand), `ios/State/Sources/**`, neu `ios/State/Sources/Platform/**`, neu `ios/StateMac/**`, `ios/StateTests/**`
**Geschätzter Umfang:** groß (Swift, viele Dateien, aber jede Änderung ist mechanisch)

## Goal-Prompt (so an die Worker-Session geben)

> Lies `docs/plans/2026-09-23-product-lineup/README.md` vollständig und halte dich an das Arbeitsprotokoll in Abschnitt 4. Setze danach `docs/plans/2026-09-23-product-lineup/WP06-macos-client-target.md` Task für Task um. Die iPhone-App muss nach jedem Task unverändert bauen und alle Tests bestehen. Du änderst kein Layout und kein Verhalten der iPhone-App, du machst den Code nur plattformfähig. Schließe mit Report und Draft-PR ab.

## Ziel

Ein neues natives macOS-Target `StateMac` im selben Xcode-Projekt, das **denselben** Swift-Code wie die iPhone-App kompiliert und startet. UIKit-only-APIs werden hinter eine kleine Plattform-Schicht gelegt. Das Layout bleibt in diesem WP noch wie auf dem iPhone (Tabs). Das Seitenleisten-Layout für iPad und Mac kommt in WP09.

**Nicht-Ziele:** kein Mac Catalyst, keine neue UI, kein Runner (WP10), keine Push-Registrierung auf dem Mac (App Attest gibt es dort nicht), keine Signierung für den App Store (WP11).

## Pflichtlektüre

1. `ios/project.yml` (vollständig)
2. `ios/State/Sources/App/StateApp.swift`, `ios/State/Sources/App/StateRootView.swift`
3. `ios/State/Sources/Notifications/StateAppDelegate.swift`, `NotificationCoordinator.swift`, `PushRegistrationService.swift`
4. `ios/State/Sources/Security/SharedKeychain.swift`
5. `ios/State/Sources/UI/StateTheme.swift`, `ConnectView.swift`
6. `ios/State/Supporting/Info.plist` und die Entitlements-Dateien in `ios/State/Supporting/`

## Sichere Befehle (immer genau diese verwenden)

iPhone bauen und testen (muss nach jedem Task grün sein):

```bash
cd ios && xcodegen generate && cd ..
xcodebuild -project ios/State.xcodeproj -scheme State \
  -destination 'platform=iOS Simulator,name=iPhone 16 Pro,OS=18.5' \
  -derivedDataPath build/DerivedData-wp06 CODE_SIGNING_ALLOWED=NO test 2>&1 | tail -20
```

Mac bauen (ab Task 3):

```bash
xcodebuild -project ios/State.xcodeproj -scheme StateMac \
  -destination 'platform=macOS' \
  -derivedDataPath build/DerivedData-wp06 CODE_SIGNING_ALLOWED=NO build 2>&1 | grep -E "error:|warning: .*deprecated|BUILD" | tail -40
```

## Vollständige Liste der UIKit-Stellen (Stand `main`)

Diese Liste ist die Arbeitsliste. Nach Task 2 darf `grep` keine dieser Stellen mehr ungeschützt finden.

```text
UI/DocumentationView.swift:2     import UIKit
UI/DocumentationView.swift:87    .listStyle(.insetGrouped)
UI/DocumentationView.swift:90    .navigationBarTitleDisplayMode(.inline)
UI/DocumentationView.swift:142   UIPasteboard.general.string = ...
UI/MainTabView.swift:94          .listStyle(.insetGrouped)
UI/MainTabView.swift:106         ToolbarItem(placement: .topBarLeading)
UI/ConnectView.swift:2           import UIKit
UI/ConnectView.swift:3           import VisionKit
UI/ConnectView.swift:23          UIDevice.current.name
UI/ConnectView.swift:118         .keyboardType(.URL)
UI/ConnectView.swift:119         .textInputAutocapitalization(.never)
UI/ConnectView.swift:187         DataScannerViewController.isSupported
UI/ConnectView.swift:234         .navigationBarTitleDisplayMode(.inline)
UI/ConnectView.swift:321-370     struct PairingScannerView: UIViewControllerRepresentable (ganzer Typ)
UI/SettingsView.swift:2          import UIKit
UI/SettingsView.swift:109        UIPasteboard.general.string = ...
UI/SettingsView.swift:282        .textInputAutocapitalization(.never)
UI/SettingsView.swift:358        UIPasteboard.general.string = ...
UI/SettingsView.swift:526        UIApplication.openNotificationSettingsURLString
UI/SettingsView.swift:533        .navigationBarTitleDisplayMode(.inline)
UI/StateTheme.swift:2,12-43      import UIKit, UIColor { traits in ... } (4 dynamische Farben)
UI/ReminderDetailView.swift:86   .listStyle(.insetGrouped)
UI/ReminderDetailView.swift:102  .navigationBarTitleDisplayMode(.inline)
UI/PolicyEditorView.swift:115    .textInputAutocapitalization(.never)
UI/PolicyEditorView.swift:144    .textInputAutocapitalization(.never)
UI/PolicyEditorView.swift:205    .navigationBarTitleDisplayMode(.inline)
UI/RunDetailView.swift:233       .listStyle(.insetGrouped)
UI/RunDetailView.swift:235       .navigationBarTitleDisplayMode(.inline)
UI/ReminderEditorView.swift:37   .textInputAutocapitalization(.sentences)
UI/ReminderEditorView.swift:101  .navigationBarTitleDisplayMode(.inline)
UI/ActivityView.swift:73         .listStyle(.insetGrouped)
UI/ActivityView.swift:138        .navigationBarTitleDisplayMode(.inline)
App/StateApp.swift:5             @UIApplicationDelegateAdaptor(StateAppDelegate.self)
Notifications/StateAppDelegate.swift  ganze Datei (UIApplicationDelegate)
Notifications/PushRegistrationService.swift  DeviceCheck / DCAppAttestService
Notifications/NotificationCoordinator.swift:1,27  import UIKit, registerForRemoteNotifications()
```

Zeilennummern können leicht abweichen. Mit diesem Befehl findest du alle Stellen neu:

```bash
grep -rnE "UIKit|UIDevice|UIPasteboard|UIApplication|UIColor|UIViewController|insetGrouped|navigationBarTitleDisplayMode|textInputAutocapitalization|keyboardType|DataScanner|VisionKit|topBarLeading|topBarTrailing|DeviceCheck|DCAppAttest" ios/State/Sources
```

## Task 1: Plattform-Schicht anlegen

**Datei:** Create `ios/State/Sources/Platform/Platform.swift`

Dieser Code ist vollständig. Übernimm ihn so:

```swift
import SwiftUI
#if os(iOS)
import UIKit
#elseif os(macOS)
import AppKit
#endif

/// The few platform services the shared UI needs. Everything else in the
/// app is plain SwiftUI and compiles unchanged on iOS, iPadOS and macOS.
enum Platform {
    static func copyToPasteboard(_ text: String) {
        #if os(iOS)
        UIPasteboard.general.string = text
        #elseif os(macOS)
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        #endif
    }

    static var deviceName: String {
        #if os(iOS)
        UIDevice.current.name
        #elseif os(macOS)
        Host.current().localizedName ?? "Mac"
        #endif
    }

    /// Opens the system settings page for this app's notifications.
    static var notificationSettingsURL: URL? {
        #if os(iOS)
        URL(string: UIApplication.openNotificationSettingsURLString)
        #elseif os(macOS)
        URL(string: "x-apple.systempreferences:com.apple.Notifications-Settings.extension")
        #endif
    }

    static var supportsQRScanning: Bool {
        #if os(iOS)
        true
        #else
        false
        #endif
    }
}

extension Color {
    /// A color that resolves differently in light and dark appearance.
    init(light: (Double, Double, Double, Double), dark: (Double, Double, Double, Double)) {
        #if os(iOS)
        self.init(uiColor: UIColor { traits in
            let value = traits.userInterfaceStyle == .dark ? dark : light
            return UIColor(red: value.0, green: value.1, blue: value.2, alpha: value.3)
        })
        #elseif os(macOS)
        self.init(nsColor: NSColor(name: nil) { appearance in
            let isDark = appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
            let value = isDark ? dark : light
            return NSColor(srgbRed: value.0, green: value.1, blue: value.2, alpha: value.3)
        })
        #endif
    }
}

extension View {
    /// Inset grouped lists on iOS, the native inset style on macOS.
    @ViewBuilder
    func stateListStyle() -> some View {
        #if os(iOS)
        self.listStyle(.insetGrouped)
        #else
        self.listStyle(.inset)
        #endif
    }

    /// Inline navigation titles exist only on iOS.
    @ViewBuilder
    func stateInlineNavigationTitle() -> some View {
        #if os(iOS)
        self.navigationBarTitleDisplayMode(.inline)
        #else
        self
        #endif
    }

    @ViewBuilder
    func stateNoAutocapitalization() -> some View {
        #if os(iOS)
        self.textInputAutocapitalization(.never)
        #else
        self
        #endif
    }

    @ViewBuilder
    func stateSentenceAutocapitalization() -> some View {
        #if os(iOS)
        self.textInputAutocapitalization(.sentences)
        #else
        self
        #endif
    }

    @ViewBuilder
    func stateURLKeyboard() -> some View {
        #if os(iOS)
        self.keyboardType(.URL)
        #else
        self
        #endif
    }
}

extension ToolbarItemPlacement {
    /// Leading bar position on iOS, the navigation area on macOS.
    static var stateLeading: ToolbarItemPlacement {
        #if os(iOS)
        .topBarLeading
        #else
        .navigation
        #endif
    }
}
```

**Prüfen:** Prüfe in `StateTheme.swift`, ob die Farben dort über `traits.userInterfaceStyle == .dark` unterscheiden. Wenn die Logik anders ist (z.B. andere Trait-Abfrage), passe `Color(light:dark:)` so an, dass die iPhone-Farben **exakt** gleich bleiben.

iPhone-Build und Tests ausführen (siehe "Sichere Befehle"). Erwartet: grün.

Commit: `git commit -m "feat: add a small platform layer for shared swiftui code"`

## Task 2: Alle UIKit-Stellen umstellen

Arbeite die Liste oben Datei für Datei ab. Ersetzungen:

| Alt | Neu |
| --- | --- |
| `.listStyle(.insetGrouped)` | `.stateListStyle()` |
| `.navigationBarTitleDisplayMode(.inline)` | `.stateInlineNavigationTitle()` |
| `.textInputAutocapitalization(.never)` | `.stateNoAutocapitalization()` |
| `.textInputAutocapitalization(.sentences)` | `.stateSentenceAutocapitalization()` |
| `.keyboardType(.URL)` | `.stateURLKeyboard()` |
| `UIPasteboard.general.string = X` | `Platform.copyToPasteboard(X)` |
| `UIDevice.current.name` | `Platform.deviceName` |
| `URL(string: UIApplication.openNotificationSettingsURLString)` | `Platform.notificationSettingsURL` |
| `ToolbarItem(placement: .topBarLeading)` | `ToolbarItem(placement: .stateLeading)` |
| `Color(uiColor: UIColor { traits in ... })` in `StateTheme.swift` | `Color(light: (r, g, b, a), dark: (r, g, b, a))` mit **denselben** Zahlen |
| `import UIKit` in UI-Dateien | entfernen, wenn danach nichts mehr UIKit braucht |

Beispiel `StateTheme.swift` (Zahlen aus der Datei übernehmen, nicht aus diesem Beispiel):

```swift
static let accent = Color(light: (0.22, 0.26, 0.62, 1), dark: (0.62, 0.66, 0.98, 1))
```

**ConnectView (QR-Scanner):**
- `import VisionKit` in `#if os(iOS)` einschließen.
- Den gesamten Typ `PairingScannerView` (und seinen `Coordinator`) in `#if os(iOS) ... #endif` einschließen.
- Jede Stelle, die den Scanner zeigt oder `DataScannerViewController.isSupported` prüft, in `#if os(iOS) ... #endif` einschließen. Auf dem Mac bleibt die manuelle Eingabe (Server-Adresse, Code) sichtbar.
- Existiert eine Funktion, die eine gescannte Pairing-URL auswertet (z.B. `apply(payload)` oder ähnlich): Biete auf dem Mac zusätzlich ein Textfeld "Pairing-Link einfügen" an, dessen Inhalt durch **dieselbe** Auswertungsfunktion läuft. Umschließe das Feld mit `#if os(macOS)`. Wenn es keine solche Funktion gibt, lass es weg und notiere es im Report.

Nach jeder Datei: iPhone-Build. Am Ende iPhone-Tests.

Kontrolle:

```bash
grep -rnE "UIPasteboard|UIDevice|insetGrouped|navigationBarTitleDisplayMode|textInputAutocapitalization|keyboardType|topBarLeading|UIColor" ios/State/Sources | grep -v "Platform/Platform.swift"
```

Erwartet: keine Ausgabe.

Commit (gerne mehrere, z.B. pro 2 bis 3 Dateien): `git commit -m "refactor: route ui platform calls through the platform layer"`

## Task 3: App-Delegate, Benachrichtigungen und Push trennen

Ziel: iOS behält exakt sein Verhalten, macOS kompiliert ohne UIKit und ohne App Attest.

1. **`StateAppDelegate.swift`:** Lies die Datei. Sie enthält (a) UIApplicationDelegate-Code (Launch, Remote-Notification-Token) und (b) `UNUserNotificationCenterDelegate`-Code (Aktionen aus Mitteilungen).
   - Verschiebe (b) in eine neue Datei `ios/State/Sources/Notifications/NotificationCenterDelegate.swift` als eigene Klasse `final class NotificationCenterDelegate: NSObject, UNUserNotificationCenterDelegate`. Die Logik (was bei welcher Aktion passiert, welche Notification-Center-Posts ausgelöst werden) bleibt 1:1 gleich.
   - `StateAppDelegate` behält (a), hält eine Instanz von `NotificationCenterDelegate` und setzt sie in `didFinishLaunching` als `UNUserNotificationCenter.current().delegate`, genau dort, wo heute `self` gesetzt wird.
   - Die ganze Datei `StateAppDelegate.swift` in `#if os(iOS) ... #endif` einschließen.
2. **`NotificationCoordinator.swift`:** `import UIKit` und `UIApplication.shared.registerForRemoteNotifications()` in `#if os(iOS)` einschließen. Lokale Mitteilungen (`UNUserNotificationCenter`) bleiben auf beiden Plattformen.
3. **`PushRegistrationService.swift`:** `import DeviceCheck` und alle Stellen mit `DCAppAttestService` in `#if os(iOS)` einschließen. Auf macOS soll `registerIfSupported(...)` sofort zurückkehren. Am einfachsten: die öffentliche Funktion bekommt als erste Zeile
   ```swift
   #if !os(iOS)
   return
   #endif
   ```
   und alle iOS-spezifischen Teile stehen in `#if os(iOS)`. Achte darauf, dass keine "will never be executed"-Warnung zum Fehler wird. Wenn doch: den iOS-Körper komplett in `#if os(iOS) ... #else return #endif` einschließen.
4. **`StateApp.swift`:**
   ```swift
   #if os(iOS)
   @UIApplicationDelegateAdaptor(StateAppDelegate.self) private var appDelegate
   #elseif os(macOS)
   @NSApplicationDelegateAdaptor(StateMacAppDelegate.self) private var appDelegate
   #endif
   ```
   Wird `appDelegate` im Body benutzt (z.B. für das APNs-Token), diese Stellen ebenfalls in `#if os(iOS)` einschließen. Suche danach mit `grep -n appDelegate ios/State/Sources -r`.
5. **Neue Datei** `ios/StateMac/Sources/StateMacAppDelegate.swift`:
   ```swift
   import AppKit
   import UserNotifications

   /// macOS has no APNs route for State (the relay requires App Attest), so
   /// the delegate only wires up local notification actions.
   final class StateMacAppDelegate: NSObject, NSApplicationDelegate {
       private let notificationDelegate = NotificationCenterDelegate()

       func applicationDidFinishLaunching(_ notification: Notification) {
           UNUserNotificationCenter.current().delegate = notificationDelegate
       }

       func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
           false
       }
   }
   ```
   Wenn `NotificationCenterDelegate` einen anderen Initializer braucht (z.B. Registrierung von Kategorien), gleiche das an.
6. **`StateRootView.swift`** (in `App/`): Alles, was `.onReceive` auf APNs-Token-Notifications macht oder `pushRegistrationService` für Remote-Push nutzt, in `#if os(iOS)` einschließen. Den 15-Sekunden-Poll (`.task(id: scenePhase)`) **nicht** einschließen, der ist auf dem Mac gewollt. Prüfe dort die Bedingung `model.session?.certificateFingerprint != nil`: Auf dem Mac soll immer gepollt werden, solange die App aktiv ist. Ergänze deshalb:
   ```swift
   #if os(macOS)
   let shouldPoll = !model.isDemo && model.session != nil
   #else
   let shouldPoll = model.session?.certificateFingerprint != nil && !model.isDemo
   #endif
   ```
   und nutze `shouldPoll` in der Bedingung.
7. **`SharedKeychain.swift`:** Lies die Datei. Auf macOS muss für Access Groups der Data-Protection-Keychain benutzt werden. Ergänze bei **jeder** Query (add, copy, update, delete) unter `#if os(macOS)` den Eintrag `kSecUseDataProtectionKeychain as String: true`. Die iOS-Queries bleiben unverändert.

iPhone-Build und iPhone-Tests. Erwartet: grün, identisches Verhalten.

Commit: `git commit -m "refactor: separate ios only push and delegate code from shared notifications"`

## Task 4: Target `StateMac` in XcodeGen

**Dateien:**
- Modify: `ios/project.yml`
- Create: `ios/StateMac/Info.plist`
- Create: `ios/StateMac/StateMac.entitlements`
- Create: `ios/StateMac/Resources/MacAssets.xcassets/Contents.json` und `AppIconMac.appiconset/`

1. `ios/project.yml`, unter `options.deploymentTarget` ergänzen: `macOS: "15.0"`.
2. Neues Target unter `targets:` (Einrückung wie beim Target `State`):
   ```yaml
   StateMac:
     type: application
     platform: macOS
     sources:
       - path: State/Sources
       - path: ../shared/LocalTransport
       - path: State/Resources
       - path: StateMac/Sources
       - path: StateMac/Resources
     dependencies:
       - package: GRDB
         product: GRDB
     settings:
       base:
         PRODUCT_BUNDLE_IDENTIFIER: com.fabincrm.state
         PRODUCT_NAME: State
         INFOPLIST_FILE: StateMac/Info.plist
         CODE_SIGN_ENTITLEMENTS: StateMac/StateMac.entitlements
         ASSETCATALOG_COMPILER_APPICON_NAME: AppIconMac
         ENABLE_HARDENED_RUNTIME: YES
         MACOSX_DEPLOYMENT_TARGET: "15.0"
     scheme:
       gatherCoverageData: false
   ```
   Prüfe beim Target `State`, ob `../shared/LocalTransport` dort genauso eingebunden ist, und übernimm exakt dieselbe Schreibweise.
3. `ios/StateMac/Info.plist`: Nimm `ios/State/Supporting/Info.plist` als Vorlage. Übernimm nur Schlüssel, die es auf macOS gibt: `CFBundle*`-Schlüssel, `CFBundleShortVersionString = $(MARKETING_VERSION)`, `CFBundleVersion = $(CURRENT_PROJECT_VERSION)`, `LSApplicationCategoryType = public.app-category.productivity`, `NSLocalNetworkUsageDescription` und `NSBonjourServices` (falls in der iOS-Plist vorhanden, gleicher Text), `LSMinimumSystemVersion = $(MACOSX_DEPLOYMENT_TARGET)`. **Nicht** übernehmen: `UIBackgroundModes`, `UILaunchScreen`, `UISupportedInterfaceOrientations*`, `UIApplicationSceneManifest`, `NSCameraUsageDescription`.
4. `ios/StateMac/StateMac.entitlements`:
   ```xml
   <?xml version="1.0" encoding="UTF-8"?>
   <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
   <plist version="1.0">
   <dict>
       <key>com.apple.security.app-sandbox</key>
       <true/>
       <key>com.apple.security.network.client</key>
       <true/>
       <key>keychain-access-groups</key>
       <array>
           <string>$(AppIdentifierPrefix)com.fabincrm.state</string>
       </array>
   </dict>
   </plist>
   ```
   Lies `SharedKeychain.swift`: Wenn dort eine andere Access Group oder eine App Group benutzt wird, trage **diese** ein und nenne es im Report.
5. Mac-App-Icon aus dem vorhandenen 1024er-Icon erzeugen:
   ```bash
   SRC=ios/State/Resources/Assets.xcassets/AppIcon.appiconset/AppIcon-1024.png
   DST=ios/StateMac/Resources/MacAssets.xcassets/AppIconMac.appiconset
   mkdir -p "$DST"
   printf '{\n  "info" : { "author" : "xcode", "version" : 1 }\n}\n' > ios/StateMac/Resources/MacAssets.xcassets/Contents.json
   for S in 16 32 128 256 512; do
     sips -z $S $S "$SRC" --out "$DST/icon_${S}x${S}.png" >/dev/null
     D=$((S*2)); sips -z $D $D "$SRC" --out "$DST/icon_${S}x${S}@2x.png" >/dev/null
   done
   ```
   Prüfe vorher, ob `AppIcon-1024.png` unter diesem Pfad existiert (`ls ios/State/Resources/Assets.xcassets/AppIcon.appiconset/`). Dann `Contents.json` im `AppIconMac.appiconset` mit den zehn Einträgen schreiben (`"idiom" : "mac"`, `"size" : "16x16"`, `"scale" : "1x"` bzw. `"2x"`, `"filename"` passend).
6. `cd ios && xcodegen generate && cd ..`
7. Mac bauen (siehe "Sichere Befehle"). Erwartet: `** BUILD SUCCEEDED **`. Kompilierfehler zeigen fast immer eine vergessene iOS-API. Behebe sie mit der Plattform-Schicht oder `#if os(iOS)`, **nie** durch Löschen von iOS-Funktionalität.
8. iPhone-Build und iPhone-Tests. Erwartet: grün.

Commit: `git commit -m "feat: add a native macos target that shares the ios code"`

## Task 5: Mac-App startet

1. Mac-App bauen (siehe oben) und die gebaute App finden:
   ```bash
   APP=$(find build/DerivedData-wp06/Build/Products -maxdepth 2 -name "State.app" -path "*Debug/*" | head -1); echo "$APP"
   ```
2. 10 Sekunden starten und prüfen, dass sie nicht abstürzt:
   ```bash
   "$APP/Contents/MacOS/State" & PID=$!; sleep 10
   if kill -0 $PID 2>/dev/null; then echo "RUNNING_OK"; kill $PID; else echo "CRASHED"; fi
   ```
   Hinweis: Ohne Signierung kann die Keychain-Nutzung scheitern. Ein **Absturz** ist ein Fehler, eine Fehlermeldung in der App ("Keychain nicht verfügbar" o.ä.) ist akzeptabel und kommt in den Report.
3. Einen Screenshot machen und im Report verlinken (Datei **nicht** committen):
   ```bash
   "$APP/Contents/MacOS/State" & PID=$!; sleep 6
   screencapture -x /tmp/wp06-mac.png; kill $PID
   ```
   Prüfe das Bild selbst (öffnen oder mit einem Bild-Lesewerkzeug), beschreibe im Report in zwei Sätzen, was man sieht.
4. Demo-Modus testen, falls der Onboarding-Bildschirm einen "Demo"-Knopf hat: nur beschreiben, nicht automatisieren.

Commit nur, falls Fixes nötig waren.

## Task 6: Gesamtprüfung

```bash
cd ios && xcodegen generate && cd ..
git status --short ios/State.xcodeproj    # nur Änderungen durch xcodegen
xcodebuild -project ios/State.xcodeproj -scheme State -destination 'platform=iOS Simulator,name=iPhone 16 Pro,OS=18.5' -derivedDataPath build/DerivedData-wp06 CODE_SIGNING_ALLOWED=NO test 2>&1 | tail -5
xcodebuild -project ios/State.xcodeproj -scheme StateMac -destination 'platform=macOS' -derivedDataPath build/DerivedData-wp06 CODE_SIGNING_ALLOWED=NO build 2>&1 | tail -5
grep -rnE "UIKit|UIDevice|UIPasteboard|UIApplication|UIColor|DeviceCheck|DCAppAttest|VisionKit" ios/State/Sources | grep -v "#if os(iOS)"
```

Die letzte Zeile darf nur Treffer zeigen, die innerhalb von `#if os(iOS)`-Blöcken stehen. Prüfe jeden Treffer von Hand.

`build/` darf nicht committet werden. Prüfe, dass `build/` in `.gitignore` steht. Wenn nicht, ergänzen und im Report nennen.

Report und Draft-PR nach Master-Plan Abschnitt 4.4 und 4.5. Im Report: Liste aller Stellen, an denen du vom Plan abweichen musstest.
