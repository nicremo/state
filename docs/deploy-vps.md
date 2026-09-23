# Deploying State on a VPS with Traefik

This guide brings `state-server` and `state-relay` up on a Linux VPS that already runs Traefik, reachable as
`https://state.<domain>` and `https://relay.<domain>` with automatic TLS, real APNs delivery, encrypted backups,
updates and a smoke test.

The two host names are fixed on purpose: the iPhone derives the relay address from the server address by replacing
the `state.` prefix with `relay.`, so `https://state.example.com` becomes `https://relay.example.com`. If the records
are named differently, pushes cannot be registered by the app.

Expected time: under 15 minutes once DNS resolves and the APNs key exists. The first `up` compiles the Go binaries
inside Docker and takes a few minutes on a small VPS.

## 1. Prerequisites

| Requirement | Check |
| --- | --- |
| Linux VPS with Docker Engine | `docker version` |
| Docker Compose v2.39 or newer | `docker compose version` |
| Traefik already running with an HTTPS entrypoint and a certificate resolver | `docker ps --format '{{.Names}} {{.Image}}' \| grep -i traefik` |
| External Docker network shared with Traefik | `docker network ls` |
| DNS A or AAAA records `state.<domain>` and `relay.<domain>` pointing at the VPS | `dig +short state.<domain>` |
| Ports 80 and 443 reachable from the internet for the ACME challenge | `curl -I http://state.<domain>` |
| Apple Developer account with an APNs auth key (`.p8`), its Key ID and the Team ID | Apple Developer portal, Keys, Apple Push Notifications service |
| `age` for encrypted backups | `age --version`, install with `sudo apt install age` |

No host ports are published. Traefik reaches both containers over the shared Docker network.

## 2. Find your Traefik names

Entrypoint, certificate resolver and network name are not hard coded, because they differ per host. Find them once:

```bash
docker network ls
docker ps --format '{{.Names}}' | grep -i traefik
docker inspect <traefik-container> --format '{{json .Config.Cmd}}'
docker inspect <traefik-container> --format '{{json .Args}}'
docker inspect <traefik-container> --format '{{json .Mounts}}'
```

`Config.Cmd` and `Args` show command line flags such as `--entrypoints.websecure.address=:443` and
`--certificatesresolvers.letsencrypt.acme.email=...`. The `Mounts` output points at the static configuration file,
usually `traefik.yml`, which contains the same names in YAML form:

```yaml
entryPoints:
  websecure:
    address: ":443"
certificatesResolvers:
  letsencrypt:
    acme:
      email: owner@example.com
      storage: /letsencrypt/acme.json
      httpChallenge:
        entryPoint: web
```

The Docker network that Traefik is attached to is the value for `PROXY_NETWORK`. Write the three names into
`deploy/.env`:

```dotenv
TRAEFIK_ENTRYPOINT=websecure
TRAEFIK_CERTRESOLVER=letsencrypt
PROXY_NETWORK=proxy-network
```

## 3. Install

```bash
sudo git clone https://github.com/nicremo/state.git /opt/state
cd /opt/state
cp deploy/.env.example deploy/.env
sudo chown "$USER" deploy/.env
```

Edit `deploy/.env`:

* `STATE_DOMAIN`: your base domain, for example `example.com`.
* `TRAEFIK_ENTRYPOINT`, `TRAEFIK_CERTRESOLVER`, `PROXY_NETWORK`: the names found in step 2.
* `STATE_VERSION`: image tag and version string baked into the binaries, keep the default unless you roll a release.
* `STATE_RELAY_APNS_TEAM_ID` and `STATE_RELAY_APNS_KEY_ID`: your Apple Team ID and the Key ID of the `.p8` key.
* `STATE_RELAY_APNS_TOPIC`: `com.fabincrm.state`, matching the app bundle ID.
* `STATE_RELAY_DRY_RUN_APNS`: `false` for real pushes, `true` only for a dry run without Apple credentials.
* `STATE_RELAY_ALLOW_DEVELOPMENT_ATTEST`: `false` for TestFlight and App Store builds.

The overlay refuses to start when `STATE_DOMAIN`, `STATE_RELAY_APNS_TEAM_ID` or `STATE_RELAY_APNS_KEY_ID` are empty,
so a missing value fails with a readable message instead of a broken deployment. A dry run still needs non-empty
values there, because the guard runs before the relay ever looks at `STATE_RELAY_DRY_RUN_APNS`.

### Copy the APNs key

Keep the `.p8` file out of the repository. `deploy/secrets/` and `deploy/.env` are gitignored.

