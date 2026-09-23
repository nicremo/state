# WP09 Report: iPad- und Mac-Layout mit `NavigationSplitView`

**Status:** DONE
**Branch:** wp/09-adaptive-layout
**Letzter Commit:** 43520ae test: prove the layout follows the available width

## Ergebnis in drei Sätzen

Das iPhone behält unverändert die `TabView` mit Heute, Geplant, Aktivität und Einstellungen; die Erinnerungsliste kann ihre Auswahl jetzt nach außen geben statt selbst zu navigieren. Auf dem iPad in regulärer Breite und auf dem Mac zeigt `SplitRootView` drei Spalten: Seitenleiste mit Konflikt-Badge, Inhaltsspalte mit der jeweiligen Liste, Detailspalte mit `ReminderDetailView` oder dem Platzhalter "Wähle eine Erinnerung". Auf dem Mac gibt es zusätzlich die Menübefehle "Neue Erinnerung" (⌘N) und "Synchronisieren" (⌘R).

## Erledigte Tasks

- [x] Task 1: `ReminderCollectionView` hat einen optionalen Parameter `selection: Binding<String?>?`. Ist er gesetzt, gibt es keinen eigenen `NavigationStack`, die Liste nutzt `List(selection:)`, und ein Tipp setzt die Auswahl. Der Listeninhalt liegt in einer gemeinsamen `list(selection:)`-Funktion, beide Varianten nutzen sie.
- [x] Task 2: `SplitRootView.swift` mit drei Spalten, Platzhalter und Konflikt-Badge. Neue String-Keys `Select a reminder` ("Wähle eine Erinnerung") und `Sync` ("Synchronisieren").
- [x] Task 3: `AdaptiveRootView.swift` wählt nach `horizontalSizeClass`: kompakt die Tabs, regulär die Split-Ansicht, auf dem Mac immer die Split-Ansicht. `StateRootView` erzeugt nur noch `AdaptiveRootView`.
- [x] Task 4: Menübefehle auf der `WindowGroup` unter `#if os(macOS)`, Notification-Namen `stateCreateReminder` und `stateSynchronizeNow`, `SplitRootView` öffnet darauf den Editor beziehungsweise ruft `await model.synchronize()`.
- [x] Task 5: Gebaut und getestet auf iPhone, iPad und Mac, Screenshots angesehen, siehe unten.
- [x] Report und Draft-PR.

## Geänderte Dateien

| Datei | Änderung |
| --- | --- |
| `ios/State/Sources/UI/MainTabView.swift` | `selection` und `editorPresentation` als optionale Parameter, gemeinsame `list(selection:)`, Zeilen als `NavigationLink` oder als auswählbarer Knopf, `revealCreatedReminder` setzt je nach Variante Auswahl oder Pfad |
| `ios/State/Sources/UI/SplitRootView.swift` | Neu. Drei Spalten, Auswahlzustand, Reaktion auf die drei Notification-Namen |
| `ios/State/Sources/UI/AdaptiveRootView.swift` | Neu. Layout nach Size-Class |
| `ios/State/Sources/App/StateRootView.swift` | Nur die eine Stelle: `MainTabView(model:)` wird zu `AdaptiveRootView(model:)` |
| `ios/State/Sources/App/StateApp.swift` | `.commands` unter `#if os(macOS)` |
| `ios/State/Sources/Notifications/StateNotificationNames.swift` | Die zwei neuen Notification-Namen |
| `ios/State/Resources/Localizable.xcstrings` | `Select a reminder` und `Sync` mit deutscher Übersetzung |
| `ios/StateUITests/AdaptiveLayoutTests.swift` | Neu. Prüft auf jeder Destination, welches Layout erscheint, und hält die Screenshots als Testanhänge fest |

## Dateien außerhalb meines Bereichs

- `ios/State/Sources/Notifications/StateNotificationNames.swift`: Diese Datei ist in WP06 entstanden. WP09 braucht dort die zwei neuen Notification-Namen, weil der Plan verlangt, sie neben `stateOpenNotificationSettings` zu definieren. Die Änderung sind zwei Zeilen.

## Prüfungen

