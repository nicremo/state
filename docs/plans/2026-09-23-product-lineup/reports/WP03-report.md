# WP03 Report: VPS Deploy-Kit (Compose + Traefik + APNs + Backups)

**Status:** DONE
**Branch:** wp/03-vps-deploy-kit
**Letzter Commit:** 2d8104d docs: note the image tag used by the backup verification

## Ergebnis in drei Sätzen

Das Deploy-Kit steht vollständig: ein Traefik-Overlay mit TLS-Labels für `state.<domain>` und `relay.<domain>`, eine Umgebungsvorlage ohne echte Werte, ein Betriebsskript mit allen neun Unterbefehlen und eine Anleitung in acht Abschnitten. `docker compose config` läuft mit der Vorlage durch, erzeugt genau die zwei erwarteten `Host(...)`-Regeln und bricht bei fehlenden APNs-Werten mit klarer Meldung ab, während `deploy/compose.yaml` allein weiter im Dry-Run bleibt. Report und Branch sind bereit für Review und Draft-PR; ein Server wurde nicht angefasst, es gibt keinen echten Schlüssel und kein Deployment.

## Erledigte Tasks

- [x] Task 1: Traefik-Override, `.env.example`, Variablen in `compose.yaml`, `deploy/secrets/.gitignore`, Validierung mit `docker compose config`
- [x] Task 2: `scripts/vps/state.sh` mit den Unterbefehlen `up`, `down`, `logs`, `ps`, `bootstrap-token`, `verify-audit`, `backup`, `update`, `smoke`, dazu `scripts/vps/README.md`
- [x] Task 3: `docs/deploy-vps.md` mit den Abschnitten Prerequisites, Traefik-Namen, Install, Pairing, Push-Checkliste, Backup und Restore, Updates, Troubleshooting, plus Verweis aus `docs/operations.md`
- [x] Task 4: Gesamtprüfung (`bash -n`, `git status`, Secret-Grep) grün

## Geänderte Dateien

| Datei | Änderung |
| --- | --- |
| `deploy/compose.yaml` | `image`-Tags und `STATE_VERSION`-Build-Args auf `${STATE_VERSION:-0.1.0}`, Relay-Umgebung auf Variablen mit Dry-Run-Default, Netzname auf `${PROXY_NETWORK:-proxy-network}` |
| `deploy/compose.traefik.yaml` | Neu: Traefik-Labels für beide Dienste, TLS über den Cert-Resolver des Hosts, APNs-Secret als Datei |
| `deploy/.env.example` | Neu: Vorlage mit Platzhaltern, keine echten Werte |
| `deploy/.gitignore` | Neu: hält `deploy/.env` aus Git (im WP nicht genannt, siehe Abweichungen) |
| `deploy/secrets/.gitignore` | Neu: `*` plus `!.gitignore`, schützt `.p8` und Empfängerdatei |
| `scripts/vps/state.sh` | Neu: einziger Einstiegspunkt, 9 Unterbefehle, Repo-Root über `dirname $0/../..`, ausführbar (100755) |
| `scripts/vps/README.md` | Neu: kurze Befehlsreferenz, Englisch |
| `docs/deploy-vps.md` | Neu: Deployment-Anleitung, Englisch, 8 Abschnitte plus Dateiübersicht |
| `docs/operations.md` | Ein Satz Verweis auf `docs/deploy-vps.md` am Anfang |
| `.gitignore` | `backups/` ergänzt |

## Dateien außerhalb meines Bereichs

- `docs/operations.md`: ein Verweissatz am Anfang, im WP ausdrücklich gefordert.
- `.gitignore` (Repo-Root): `backups/` ergänzt, damit `scripts/vps/state.sh backup` keine unversionierten Dateien hinterlässt. Im WP als Ausnahme benannt.
- `deploy/.gitignore` liegt innerhalb von `deploy/**`, war aber im WP nicht aufgeführt. Grund: `deploy/.env` enthält echte APNs-IDs und Domain, wurde bisher von keiner Ignore-Regel erfasst und wäre beim ersten `git add -A` auf dem VPS im Commit gelandet.