```bash
scp ~/Downloads/AuthKey_XXXXXXXXXX.p8 root@<vps-ip>:/tmp/apns_key.p8
sudo install -o root -g 10001 -m 0440 /tmp/apns_key.p8 /opt/state/deploy/secrets/apns_key.p8
shred -u /tmp/apns_key.p8 2>/dev/null || rm -f /tmp/apns_key.p8
ls -l /opt/state/deploy/secrets/apns_key.p8
```

The last line must show `-r--r----- root 10001`.

This matters: a Compose file secret is a read-only bind mount of the source file, so the owner, group and mode of the
host file decide what the container can read. The relay runs as user `10001:10001` and opens the file at
`/run/secrets/apns_key`. With `-rw------- root root` the relay stops with `read APNs private key: permission denied`.
Either owner or group `10001` with mode `0440` works, so `chown 10001:10001` with `chmod 0440` is equally valid.
Use `install` again to rotate the key, then `scripts/vps/state.sh up`.

### Start

```bash
scripts/vps/state.sh up
scripts/vps/state.sh ps
scripts/vps/state.sh smoke
```

`up` builds both images from the checked out source, because the `ghcr.io/nicremo/state-*` images are not published
automatically. `smoke` checks the three public endpoints and prints `SMOKE_OK`:

```text
{"status":"ready"}
{"name":"state-server","version":"0.1.0","api_version":"v1"}
{"status":"ready"}
SMOKE_OK
```

If a check fails, the script exits with code 1 and names the failing URL.

## 4. Pair the owner

```bash
scripts/vps/state.sh bootstrap-token
```

The command prints the token from `/data/state_secrets/bootstrap.token` and creates it when it does not exist yet.
Treat it like a password: it is exchanged for a device credential, and anybody who has it can claim the instance.

Keep it out of the shell history. In bash set `HISTCONTROL=ignorespace` and prefix any command that contains the
token with a space, in zsh use `setopt histignorespace` and the same trick.

On the iPhone open State, choose "Server verbinden", enter `https://state.<domain>` and paste the token. Register the
device, then confirm in the app that reminders sync.

## 5. Push checklist (real APNs)

1. `deploy/.env` has `STATE_RELAY_DRY_RUN_APNS=false`.
2. `STATE_RELAY_APNS_TEAM_ID` and `STATE_RELAY_APNS_KEY_ID` match the `.p8` key that is mounted.
3. `STATE_RELAY_APNS_TOPIC=com.fabincrm.state`.
4. `STATE_RELAY_ALLOW_DEVELOPMENT_ATTEST=false` for TestFlight and App Store builds, which use production App Attest.
   Only an Xcode debug build needs `true`, and it must be switched back afterwards.
5. `scripts/vps/state.sh up`, which recreates the relay with the new environment.
6. `scripts/vps/state.sh logs state-relay` shows no `APNs dry run mode is enabled` warning.
7. End to end test: create a reminder two minutes in the future in the app, close the app, and wait. The push must
   arrive with the lock screen, outside of any network the Mac Server could serve.

## 6. Backups and restore

Configure an age recipient once. The private identity never leaves your Mac:

```bash
age-keygen -o ~/state-backup-identity.txt      # on the Mac, keep this file safe
grep 'public key' ~/state-backup-identity.txt  # age1...
```

Put the public key on the VPS:

```bash
printf '%s\n' 'age1...' | sudo tee /opt/state/deploy/secrets/backup-recipient.txt >/dev/null
sudo chmod 0444 /opt/state/deploy/secrets/backup-recipient.txt
```

Then take a backup:

```bash
cd /opt/state
scripts/vps/state.sh backup
ls -l backups/
```

`backup` follows the procedure in `docs/operations.md`: it stops both services for a consistent snapshot, archives
`state-server-data` and `state-relay-data`, encrypts the archive with `age`, writes a SHA-256 checksum and starts the
services again. The result is `backups/state-<timestamp>.tar.gz.age` plus `.sha256`, both mode `0600`. Instead of the
recipient file you can pass the key as a variable: `STATE_AGE_RECIPIENT='age1...' scripts/vps/state.sh backup`.

Verify a backup without touching production data:

```bash
STATE_SERVICE_ROOT=/opt/state \
STATE_AGE_IDENTITY_FILE=~/state-backup-identity.txt \
  ./ops/verify-backup.sh /opt/state/backups/state-<timestamp>.tar.gz.age
```

The script decrypts into temporary Docker volumes, starts isolated server and relay containers, waits for both
readiness endpoints and verifies the restored audit chain. It removes only its own containers and volumes.

