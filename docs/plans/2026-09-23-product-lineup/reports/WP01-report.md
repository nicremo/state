# WP01 Report: Architektur-Dokument `docs/product-lineup.md`

**Status:** DONE
**Branch:** wp/01-product-lineup-doc
**Letzter Commit:** a81ce49 docs: keep the Mac Server app a pure server in the lineup

## Ergebnis in drei Sätzen

`docs/product-lineup.md` beschreibt die vier Produkte, ihre Verbindungen, Ports, Push-Wege, den Ort des Runners und die geltenden Grenzen in den zehn geforderten Abschnitten auf Englisch, mit dem Mermaid-Diagramm aus der Vorlage. `README.md` verweist aus dem Abschnitt `## Components` auf das neue Dokument, sonst wurde dort nichts geändert. Alle fünf Punkte der Selbstprüfung aus Task 3 sind grün, dieser Report und der Draft-PR schließen das Paket ab.

## Erledigte Tasks

- [x] Task 1: `docs/product-lineup.md` neu angelegt, zehn Pflichtabschnitte in der vorgegebenen Reihenfolge, Mermaid-Diagramm zeichengleich aus der Vorlage. Commits `39dd330`, `fd90ebe` und `a81ce49`.
- [x] Task 2: `README.md`, Abschnitt `## Components`, genau ein zusätzlicher Absatz mit dem vorgegebenen Satz, sonst nichts geändert. Commit `816fbb5`.
- [x] Task 3: Selbstprüfung, siehe Prüfungen.

## Geänderte Dateien

| Datei | Änderung |
| --- | --- |
| `docs/product-lineup.md` | Neu, 142 Zeilen. Zehn Abschnitte, Mermaid-Diagramm in Abschnitt 1, Tabellen in den Abschnitten 1, 4, 7 und 10. |
| `README.md` | Eine Zeile im Abschnitt `## Components`, keine weitere Änderung. |
| `docs/plans/2026-09-23-product-lineup/reports/WP01-report.md` | Dieser Report. |

## Dateien außerhalb meines Bereichs

(keine)

## Prüfungen

| Befehl | Ergebnis |
| --- | --- |
| Link-Check aus WP01 Task 2 (python3, beide Dokumente) | `link check done`, keine `BROKEN`-Zeile |
| `grep -n "^#" docs/product-lineup.md` | Titel plus zehn Pflichtabschnitte in der geforderten Reihenfolge, keine weiteren Überschriften |
| Mermaid-Vergleich gegen die Vorlage (Python, Zeichenvergleich) | identisch, 23 Zeilen, auch mit dem am 23.09. geänderten Knoten `state-runner<br/>user LaunchAgent` |
| Portabgleich 8090 und 8091 | stimmt mit `deploy/compose.yaml`, `cmd/state-server/main.go:78` und `cmd/state-relay/main.go:70` |
| Portabgleich 9847 und 9848 | stimmt mit `macos/README.md` und `cmd/state-server/desktop.go:66,67` |
| Datenschutzprüfung (`docs/product-lineup.md` und `README.md`) | keine Treffer für Team-ID, E-Mail, echte Domains, Tokens oder Registry-Pfade, nur die öffentliche Bundle-ID `com.fabincrm.state` |
| Gedankenstrichprüfung | 0 Treffer für Geviertstrich und Halbgeviertstrich |
| `git status` | sauber |
| `gofmt -l ./cmd ./internal`, `go vet ./...`, `go test -race ./...` | nicht anwendbar, keine Go-Änderung |
| `xcodegen generate`, `xcodebuild test` | nicht anwendbar, keine Swift-Änderung |

## Abweichungen vom Plan