## Prüfungen

| Befehl | Ergebnis |
| --- | --- |
| `docker compose version` | `v2.40.2-desktop.1`, Docker vorhanden |
| `docker compose --env-file /tmp/wp03.env -f deploy/compose.yaml -f deploy/compose.traefik.yaml config` | `CONFIG_OK`, zwei Host-Regeln mit `state.example.com` und `relay.example.com` |
| Gegenprobe mit leerer `STATE_RELAY_APNS_TEAM_ID` | Exit 1, `required variable STATE_RELAY_APNS_TEAM_ID is missing a value: set STATE_RELAY_APNS_TEAM_ID` |
| `docker compose -f deploy/compose.yaml config` ohne Overlay | `STATE_RELAY_DRY_RUN_APNS: "true"`, `STATE_RELAY_ALLOW_DEVELOPMENT_ATTEST: "true"`, also weiter sicher |
| Aufruf aus fremdem Arbeitsverzeichnis mit absoluten Pfaden | Projekt `state`, Build-Kontext korrekt das Repo-Root, Secret-Pfad unter `deploy/secrets/` |
| `python3 -c "import yaml; yaml.safe_load(...)"` | `YAML_OK` für beide Compose-Dateien |
| `bash -n scripts/vps/state.sh` | `SYNTAX_OK` |
| `shellcheck scripts/vps/state.sh` (0.11.0, via Docker-Image `koalaman/shellcheck:stable`) | keine Findings, Exit 0 |
| `scripts/vps/state.sh` ohne Argument | Usage-Text, Exit 2 |
| `scripts/vps/state.sh frobnicate` | Usage plus `error: unknown command 'frobnicate'`, Exit 1 |
| `scripts/vps/state.sh smoke` ohne `deploy/.env` | `error: deploy/.env missing, copy deploy/.env.example and fill it in`, Exit 1 |
| Verhaltenslauf mit Fake-Binaries für `docker`, `git`, `curl`, `age`, `sha256sum` | `up`, `down`, `ps`, `logs`, `bootstrap-token`, `verify-audit` erzeugen exakt die geplanten Aufrufe, `smoke` prüft `https://state.<domain>/health/ready`, `https://state.<domain>/version`, `https://relay.<domain>/health/ready` und gibt `SMOKE_OK`, `update` macht erst `git pull --ff-only`, dann `up`, dann `smoke` |
| Backup-Pfad ohne Empfänger | Exit 1 mit Hinweis auf `STATE_AGE_RECIPIENT` und `deploy/secrets/backup-recipient.txt` |
| Backup-Pfad mit `STATE_AGE_RECIPIENT` | Ruft `ops/backup-state.sh` auf, schreibt `backups/state-<Zeitstempel>.tar.gz.age` plus `.sha256`, temporäre Empfängerdatei wird gelöscht |
| `git grep -nE "BEGIN (EC )?PRIVATE KEY\|STATE_RELAY_APNS_KEY_ID=[A-Z0-9]{10}" -- deploy docs scripts` | `NO_SECRETS` |
| Zusätzlich: Suche nach `TEAMID1234`, `KEYID12345`, `age1fake` im Repo | keine Treffer |
| `git status --short` | leer, keine `.env`, keine `.p8`, keine Backup-Datei, keine untracked Plandatei |
| `git ls-files -s scripts/vps/state.sh` | `100755`, ausführbar im Index |
| `ls -l deploy/secrets/` | nur `.gitignore`, die Testdatei `apns_key.p8` wurde wieder entfernt |

## Abweichungen vom Plan