Both verification and restore start their isolated containers from `STATE_SERVER_IMAGE` and `STATE_RELAY_IMAGE`, which
default to `ghcr.io/nicremo/state-server:0.1.0` and `ghcr.io/nicremo/state-relay:0.1.0`. The locally built images carry
exactly those tags while `STATE_VERSION=0.1.0`. If you changed `STATE_VERSION`, pass the matching image names as
variables to both scripts, otherwise they try to pull an image that does not exist.

Restore only during a maintenance window. The restore script stops both services itself, checks the checksum,
decrypts, replaces the content of both data volumes and starts the services again:

```bash
cd /opt/state
STATE_RESTORE_CONFIRM=restore \
STATE_SERVICE_ROOT=/opt/state \
STATE_AGE_IDENTITY_FILE=~/state-backup-identity.txt \
  ./ops/restore-state.sh /opt/state/backups/state-<timestamp>.tar.gz.age
scripts/vps/state.sh up
scripts/vps/state.sh verify-audit
```

Copy the encrypted archives off the VPS, for example with `rsync` or `restic`, and never store the age identity on
the VPS next to the backups.

## 7. Updates

```bash
cd /opt/state
scripts/vps/state.sh backup
scripts/vps/state.sh update
```

`update` runs `git pull --ff-only`, rebuilds and restarts both services, then runs `smoke`. Because the images are
built from source, the running version is the checked out commit plus `STATE_VERSION` from `deploy/.env`.

Rollback:

```bash
git log --oneline -5
git switch --detach <previous-commit>
scripts/vps/state.sh up
scripts/vps/state.sh smoke
git switch main          # back to the branch once a fix is released
```

Volumes are never removed by `down` or `update`. `docker compose down -v` would delete them, do not use it.

## 8. Troubleshooting

| Symptom | Check |
| --- | --- |
| Traefik answers 404 for both hosts | `docker inspect state-state-server-1 --format '{{json .Config.Labels}}'` must show the router rules, `docker network inspect <PROXY_NETWORK>` must list the State containers, `traefik.docker.network` must name that network, and the router `Host()` rule must match your domain |
| Certificate missing, HTTPS fails | `dig +short state.<domain>` and `dig +short relay.<domain>` must return the VPS IP, ports 80 and 443 must be open in the firewall, then `docker logs <traefik-container> --tail 100` for ACME errors and rate limits |
| Relay answers 401 or 403 on registration | App Attest environment mismatch: a production build needs `STATE_RELAY_ALLOW_DEVELOPMENT_ATTEST=false`, a debug build needs `true`. `STATE_RELAY_APP_ID` must stay `5DKU7FFK4X.com.fabincrm.state` |
| The relay container restarts in a loop | `scripts/vps/state.sh logs state-relay`. `APNs credentials are required unless dry run mode is enabled` means a value is missing, `read APNs private key: permission denied` means the `.p8` is not readable by user 10001 |
| No pushes arrive | Check `STATE_RELAY_DRY_RUN_APNS=false`, the Key ID, the Team ID and the topic first. `scripts/vps/state.sh logs state-server` shows `push scheduler cycle failed` when the relay rejects a notification, but the relay reports only `code internal_error` and never logs the APNs reason, so a rejected send cannot be told from a wrong token by the log alone. Confirm that the device registered against the same APNs environment as the build |
| `up` fails with `required variable STATE_DOMAIN is missing a value` | `deploy/.env` is missing or empty, copy `deploy/.env.example` and fill it in |
| `up` fails with an external network error | The network named by `PROXY_NETWORK` does not exist, compare with `docker network ls` |

## Files in this kit

| File | Purpose |
| --- | --- |
| `deploy/compose.yaml` | Base stack: server on 8090, relay on 8091, volumes, hardening, dry run defaults |
| `deploy/compose.traefik.yaml` | Production overlay with Traefik labels and the APNs secret |
| `deploy/.env.example` | Template for `deploy/.env`, no real values |
| `deploy/secrets/.gitignore` | Keeps the APNs key and the age recipient out of Git |
| `scripts/vps/state.sh` | Single entry point: `up`, `down`, `logs`, `ps`, `bootstrap-token`, `verify-audit`, `backup`, `update`, `smoke` |
| `scripts/vps/README.md` | Short command reference |

Related documents: `docs/operations.md` for the Nginx Proxy Manager variant and the audit procedure,
`docs/threat-model.md` for the security boundaries of the relay, `docs/architecture.md` for the component overview.
