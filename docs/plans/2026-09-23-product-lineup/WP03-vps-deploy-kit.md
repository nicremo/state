# WP03: VPS Deploy-Kit (Compose + Traefik + APNs + Backups)

**Welle:** 1 · **Slug:** `vps-deploy-kit` · **Branch:** `wp/03-vps-deploy-kit`
**Besitzt:** `deploy/**`, neu `docs/deploy-vps.md`, neu `scripts/vps/**`
**Geschätzter Umfang:** mittel (YAML, Shell, Doku). Kein echter Server, kein echtes Deployment.

## Goal-Prompt (so an die Worker-Session geben)

> Lies `docs/plans/2026-09-23-product-lineup/README.md` vollständig und halte dich an das Arbeitsprotokoll in Abschnitt 4. Setze danach `docs/plans/2026-09-23-product-lineup/WP03-vps-deploy-kit.md` Task für Task um. Du verbindest dich mit keinem Server, deployest nichts und legst keine echten Schlüssel an. Schließe mit Report und Draft-PR ab.

## Ausgangslage

- `deploy/compose.yaml` definiert `state-server` (Port 8090) und `state-relay` (Port 8091). Beide hängen im externen Netz `proxy-network`, aber es gibt **keine Reverse-Proxy-Labels**, kein TLS und keine Domain.
- Das Relay läuft im Beispiel mit `STATE_RELAY_DRY_RUN_APNS: "true"` und `STATE_RELAY_ALLOW_DEVELOPMENT_ATTEST: "true"`. Für echte Pushes braucht es APNs-Team-ID, Key-ID und die `.p8`-Datei (`cmd/state-relay/main.go`, Flags `apns-team-id`, `apns-key-id`, `apns-private-key`, Umgebungsvariablen `STATE_RELAY_APNS_TEAM_ID`, `STATE_RELAY_APNS_KEY_ID`, `STATE_RELAY_APNS_PRIVATE_KEY_FILE`).
- Das iPhone leitet die Relay-Adresse aus der Server-Adresse ab: `https://state.example.com` wird zu `https://relay.example.com`. **Deshalb müssen die Hostnamen `state.<domain>` und `relay.<domain>` heißen.**
- Die Images `ghcr.io/nicremo/state-*` werden aktuell nicht automatisch veröffentlicht (GitHub Actions sind aus). Auf dem VPS wird also aus dem Quellcode gebaut.
- Der Ziel-VPS nutzt bereits Traefik mit einem externen Docker-Netz. Namen von Entrypoint und Cert-Resolver sind **nicht** bekannt, deshalb werden sie Variablen.

## Ziel

Ein Kit, mit dem der Koordinator auf einem VPS mit vorhandenem Traefik in unter 15 Minuten State Server und Relay unter `state.<domain>` und `relay.<domain>` mit TLS und echtem APNs betreibt, inklusive Backup, Update und Smoke-Test.

## Pflichtlektüre

`deploy/compose.yaml`, `deploy/Dockerfile`, `docs/operations.md`, `docs/threat-model.md` (Abschnitte zu Relay und Secrets), `cmd/state-relay/main.go` (Flags und Umgebungsvariablen), `cmd/state-server/main.go` (Flags, `bootstrap-token`, `verify-audit`), `internal/api/handler.go` (suche die Routen `/health/ready`, `/health/live` und `/version`, prüfe die genauen Pfade).

## Task 1: Traefik-Override und Umgebungsvorlage

**Dateien:**
- Create: `deploy/compose.traefik.yaml`
- Create: `deploy/.env.example`
- Modify: `deploy/compose.yaml` (nur Relay-Umgebung auf Variablen umstellen, siehe unten)

`deploy/.env.example` (nur Platzhalter, keine echten Werte):