| Befehl | Ergebnis |
| --- | --- |
| `cd ios && xcodegen generate` | grün, danach nur die erwartete `project.pbxproj` |
| `xcodebuild -scheme State -destination 'platform=iOS Simulator,name=iPhone 16 Pro,OS=18.5' test` | grün, 55 Unit-Tests und 2 UI-Tests, 0 Fehler |
| `xcodebuild -scheme State -destination 'platform=iOS Simulator,name=iPad Pro 13-inch (M4),OS=18.5' -only-testing:StateUITests/AdaptiveLayoutTests test` | grün |
| `xcodebuild -scheme State -destination 'platform=iOS Simulator,name=iPad Air 13-inch (M3),OS=18.5' -only-testing:StateUITests/AdaptiveLayoutTests test` | grün |
| `xcodebuild -scheme State -destination 'platform=iOS Simulator,name=iPad (A16),OS=18.5' build` | `** BUILD SUCCEEDED **` |
| `xcodebuild -scheme StateMac -destination 'platform=macOS' build` | `** BUILD SUCCEEDED **` |
| Menüleiste der Mac-App über `System Events` ausgelesen | `Neue Erinnerung` im Menü `Ablage`, eigenes Menü `Synchronisieren` |

### Was der iPad-Test prüft

`AdaptiveLayoutTests` läuft auf jeder Destination mit demselben Schema und verzweigt nach dem, was die App wirklich zeigt. Auf dem iPhone erwartet er vier Tabs und keine Seitenleiste, auf dem iPad die vier Seitenleisten-Einträge, keinen Tab-Balken, den Platzhalter in der Detailspalte und danach den echten Detailinhalt, nachdem eine Erinnerung ausgewählt wurde. Alle Abfragen laufen über Accessibility-Identifier, damit ein deutsch eingestellter Simulator den Test nicht bricht.

### Screenshots

Alle Dateien liegen unter `/tmp`, sind **nicht** committet und wurden angesehen.

**iPad**, `/tmp/wp09-ipad.png` und `/tmp/wp09-ipad-detail.png`, aus den Testanhängen des `AdaptiveLayoutTests`:

- Zu sehen sind links die Seitenleiste mit der Wortmarke `State` und den vier Einträgen `Heute` (ausgewählt), `Geplant`, `Aktivität`, `Einstellungen`. Daneben die Inhaltsspalte mit dem Titel `Heute`, dem Suchfeld `Erinnerungen durchsuchen` und der Zeile `Agenten-Workflow prüfen` samt Datum, Herkunft und Revision.
- Im zweiten Bild ist die Zeile ausgewählt und die Detailspalte zeigt den Reminder statt des Platzhalters.
- **Auffälligkeit:** Im Simulator läuft die App in einem Fenster, das nur etwa 57 Prozent der Bildschirmbreite einnimmt. Der Rest des Bildschirms bleibt grau. Dadurch ist die Detailspalte am rechten Rand abgeschnitten, und vom Platzhaltertext ist nur das Wortende zu sehen. Der Test findet die Detailspalte trotzdem vollständig im Accessibility-Baum, und auf dem Mac ist sie ganz sichtbar. Dieselbe Beobachtung zeigt sich auf drei verschiedenen iPad-Modellen, also unabhängig vom Gerät. Sie hat nichts mit dieser Änderung zu tun, weil WP09 keine Fenstergröße setzt. Siehe Offene Fragen.

**Mac**, `/tmp/wp09-mac.png`:

- Zu sehen ist das native Mac-Fenster mit drei Spalten. Links die Seitenleiste mit `Heute`, `Geplant`, `Aktivität`, `Einstellungen`, wobei `Heute` ausgewählt ist. In der Mitte die Inhaltsspalte mit dem Titel `Heute`, dem Plus-Knopf, dem Suchfeld und der ausgewählten Zeile `Agenten-Workflow prüfen`. Rechts die Detailspalte mit Beschreibung, Datum `23. Sept. 2026, 21:00`, `Vorkommnisse`, `Agent-Läufe` mit `nightly-maintenance` und den Zuständen `Wartet auf Freigabe` und `Erfolgreich`, `Kommentare` und `Vollständiger Verlauf`. In der Werkzeugleiste der Detailspalte sitzen Bearbeiten und Archivieren.
- Vor der Auswahl zeigte dieselbe Stelle den Platzhalter mit dem Häkchen-Symbol und dem Text `Wähle eine Erinnerung`, die deutsche Übersetzung des neuen Keys.

## Abweichungen vom Plan