1. **Basis-Branch.** Das Protokoll in Abschnitt 4.1 verlangt einen Worktree auf `origin/main`. Auf `origin/main` fehlten die Pflichtquellen `macos/README.md` und `docs/LOCAL_MAC_CONNECTION.md`, sie lagen nur auf dem Integrations-Branch. Der WP-Branch wurde deshalb von `bc42fbe` abgezweigt und nach dem Plan-Commit des Koordinators (`e74c673 docs: plan the product lineup as parallel work packages`) per Rebase darauf neu aufgebaut. Dadurch ist das Plan-Set im Branch enthalten und der Link-Check grün. Während der Arbeit hat der Koordinator Phase 1 abgeschlossen: `origin/main` enthält seit `4254d1b feat: integrate local Mac server, iPhone refresh and product lineup plan (#38)` dieselben Dateien. Der WP-Branch ist noch nicht auf diesen Stand gezogen.
2. **Plan-Set war beim Start nicht im Repository.** `docs/plans/` lag zunächst nur untracked im Haupt-Worktree, deshalb war der Link-Check zu Beginn rot. Mit `e74c673` löst er sich ohne Änderung am Dokument auf. Es wurde nichts aus dem Plan-Set in diesen Branch kopiert oder verändert.
3. **Die Runner-Entscheidung änderte sich während der Arbeit.** Abschnitt 2 Punkt 3 des Master-Plans wurde vom Koordinator umgestellt: Der Runner läuft als eigener LaunchAgent (`state-runner service install`) und nicht in der Mac-App, weil die App-Sandbox der App-Store-App keine Agenten-CLIs starten darf. Abschnitt 6, der Diagrammknoten und der WP10-Titel wurden nachgezogen (Commit `fd90ebe`). Mit Commit `a81ce49` steht zusätzlich ausdrücklich im Dokument, dass die Mac-Server-App ein reiner Server bleibt.
4. **Reverse Proxy.** Der Master-Plan nennt Traefik, `README.md` und `docs/operations.md` nennen Nginx Proxy Manager. Das Dokument bleibt neutral bei "TLS reverse proxy" und beschreibt nur, was `deploy/compose.yaml` tatsächlich festlegt.
5. **Drei Wege, vierte Aussage.** Abschnitt 5 beschreibt die drei Wege aus der Vorgabe und stellt die Mac-App danach in einem eigenen Absatz dar, statt sie als vierten Weg zu zählen.
6. **APNs-Status.** Abschnitt 5 qualifiziert App Attest als Produktionsanforderung und nennt den heutigen Zustand des Stacks (Entwicklungs-Attest erlaubt, APNs im Dry-Run). Der Master-Plan formuliert hier absolut, `deploy/compose.yaml` und `docs/operations.md` zeigen den Zwischenstand.
7. **README-Diagramm.** Das Mermaid-Diagramm im Abschnitt `## Components` zeigt weiterhin den heutigen Stand ohne Mac-App und Mac Server. Task 2 erlaubt nur den zusätzlichen Absatz, deshalb blieb es unverändert. Es widerspricht dem neuen Dokument nicht, zeigt aber nicht das Zielbild.

## Offene Fragen und Risiken

1. **Der Master-Plan widerspricht sich beim Runner.** Abschnitt 2 Punkt 3 sagt jetzt, der Runner ist ein eigener LaunchAgent außerhalb der App. Abschnitt 6 Punkt 6 sagt weiterhin, ein fälliger Reminder startet "über den Runner der Mac-App" eine Agent-Session. Das Dokument folgt Abschnitt 2. Abschnitt 6 des Master-Plans sollte nachgezogen werden.
2. **Die PR-Ansicht ist noch verrauscht.** `main` enthält den Integrationsstand inzwischen als `4254d1b` (#38), der WP-Branch basiert aber auf dem inhaltlich gleichen Stand `e74c673`. Deshalb zeigt GitHub im Draft-PR 61 Dateien, obwohl inhaltlich nur drei Dateien neu sind (`git diff --stat origin/main HEAD` ergibt drei Dateien, 210 Zeilen). Nach `git merge origin/main` im WP-Worktree zeigt der PR genau diese drei Dateien. Der Merge ist konfliktfrei, weil beide Seiten gegenüber der Merge-Basis dieselben Änderungen an `README.md` und am Plan-Set enthalten.
3. **Abschnitt 10 verlinkt elf Plan-Dateien.** Wird eine Plan-Datei umbenannt, muss der Link im Dokument mitgezogen werden. Die Namen stammen aus dem Plan-Set vom 23.09.2026.
4. **WP07** ist die Voraussetzung für die gestrichelte Relay-Verbindung im Diagramm.
5. **E-Mail-Adresse im Plan-Set.** `docs/plans/2026-09-23-product-lineup/README.md` nennt `fb200386@gmail.com` als Pflichtwert für `git config user.email`. Die Adresse steht bereits in öffentlichen Commits, ist also nicht neu. Mit dem Plan-Commit `e74c673` liegt sie aber erstmals als Dateiinhalt im Repository und damit auch in diesem Branch.

## Manuelle Schritte für Fabian oder den Koordinator

1. Diesen Draft-PR prüfen. Vor dem Merge im WP-Worktree `git merge origin/main` ausführen, damit der PR nur die drei geänderten Dateien zeigt. Der Merge ist konfliktfrei.
2. WP01 steht an erster Stelle der Merge-Reihenfolge für Welle 1.
3. Optional: Abschnitt 6 Punkt 6 des Master-Plans an die neue Runner-Entscheidung anpassen.
