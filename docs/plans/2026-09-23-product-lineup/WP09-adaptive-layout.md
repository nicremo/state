# WP09: iPad- und Mac-Layout mit `NavigationSplitView`

**Welle:** 2 (erst starten, wenn WP06 auf `main` ist) · **Slug:** `adaptive-layout` · **Branch:** `wp/09-adaptive-layout`
**Besitzt:** `ios/State/Sources/UI/MainTabView.swift`, neu `ios/State/Sources/UI/SplitRootView.swift`, neu `ios/State/Sources/UI/AdaptiveRootView.swift`, `ios/State/Sources/App/StateRootView.swift` (nur die Stelle, an der `MainTabView` erzeugt wird), `ios/State/Sources/App/StateApp.swift` (nur `.commands`), `ios/State/Resources/Localizable.xcstrings` (nur neue Keys), `ios/StateUITests/**`
**Nicht anfassen:** `SettingsView.swift` (WP07), Runner-Dateien (WP10)
**Geschätzter Umfang:** mittel (SwiftUI)

## Goal-Prompt (so an die Worker-Session geben)

> Lies `docs/plans/2026-09-23-product-lineup/README.md` vollständig und halte dich an das Arbeitsprotokoll in Abschnitt 4. Setze danach `docs/plans/2026-09-23-product-lineup/WP09-adaptive-layout.md` Task für Task um. Auf dem iPhone darf sich nichts sichtbar ändern. Schließe mit Report, Screenshots (nicht committen, nur im Report beschreiben) und Draft-PR ab.

## Ziel

- **iPhone** (kompakte Breite): unverändert `TabView` mit Heute, Geplant, Aktivität, Einstellungen.
- **iPad** (reguläre Breite) und **Mac**: `NavigationSplitView` mit drei Spalten:
  1. Seitenleiste: Heute, Geplant, Aktivität (mit Konflikt-Badge), Einstellungen.
  2. Inhaltsspalte: die jeweilige Liste.
  3. Detailspalte: `ReminderDetailView` des ausgewählten Reminders, sonst ein Platzhalter "Wähle eine Erinnerung".
- **Mac:** Menübefehle "Neue Erinnerung" (⌘N) und "Synchronisieren" (⌘R).

## Pflichtlektüre

1. `ios/State/Sources/UI/MainTabView.swift` vollständig. Wichtig: `StateTab`, `ReminderCollectionView(model:mode:)`, wie die Liste Navigation macht (`NavigationStack(path:)`), wie der Editor als Sheet geöffnet wird, `revealCreatedReminder`.
2. `ios/State/Sources/UI/ActivityView.swift`, `ReminderDetailView.swift`, `SettingsView.swift` (nur die Initializer).
3. `ios/State/Sources/App/StateRootView.swift` (wo `MainTabView` erzeugt wird).
4. Apple-Doku zu `NavigationSplitView` über context7 (`/websites/developer_apple_swiftui` oder die Suche `swiftui navigationsplitview`), falls nötig.

## Task 1: Auswahlbasierte Liste ermöglichen

`ReminderCollectionView` navigiert heute selbst per `NavigationStack`. In der Split-Ansicht soll die Liste stattdessen eine **Auswahl** setzen.

1. Ergänze in `ReminderCollectionView` einen optionalen Parameter `selection: Binding<String?>? = nil` (Reminder-ID).
2. Wenn `selection` gesetzt ist: kein eigener `NavigationStack`, die Zeilen nutzen `List(selection:)` bzw. `.tag(reminder.id)`, und ein Tipp setzt die Auswahl. Wenn `selection` nil ist: exakt das bisherige Verhalten (iPhone).
3. Kein Code duplizieren: Den gemeinsamen Listeninhalt in eine private `@ViewBuilder`-Eigenschaft ziehen, die beide Varianten nutzen.

iPhone-Tests ausführen (Master-Plan). Erwartet: grün, iPhone unverändert.

Commit: `git commit -m "refactor: let the reminder list drive an external selection"`

## Task 2: `SplitRootView`

**Datei:** `ios/State/Sources/UI/SplitRootView.swift`

```swift
import SwiftUI

/// Three-column layout for iPad and Mac. The iPhone keeps MainTabView.
struct SplitRootView: View {
    @Bindable var model: AppModel
    @State private var section: StateTab? = .today
    @State private var selectedReminderID: String?
    @State private var opensNotificationSettings = false
    @State private var columnVisibility: NavigationSplitViewVisibility = .all

    var body: some View {
        NavigationSplitView(columnVisibility: $columnVisibility) {
            List(selection: $section) {
                Label(String(localized: "Today"), systemImage: "sun.max.fill").tag(StateTab.today)
                Label(String(localized: "Planned"), systemImage: "calendar").tag(StateTab.planned)
                Label(String(localized: "Activity"), systemImage: "clock.arrow.circlepath")
                    .badge(model.conflicts.count)
                    .tag(StateTab.activity)
                Label(String(localized: "Settings"), systemImage: "gearshape").tag(StateTab.settings)
            }
            .navigationTitle("State")
            .navigationSplitViewColumnWidth(min: 180, ideal: 220)
        } content: {
            switch section ?? .today {
            case .today:
                ReminderCollectionView(model: model, mode: .today, selection: $selectedReminderID)
            case .planned:
                ReminderCollectionView(model: model, mode: .planned, selection: $selectedReminderID)
            case .activity:
                ActivityView(model: model)
            case .settings:
                SettingsView(model: model, opensNotificationSettings: $opensNotificationSettings)
            }
        } detail: {
            if let selectedReminderID, section == .today || section == .planned {
                NavigationStack {
                    ReminderDetailView(model: model, reminderID: selectedReminderID)
                }
                .id(selectedReminderID)
            } else {
                ContentUnavailableView(String(localized: "Select a reminder"), systemImage: "checklist")
            }
        }
        .onChange(of: section) { _, _ in selectedReminderID = nil }
        .onReceive(NotificationCenter.default.publisher(for: .stateOpenNotificationSettings)) { _ in
            section = .settings
            opensNotificationSettings = true
        }
    }
}
```

