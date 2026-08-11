# Operations guide

## Compose deployment

The supplied stack has no public host ports. Create the external proxy network once if Nginx Proxy Manager did not create it:

```bash
docker network inspect proxy-network
docker compose -f deploy/compose.yaml build
docker compose -f deploy/compose.yaml up -d
docker compose -f deploy/compose.yaml ps
```

Persistent volumes are `state-server-data`, `state-relay-data` and `state-backups`. Never remove those volumes during routine updates.

## Nginx Proxy Manager

Create two proxy hosts:

| Public host | Forward host | Forward port |
| --- | --- | --- |
| `state.example.com` | `state-server` | `8090` |
| `relay.example.com` | `state-relay` | `8091` |

Enable Websockets Support, HTTP/2, a valid Let's Encrypt certificate and Force SSL. Keep HSTS disabled until both hosts pass end-to-end tests.

Use this advanced configuration for the State host:

```nginx
proxy_read_timeout 3600s;
proxy_send_timeout 3600s;

location = /mcp {
    proxy_pass http://state-server:8090/mcp;
    proxy_http_version 1.1;
    proxy_buffering off;
    proxy_cache off;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
}
```

Verify after each proxy change:

```bash
curl --fail https://state.example.com/health/ready
curl --fail https://state.example.com/version
curl --fail https://relay.example.com/health/ready
```

Then run `statectl doctor` through the public State URL. Enable HSTS only after these checks and iOS pairing succeed.

## Owner bootstrap

Read the initial token without copying it into a repository file:

```bash
docker compose -f deploy/compose.yaml exec state-server \
  state-server bootstrap-token --data /data
```

Enter it into the iOS owner setup screen. The app exchanges it for a device credential.

## APNs relay activation

The committed stack intentionally uses `STATE_RELAY_DRY_RUN_APNS=true`. Before enabling real delivery:

1. Configure a permanent relay domain.
2. Store the APNs `.p8` file outside the repository with restrictive permissions.
3. Mount that single file read-only into the relay container.
4. Set `STATE_RELAY_APNS_TEAM_ID`, `STATE_RELAY_APNS_KEY_ID`, `STATE_RELAY_APNS_TOPIC` and `STATE_RELAY_APNS_PRIVATE_KEY_FILE` through protected deployment configuration.
5. Set `STATE_RELAY_ALLOW_DEVELOPMENT_ATTEST=false`.
6. Set `STATE_RELAY_DRY_RUN_APNS=false`.
7. Verify App Attest registration, encrypted delivery and fallback text on a real device.

## Encrypted backup

Set an age recipient through a protected runtime variable and run:

```bash
STATE_AGE_RECIPIENT='age1...' ./ops/backup-state.sh
```

The script stops the State services for a consistent snapshot, archives both data volumes, encrypts the archive and starts the services again through a trap. It writes a SHA-256 checksum beside the encrypted artifact.

Test restore into isolated volumes before relying on a backup:

```bash
STATE_AGE_IDENTITY_FILE=/secure/path/to/identity.txt \
  ./ops/restore-state.sh /absolute/path/to/state-backup.tar.gz.age
```

The restore script requires the exact confirmation text `RESTORE STATE`. It must only be used during a planned maintenance window.

## Audit verification

```bash
docker compose -f deploy/compose.yaml exec state-server \
  state-server verify-audit --data /data
```

Run this after restores and periodically through monitoring.

## Update procedure

1. Create and verify an encrypted backup.
2. Pull an immutable version tag or digest.
3. Run `docker compose config --quiet`.
4. Build or pull images.
5. Run `docker compose up -d`.
6. Check container health, public TLS endpoints, audit verification and `statectl doctor`.
7. Keep the prior immutable image available for rollback.
