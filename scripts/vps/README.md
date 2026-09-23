# VPS operations scripts

`state.sh` is the single entry point for running State Server and the APNs relay on a VPS behind Traefik.

## Usage

```bash
scripts/vps/state.sh up                # build and start both services
scripts/vps/state.sh ps
scripts/vps/state.sh logs state-relay
scripts/vps/state.sh bootstrap-token   # one-time owner pairing token
scripts/vps/state.sh smoke             # public HTTPS checks for both hosts
scripts/vps/state.sh backup            # encrypted snapshot of both data volumes
scripts/vps/state.sh update            # git pull --ff-only, up, smoke
scripts/vps/state.sh verify-audit
scripts/vps/state.sh down              # keeps the named volumes
```

The script reads configuration from `deploy/.env` (copy `deploy/.env.example`) and always composes
`deploy/compose.yaml` together with the Traefik overlay `deploy/compose.traefik.yaml`. It must run from
anywhere inside the repository clone; it resolves the repository root itself.

`backup` follows the encrypted procedure in `docs/operations.md` and writes
`backups/state-<timestamp>.tar.gz.age` plus a checksum. It requires the `age` binary and either the
age public key in `deploy/secrets/backup-recipient.txt` or `STATE_AGE_RECIPIENT` in the environment.
Services are stopped for the snapshot and started again afterwards.

The full deployment walkthrough is in `docs/deploy-vps.md`.