1. **Backup folgt dem dokumentierten Verfahren.** Das WP nennt zuerst `docs/operations.md` und nur für den Fall, dass dort nichts steht, den Stop-tar-Start-Fallback. `docs/operations.md` beschreibt `ops/backup-state.sh` mit age-Verschlüsselung, also ruft `scripts/vps/state.sh backup` dieses Skript auf. Der unverschlüsselte Volume-Fallback ist als zweiter Zweig implementiert und wird nur benutzt, wenn `ops/backup-state.sh` fehlt, dann mit ausdrücklicher Warnung. Zielverzeichnis ist in beiden Fällen `backups/` im Repo-Root.
2. **`docs/operations.md` und `ops/backup-state.sh` passen nicht zusammen.** Die Doku nennt `STATE_AGE_RECIPIENT='age1...' ./ops/backup-state.sh`, das Skript liest aber ausschließlich `STATE_AGE_RECIPIENT_FILE`. Mein Wrapper übersetzt beide Wege: vorhandene Datei, sonst `deploy/secrets/backup-recipient.txt`, sonst `STATE_AGE_RECIPIENT` über eine temporäre Datei mit `chmod 0600`, die danach gelöscht wird. Beide Varianten sind getestet.
3. **`deploy/.gitignore` ergänzt** (siehe Dateien außerhalb meines Bereichs).
4. **Basis ist `origin/main` nach Protokoll 4.1.** Die Plandateien lagen dort noch nicht, deshalb habe ich `README.md` und die WP-Dokumente zum Lesen aus `~/Desktop/state` in den Worktree kopiert und vor dem Abschluss wieder entfernt. Die ersten Commits entstanden auf `bc42fbe`, dem HEAD des Integrations-Branches und der Basis der Worktrees von WP01 und WP02, danach habe ich die fünf WP-Commits mit `git rebase --onto origin/main bc42fbe` auf `origin/main` gesetzt. Der Draft-PR enthält dadurch genau die elf Dateien dieses WP und keine fremden iOS- oder macOS-Änderungen. `deploy/**`, `docs/operations.md` und `.gitignore` sind zwischen beiden Basen identisch, der Dateibaum des WP hat sich beim Rebase nicht geändert.
5. **Zusätzlicher Commit** `docs: note the image tag used by the backup verification`, weil `ops/verify-backup.sh` und `ops/restore-state.sh` mit `STATE_SERVER_IMAGE` und `STATE_RELAY_IMAGE` arbeiten und deren Default `0.1.0` zum lokal gebauten Image passen muss.
6. **Container-Name im Troubleshooting** ist `state-state-server-1`, das wurde mit einem Wegwerf-Projekt gegen Docker Desktop verifiziert.
7. **Kein `config`-Unterbefehl im Betriebsskript.** Das WP nennt ihn nicht, und für die Fehlersuche ist `docker inspect <container> --format '{{json .Config.Labels}}'` direkter, deshalb steht das in der Anleitung.

## Offene Fragen und Risiken

1. **APNs-Fehler sind auf dem VPS nicht sichtbar.** `internal/relay/handler.go` gibt bei einem Fehler des Dispatchers nur `{"code":"internal_error"}` zurück und schreibt nichts ins Log. Der Server loggt zwar `push scheduler cycle failed`, aber ohne den APNs-Grund (`BadDeviceToken`, `DeviceTokenNotForTopic`, `ExpiredProviderToken`). Damit ist die Push-Fehlersuche aus Abschnitt 5 der Anleitung auf Ausschlussverfahren beschränkt. Der Fix gehört in `internal/relay`, also nicht in meinen Bereich, sondern in ein eigenes WP oder in eine Koordinator-Korrektur.
2. **Traefik-Namen sind Annahmen.** `TRAEFIK_ENTRYPOINT=websecure`, `TRAEFIK_CERTRESOLVER=letsencrypt` und `PROXY_NETWORK=proxy-network` sind Defaults. Auf dem Ziel-VPS müssen sie nach Abschnitt 2 der Anleitung geprüft werden.
3. **`.p8`-Rechte konnte ich nur begründen, nicht reproduzieren.** Auf macOS bildet Docker Desktop die Eigentümerschaft von Bind-Mounts auf den Container-Nutzer ab, deshalb lief der Test mit `0440` und mit `0600` durch. Die Aussage in der Anleitung stützt sich auf das dokumentierte Verhalten von Compose-Datei-Secrets, die als Bind-Mount der Quelldatei landen. Auf dem VPS mit `ls -l` prüfen, ein Relay mit `read APNs private key: permission denied` bestätigt die Regel.
4. **Backup-Kette ist nicht gegen echte Volumes getestet.** Der Backup-Zweig lief mit Fake-Binaries für `docker`, `age` und `sha256sum`, also nur die Aufrufkette und die Dateipfade sind belegt. Ein echter Lauf braucht den VPS, das war laut WP nicht Teil des Auftrags.
5. **`STATE_VERSION` muss zu den Image-Namen der Hilfsskripte passen.** Solange GitHub Actions aus sind, gibt es die `ghcr.io/nicremo/state-*`-Images nicht im Registry, lokal gebaut heißen sie aber genau so. Nach einem Wechsel von `STATE_VERSION` müssen `STATE_SERVER_IMAGE` und `STATE_RELAY_IMAGE` mitgegeben werden, das steht jetzt in der Anleitung.
6. **Dry-Run braucht trotzdem APNs-IDs.** Die Compose-Guards greifen vor der Relay-Logik, deshalb verlangt auch ein reiner Testlauf nicht leere `STATE_RELAY_APNS_TEAM_ID` und `STATE_RELAY_APNS_KEY_ID`. In der Anleitung steht der Hinweis.