1. **Startpunkt des Branches.** Der Plan verlangt, WP09 erst zu starten, wenn WP06 auf `main` ist. WP06 war zu Beginn dieser Sitzung noch nicht einmal umgesetzt, deshalb habe ich zuerst WP06 fertiggestellt und als PR #49 eingereicht. WP09 ist auf dem WP06-Branch gestartet und nach dem Merge von WP06 (`c046d8a`) auf das aktuelle `main` rebased.
2. **Die Zeilen der Auswahlvariante sind ein Knopf.** Der Plan nennt `List(selection:)` und `.tag(reminder.id)`. Das reicht auf dem Mac nicht: Ein Klick auf eine Zeile setzte die Auswahl nicht, geprüft mit echtem Mausklick und mit `AXPress` auf die Zeile. Die Zeile ist deshalb zusätzlich ein `Button` mit `.buttonStyle(.plain)`, der dieselbe Auswahl setzt. Der `.tag` bleibt erhalten. Auf dem iPhone ist der Pfad unverändert der `NavigationLink`.
3. **Accessibility-Identifier.** `SplitRootView` vergibt `split-detail-placeholder`, `split-detail` und `sidebar-today`, `sidebar-planned`, `sidebar-activity`, `sidebar-settings`. Ohne sie müsste der Test lokalisierte Texte vergleichen, was auf einem deutschen Simulator bricht.
4. **Der Editor-Sheet wird durchgereicht.** Der Plan erlaubt das ausdrücklich ("dafür ggf. einen Binding-Parameter durchreichen"). `ReminderCollectionView` bekommt `editorPresentation: Binding<Bool>?`, damit ⌘N und der Plus-Knopf dasselbe Sheet öffnen. Ohne externes Binding bleibt der interne `@State` und damit das iPhone-Verhalten identisch.
5. **`stateCreateReminder` wechselt vorher auf Heute.** Der Editor lebt in der Erinnerungsliste. Steht die Seitenleiste auf Aktivität oder Einstellungen, wechselt `SplitRootView` erst auf Heute und öffnet dann das Sheet.
6. **Neue UI-Testdatei.** `ios/StateUITests/**` gehört zu diesem WP, deshalb liegt der Layoutbeweis dort und nicht nur in einem Screenshot.

## Offene Fragen und Risiken

1. **Die App läuft im iPad-Simulator in einem schmalen Fenster.** Auf drei iPad-Modellen (A16, Pro 13 Zoll M4, Air 13 Zoll M3) nimmt das App-Fenster nur etwa 57 Prozent der Bildschirmbreite ein, der Rest bleibt grau, und die Detailspalte wird am rechten Rand abgeschnitten. Die Einstellungen-App im selben Simulator füllt den Bildschirm, es liegt also nicht am Simulator. Da WP09 keine Fenstergröße setzt und das Verhalten auch die Tab-Ansicht betrifft, ist es kein Befund dieses WP, aber es sollte auf einem echten iPad geprüft werden, bevor die iPad-Abnahme läuft.
2. **Auswahl auf dem Mac nur mit Knopf.** Siehe Abweichung 2. Wer später auf die reine Tag-Variante zurückbaut, bricht die Mac-Auswahl wieder.
3. **Mehrere Sessions arbeiten gleichzeitig im selben Repository.** Während dieser Sitzung sind wiederholt Branches und `main` unter mir weitergelaufen (WP07 als PR #46, WP10 als #44, WP06 als #49), und im Worktree `wp09-adaptive-layout` lagen am Ende uncommittete Änderungen einer anderen Session (`Platform.deviceNoun` in `Platform.swift`, `ConnectView.swift`, `OnboardingFlowView.swift` und String-Keys). Ich habe diese Dateien nicht angefasst und nicht committet. Der Branch wurde deshalb in einem getrennten Worktree rebased und gepusht, damit die fremde Arbeit unberührt bleibt.

## Manuelle Schritte für Fabian oder den Koordinator

1. Diesen Branch nach `main` mergen. Er ist auf `origin/main` (`c046d8a`, WP06) rebased.
2. Die iPad-Fensterbreite auf einem echten iPad ansehen. Falls die drei Spalten dort zu eng sind, ist die naheliegende Anpassung, die Detailspalte auf schmalen regulären Breiten erst nach einer Auswahl einzublenden.
3. Die uncommitteten Änderungen der anderen Session im Worktree `wp09-adaptive-layout` prüfen und selbst committen oder verwerfen.
