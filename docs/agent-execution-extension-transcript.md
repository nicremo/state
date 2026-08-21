# Product conversation transcript: scheduled agent execution and `.state/`

> **Editorial note:** This is a faithful, lightly edited transcription of the product idea that motivated the accompanying proposal. Personal references, concrete dates, and identifying project details were generalized for an open-source repository. Spoken repetitions and some colloquial phrasing are intentionally retained.

Schau dir bitte den Ordner beziehungsweise das Repository State an. Das ist eine Handy-App, die mit Agenten über MCP verbunden ist und praktisch ein verlängerter Arm für Erinnerungen, wichtige Termine und solche Dinge sein soll.

Konzipiere und brainstorme dazu. Es soll eine Erweiterung für die App geben: Aus der App heraus sollen Sessions auf einem Hauptarbeitsgerät gestartet werden können. In der Regel gibt es ein Hauptgerät, etwa ein Laptop, einen Desktop-Rechner oder einen Rechner zu Hause, mit dem meistens gearbeitet wird.

Wenn der State-MCP installiert und eingerichtet ist, soll aus der App heraus ein Agent gestartet werden können. Besonders bei Terminen: Wenn in einem Codex-, Claude-Code- oder anderen Harness-Kontext hinterlegt ist, dass zu einem bestimmten Zeitpunkt eine Mitgliedschaft gekündigt, ein API-Key erneuert oder rotiert oder eine andere technische Aufgabe ausgeführt werden muss, soll aus diesem Termin heraus automatisch eine Agent-Session auf dem Arbeitsrechner starten.

Die technische Umsetzung könnte über SSH, tmux oder eine andere lokale Ausführung geschehen. Die Session soll die Aufgabe zum gegebenen Zeitpunkt ausführen und den vollständigen Kontext erhalten. Beispiel: „Dieser geplante Job ist jetzt auszuführen.“

State enthält den Kontext einer Sache. Wenn innerhalb eines Projekts mehrere Aktualisierungen passieren, steht dieser Kontext in State. Der Harness-Agent soll ihn über MCP erhalten, die Aufgabe erledigen und den Job anschließend autonom über MCP in der State-App abhaken können.

Nach dem Abschluss soll eine Benachrichtigung auf dem Handy erscheinen, etwa: „Der Job ist erledigt. Hier ist die Aktualisierung.“ Das Ergebnis soll anschließend in State sichtbar sein.

Eine weitere Idee ist ein `.state`-Ordner in jedem Projektrepository oder Projektordner, in dem State eingerichtet ist. Dieser Ordner soll über MCP mit der App synchronisiert werden. Wenn ein Termin, eine Einladung oder eine feste Aufgabe gesetzt ist, könnte durch die Synchronisierung zwischen State, MCP und Projekt eine Datei oder ein Skript im `.state`-Ordner zur richtigen Zeit aktiviert werden. Dadurch wird die Agent-Session gestartet und erhält den zugehörigen Kontext.

Dieser Gedanke des `.state`-Ordners kann erweitert werden: Ähnlich wie bei `.git` oder Verzeichnissen anderer Harnesses und Systeme liegen dort sinnvolle Dateien, die Agenten und Menschen weiterhelfen. Der Ordner synchronisiert sich mit der App über MCP.

Eine typische Einrichtung kann einen eigenen PocketBase-Server oder eine vergleichbare selbst gehostete Infrastruktur verwenden. Die App könnte auf einem VPS, einem Container-Host oder einer ähnlichen Umgebung laufen.

Das Konzept soll offen und neutral bleiben. Es soll kein personenspezifisches Beispiel, keine privaten Zugangsdaten und keine besondere Infrastruktur voraussetzen.