```dotenv
# Public base domain. Services answer on state.<domain> and relay.<domain>.
STATE_DOMAIN=example.com
# Name of the Traefik entrypoint that terminates HTTPS on the host.
TRAEFIK_ENTRYPOINT=websecure
# Name of the Traefik certificate resolver configured on the host.
TRAEFIK_CERTRESOLVER=letsencrypt
# Docker network Traefik watches. Must already exist.
PROXY_NETWORK=proxy-network

# Version label baked into the binaries.
STATE_VERSION=0.1.0

# APNs. Keep the .p8 file outside the repository, see docs/deploy-vps.md.
STATE_RELAY_APNS_TEAM_ID=
STATE_RELAY_APNS_KEY_ID=
STATE_RELAY_APNS_TOPIC=com.fabincrm.state
# Production TestFlight and App Store builds use production App Attest.
STATE_RELAY_ALLOW_DEVELOPMENT_ATTEST=false
# Set to true only for a dry run without Apple credentials.
STATE_RELAY_DRY_RUN_APNS=false
```

In `deploy/compose.yaml` beim Service `state-relay` die festen Werte durch Variablen mit Default ersetzen:

```yaml
      STATE_RELAY_ALLOW_DEVELOPMENT_ATTEST: ${STATE_RELAY_ALLOW_DEVELOPMENT_ATTEST:-true}
      STATE_RELAY_DRY_RUN_APNS: ${STATE_RELAY_DRY_RUN_APNS:-true}
      STATE_RELAY_APNS_TOPIC: ${STATE_RELAY_APNS_TOPIC:-com.fabincrm.state}
```

Die Defaults bleiben absichtlich im sicheren Dry-Run-Modus, damit `compose.yaml` allein nichts Echtes verschickt. Auch `image`-Tags und `STATE_VERSION`-Build-Args auf `${STATE_VERSION:-0.1.0}` umstellen. Das Netz `proxy-network` bekommt `name: ${PROXY_NETWORK:-proxy-network}`.

`deploy/compose.traefik.yaml`:

```yaml
# Production overlay for a host that already runs Traefik.
# Usage: docker compose --env-file deploy/.env -f deploy/compose.yaml -f deploy/compose.traefik.yaml up -d --build
services:
  state-server:
    labels:
      traefik.enable: "true"
      traefik.docker.network: ${PROXY_NETWORK:-proxy-network}
      traefik.http.routers.state-server.rule: Host(`state.${STATE_DOMAIN:?set STATE_DOMAIN}`)
      traefik.http.routers.state-server.entrypoints: ${TRAEFIK_ENTRYPOINT:-websecure}
      traefik.http.routers.state-server.tls: "true"
      traefik.http.routers.state-server.tls.certresolver: ${TRAEFIK_CERTRESOLVER:-letsencrypt}
      traefik.http.services.state-server.loadbalancer.server.port: "8090"

  state-relay:
    environment:
      STATE_RELAY_APNS_TEAM_ID: ${STATE_RELAY_APNS_TEAM_ID:?set STATE_RELAY_APNS_TEAM_ID}
      STATE_RELAY_APNS_KEY_ID: ${STATE_RELAY_APNS_KEY_ID:?set STATE_RELAY_APNS_KEY_ID}
      STATE_RELAY_APNS_PRIVATE_KEY_FILE: /run/secrets/apns_key
    secrets:
      - apns_key
    labels:
      traefik.enable: "true"
      traefik.docker.network: ${PROXY_NETWORK:-proxy-network}
      traefik.http.routers.state-relay.rule: Host(`relay.${STATE_DOMAIN:?set STATE_DOMAIN}`)
      traefik.http.routers.state-relay.entrypoints: ${TRAEFIK_ENTRYPOINT:-websecure}
      traefik.http.routers.state-relay.tls: "true"
      traefik.http.routers.state-relay.tls.certresolver: ${TRAEFIK_CERTRESOLVER:-letsencrypt}
      traefik.http.services.state-relay.loadbalancer.server.port: "8091"

secrets:
  apns_key:
    file: ./secrets/apns_key.p8
```

Ergänze `deploy/secrets/.gitignore` mit Inhalt:

```gitignore
*
!.gitignore
```