Anpassen an die echten Initializer aus der Pflichtlektüre (Parameternamen prüfen). Wenn `ActivityView` oder `SettingsView` intern einen eigenen `NavigationStack` haben, ist das in der Inhaltsspalte in Ordnung.

Neue String-Keys (`Select a reminder`) im String-Katalog mit deutscher Übersetzung "Wähle eine Erinnerung" ergänzen, gleiche Struktur wie vorhandene Einträge.

Commit: `git commit -m "feat: add a three column layout for ipad and mac"`

## Task 3: Automatische Auswahl des Layouts

**Datei:** `ios/State/Sources/UI/AdaptiveRootView.swift`

```swift
import SwiftUI

/// Chooses tabs on compact widths and the split layout everywhere else.
struct AdaptiveRootView: View {
    @Bindable var model: AppModel
    #if os(iOS)
    @Environment(\.horizontalSizeClass) private var horizontalSizeClass
    #endif

    var body: some View {
        #if os(macOS)
        SplitRootView(model: model)
        #else
        if horizontalSizeClass == .regular {
            SplitRootView(model: model)
        } else {
            MainTabView(model: model)
        }
        #endif
    }
}
```

In `StateRootView.swift` die Stelle, an der `MainTabView(model: model)` erzeugt wird, durch `AdaptiveRootView(model: model)` ersetzen. Nichts anderes dort ändern.

Achtung: Beim Drehen oder bei Split View auf dem iPad wechselt die Size-Class. Der Zustand (gewählter Tab) darf dabei verloren gehen, die App darf aber nicht abstürzen.

Commit: `git commit -m "feat: pick tabs or split layout by size class"`

## Task 4: Mac-Menübefehle

**Datei:** `ios/State/Sources/App/StateApp.swift`

Auf der `WindowGroup` unter `#if os(macOS)`:

```swift
.commands {
    CommandGroup(replacing: .newItem) {
        Button(String(localized: "New reminder")) {
            NotificationCenter.default.post(name: .stateCreateReminder, object: nil)
        }
        .keyboardShortcut("n")
    }
    CommandMenu(String(localized: "Sync")) {
        Button(String(localized: "Synchronize now")) {
            NotificationCenter.default.post(name: .stateSynchronizeNow, object: nil)
        }
        .keyboardShortcut("r")
    }
}
```

Die Notification-Namen `stateCreateReminder` und `stateSynchronizeNow` neben dem vorhandenen `stateOpenNotificationSettings` definieren (suche dessen Definition mit `grep -rn "stateOpenNotificationSettings" ios/State/Sources`). `SplitRootView` reagiert darauf: `stateCreateReminder` öffnet den Editor (gleiche Sheet-Logik wie in `ReminderCollectionView`, dafür ggf. einen Binding-Parameter durchreichen), `stateSynchronizeNow` ruft `await model.synchronize()` auf.

Commit: `git commit -m "feat: add new reminder and sync commands on the mac"`

## Task 5: Prüfung mit Screenshots

```bash
cd ios && xcodegen generate && cd ..
xcodebuild -project ios/State.xcodeproj -scheme State -destination 'platform=iOS Simulator,name=iPhone 16 Pro,OS=18.5' -derivedDataPath build/DerivedData-wp09 CODE_SIGNING_ALLOWED=NO test 2>&1 | tail -5
xcodebuild -project ios/State.xcodeproj -scheme State -destination 'platform=iOS Simulator,name=iPad (A16),OS=18.5' -derivedDataPath build/DerivedData-wp09 CODE_SIGNING_ALLOWED=NO build 2>&1 | tail -3
xcodebuild -project ios/State.xcodeproj -scheme StateMac -destination 'platform=macOS' -derivedDataPath build/DerivedData-wp09 CODE_SIGNING_ALLOWED=NO build 2>&1 | tail -3
```

Screenshots im Demo-Modus (falls die App einen Demo-Einstieg hat):
- iPad-Simulator: App installieren und starten (`xcrun simctl install`, `xcrun simctl launch`), `xcrun simctl io booted screenshot /tmp/wp09-ipad.png`.
- Mac: wie in WP06 Task 5, `screencapture -x /tmp/wp09-mac.png`.

Screenshots selbst ansehen und im Report beschreiben (Seitenleiste sichtbar? Detailspalte zeigt Platzhalter oder Reminder?). Nicht committen.

Report und Draft-PR nach Master-Plan Abschnitt 4.4 und 4.5.