## Manuelle Schritte für Fabian oder den Koordinator

Nach dem Merge des WP auf `main`, auf dem VPS als root oder als Nutzer mit Docker-Rechten:

```bash
# 1. Traefik-Namen ermitteln (Entrypoint, Cert-Resolver, Docker-Netz)
docker network ls
docker ps --format '{{.Names}}' | grep -i traefik
docker inspect <traefik-container> --format '{{json .Config.Cmd}}'
docker inspect <traefik-container> --format '{{json .Args}}'

# 2. Ausrollen
sudo git clone https://github.com/nicremo/state.git /opt/state
cd /opt/state
cp deploy/.env.example deploy/.env
vi deploy/.env          # STATE_DOMAIN, TRAEFIK_ENTRYPOINT, TRAEFIK_CERTRESOLVER, PROXY_NETWORK, STATE_RELAY_APNS_TEAM_ID, STATE_RELAY_APNS_KEY_ID

# 3. APNs-Key auf den VPS bringen und für Nutzer 10001 lesbar machen
mkdir -p deploy/secrets
scp ~/Downloads/AuthKey_XXXXXXXXXX.p8 root@<vps-ip>:/tmp/apns_key.p8
install -o root -g 10001 -m 0440 /tmp/apns_key.p8 /opt/state/deploy/secrets/apns_key.p8
rm -f /tmp/apns_key.p8
ls -l /opt/state/deploy/secrets/apns_key.p8     # -r--r----- root 10001

# 4. Starten und prüfen
scripts/vps/state.sh up
scripts/vps/state.sh ps
scripts/vps/state.sh smoke                      # muss SMOKE_OK ausgeben

# 5. Owner pairen: Token wie ein Passwort behandeln, nicht in die History
scripts/vps/state.sh bootstrap-token
# In der iPhone-App "Server verbinden" mit https://state.<domain> und diesem Token

# 6. Echte Pushes aktivieren und testen
#    In deploy/.env: STATE_RELAY_DRY_RUN_APNS=false, STATE_RELAY_ALLOW_DEVELOPMENT_ATTEST=false
scripts/vps/state.sh up
scripts/vps/state.sh logs state-relay           # darf nicht "APNs dry run mode is enabled" zeigen
# Reminder in 2 Minuten anlegen, App schließen, Push muss ankommen

# 7. Backup einrichten
age-keygen -o ~/state-backup-identity.txt       # auf dem Mac, Identität bleibt dort
printf '%s\n' 'age1...' | tee /opt/state/deploy/secrets/backup-recipient.txt >/dev/null
chmod 0444 /opt/state/deploy/secrets/backup-recipient.txt
scripts/vps/state.sh backup
ls -l backups/
```

Offen für den Koordinator: Entscheidung zu Risiko 1 (APNs-Fehler sichtbar machen) und die Prüfung der Traefik-Namen auf dem echten Host.