**Wichtig:** Prüfe in `cmd/state-relay/main.go`, ob das Relay die Key-Datei als Nutzer 10001 lesen kann, wenn sie als Docker-Secret eingebunden ist (Compose-Secrets aus Dateien werden mit den Rechten der Quelldatei gemountet). Schreibe in `docs/deploy-vps.md`, dass die Datei `chmod 0440` und `chown 10001:10001` (oder Gruppe 10001) braucht.

**Validierung:**

```bash
docker compose version || echo "NO_DOCKER"
cp deploy/.env.example /tmp/wp03.env
sed -i '' 's/^STATE_RELAY_APNS_TEAM_ID=.*/STATE_RELAY_APNS_TEAM_ID=TEAMID1234/; s/^STATE_RELAY_APNS_KEY_ID=.*/STATE_RELAY_APNS_KEY_ID=KEYID12345/' /tmp/wp03.env
mkdir -p deploy/secrets && touch deploy/secrets/apns_key.p8
docker compose --env-file /tmp/wp03.env -f deploy/compose.yaml -f deploy/compose.traefik.yaml config > /tmp/wp03-config.yaml && echo CONFIG_OK
grep -n "Host(" /tmp/wp03-config.yaml
rm deploy/secrets/apns_key.p8
```

Erwartet: `CONFIG_OK` und zwei `Host(...)`-Regeln mit `state.example.com` und `relay.example.com`. Fehlt Docker (`NO_DOCKER`), stattdessen die YAML-Syntax prüfen:

```bash
python3 -c "import yaml,sys; [yaml.safe_load(open(f)) for f in ['deploy/compose.yaml','deploy/compose.traefik.yaml']]; print('YAML_OK')"
```

und im Report vermerken, dass `docker compose config` nicht lief.

Die leere Testdatei `deploy/secrets/apns_key.p8` **nicht** committen (die `.gitignore` verhindert das, trotzdem `git status` prüfen).

**Commit:**

```bash
git add deploy/compose.yaml deploy/compose.traefik.yaml deploy/.env.example deploy/secrets/.gitignore
git commit -m "feat: add a traefik production overlay for server and relay"
```

## Task 2: Betriebsskripte

**Dateien (alle mit `#!/usr/bin/env bash` und `set -euo pipefail`, ausführbar):**
- Create: `scripts/vps/state.sh` (einziger Einstiegspunkt mit Unterbefehlen)
- Create: `scripts/vps/README.md` (kurz, Englisch)

`scripts/vps/state.sh` Unterbefehle:

| Befehl | Tut |
| --- | --- |
| `up` | `docker compose --env-file deploy/.env -f deploy/compose.yaml -f deploy/compose.traefik.yaml up -d --build` |
| `down` | dasselbe mit `down` (ohne `-v`, Volumes bleiben) |
| `logs [service]` | `... logs --tail 200 -f [service]` |
| `ps` | `... ps` |
| `bootstrap-token` | `... exec state-server state-server bootstrap-token --data /data` |
| `verify-audit` | `... exec state-server state-server verify-audit --data /data` |
| `backup` | Siehe unten |
| `update` | `git pull --ff-only`, dann `up`, dann `smoke` |
| `smoke` | Siehe unten |

Das Skript ermittelt das Repo-Root über `cd "$(dirname "$0")/../.."` und bricht mit klarer Meldung ab, wenn `deploy/.env` fehlt (`deploy/.env missing, copy deploy/.env.example and fill it in`).

`backup`: Lies zuerst `docs/operations.md`, ob dort ein Backup-Verfahren beschrieben ist (z.B. SQLite-Online-Backup). Folge diesem Verfahren. Wenn keines beschrieben ist: Server stoppen, beide Volumes per `docker run --rm -v state-server-data:/data:ro -v "$PWD/backups":/out alpine tar czf /out/state-server-$(date -u +%Y%m%dT%H%M%SZ).tgz -C /data .` sichern (ebenso `state-relay-data`), Server wieder starten. Backups landen in `./backups/` im Repo-Root. Ergänze `backups/` in der Root-`.gitignore` nur, falls dort noch nicht vorhanden, und nenne es im Report als "Datei außerhalb meines Bereichs".

`smoke`: liest `STATE_DOMAIN` aus `deploy/.env` und prüft:

```bash
curl -fsS "https://state.${STATE_DOMAIN}/health/ready"
curl -fsS "https://state.${STATE_DOMAIN}/version"
curl -fsS "https://relay.${STATE_DOMAIN}/health/ready"
```

(Pfade vorher in `internal/api/handler.go` und `internal/relay/handler.go` verifizieren.) Gibt `SMOKE_OK` aus oder beendet sich mit Exit 1 und nennt die fehlgeschlagene URL.

**Prüfung:**

```bash
bash -n scripts/vps/state.sh && echo SYNTAX_OK
command -v shellcheck && shellcheck scripts/vps/state.sh
scripts/vps/state.sh 2>&1 | head -5     # ohne Argument: Usage-Text, Exit != 0
```

**Commit:** `git commit -m "feat: add a single operations script for the vps deployment"`

## Task 3: Deployment-Anleitung

**Datei:** `docs/deploy-vps.md` (Englisch)

Gliederung:

1. **Prerequisites:** Linux-VPS mit Docker Engine und Compose v2.39+, laufendes Traefik mit HTTPS-Entrypoint und Cert-Resolver, externes Docker-Netz, zwei DNS-A/AAAA-Records `state.<domain>` und `relay.<domain>` auf die VPS-IP, Apple-Developer-Account mit APNs-Auth-Key (.p8).
2. **Find your Traefik names:** Befehle, um Entrypoint, Cert-Resolver und Netz zu finden: `docker network ls`, `docker inspect <traefik-container> --format '{{json .Config.Cmd}}'`, alternativ die Traefik-Static-Config.
3. **Install:** `git clone https://github.com/nicremo/state.git /opt/state`, `cp deploy/.env.example deploy/.env`, Werte eintragen, APNs-Key nach `deploy/secrets/apns_key.p8` kopieren (`scp`), Rechte setzen, `scripts/vps/state.sh up`, `scripts/vps/state.sh smoke`.
4. **Pair the owner:** `scripts/vps/state.sh bootstrap-token`. In der iPhone-App "Server verbinden" mit `https://state.<domain>` und diesem Token. Hinweis: Token wie ein Passwort behandeln, nicht in Shell-History speichern (`HISTCONTROL=ignorespace`, Befehl mit führendem Leerzeichen).
5. **Push checklist:** `STATE_RELAY_DRY_RUN_APNS=false`, Team-ID, Key-ID, Topic `com.fabincrm.state`, Production-Attest für TestFlight. Test: in der App einen Reminder in 2 Minuten anlegen, App schließen, Push kommt.
6. **Backups and restore:** `scripts/vps/state.sh backup`, Restore-Schritte (Server stoppen, Volume leeren, Archiv entpacken, starten, `verify-audit`).
7. **Updates:** `scripts/vps/state.sh update`.
8. **Troubleshooting:** 404 von Traefik (Labels/Netz), Zertifikat fehlt (DNS, Port 80/443 offen, Firewall), Relay 401 bei Registrierung (Attest-Umgebung), Pushes kommen nicht (Dry-Run noch an, falsches Topic, Key-ID).

Verlinke `docs/deploy-vps.md` aus `docs/operations.md` mit einem Satz am Anfang (Datei außerhalb deines Bereichs, im Report nennen).

**Commit:** `git commit -m "docs: add a step-by-step vps deployment guide"`

## Task 4: Gesamtprüfung

```bash
bash -n scripts/vps/state.sh
git status --short        # darf keine .env, .p8 oder Backup-Datei zeigen
git grep -nE "BEGIN (EC )?PRIVATE KEY|STATE_RELAY_APNS_KEY_ID=[A-Z0-9]{10}" -- deploy docs scripts || echo NO_SECRETS
```

Erwartet: `NO_SECRETS`.

Report und Draft-PR nach Master-Plan Abschnitt 4.4 und 4.5. Im Report unter "Manuelle Schritte" die exakte Befehlsfolge für den Koordinator auflisten.
