#!/bin/sh
set -eu

STATE_SERVICE_ROOT=${STATE_SERVICE_ROOT:-/root/services/state}
STATE_BACKUP_ROOT=${STATE_BACKUP_ROOT:-/root/backups/services/state}
STATE_AGE_RECIPIENT_FILE=${STATE_AGE_RECIPIENT_FILE:-${STATE_SERVICE_ROOT}/secrets/backup-recipient.txt}
STATE_TIMESTAMP=$(date -u +%Y%m%dT%H%M%SZ)
STATE_PLAIN_ARCHIVE=$(mktemp /tmp/state-backup.XXXXXX.tar.gz)
STATE_ENCRYPTED_ARCHIVE=${STATE_BACKUP_ROOT}/state-${STATE_TIMESTAMP}.tar.gz.age

cleanup() {
  rm -f "${STATE_PLAIN_ARCHIVE}"
}
trap cleanup EXIT INT TERM

test -d "${STATE_SERVICE_ROOT}"
test -f "${STATE_AGE_RECIPIENT_FILE}"
mkdir -p "${STATE_BACKUP_ROOT}"

cd "${STATE_SERVICE_ROOT}"
restart_required=1
docker compose -f deploy/compose.yaml stop state-server state-relay
trap 'cleanup; if [ "${restart_required:-0}" = 1 ]; then docker compose -f "${STATE_SERVICE_ROOT}/deploy/compose.yaml" start state-server state-relay >/dev/null; fi' EXIT INT TERM

docker run --rm \
  -v state-server-data:/source/server:ro \
  -v state-relay-data:/source/relay:ro \
  -v /tmp:/output \
  alpine:3.23.3 \
  tar -C /source -czf "/output/$(basename "${STATE_PLAIN_ARCHIVE}")" server relay

age -R "${STATE_AGE_RECIPIENT_FILE}" -o "${STATE_ENCRYPTED_ARCHIVE}" "${STATE_PLAIN_ARCHIVE}"
sha256sum "${STATE_ENCRYPTED_ARCHIVE}" > "${STATE_ENCRYPTED_ARCHIVE}.sha256"
chmod 0600 "${STATE_ENCRYPTED_ARCHIVE}" "${STATE_ENCRYPTED_ARCHIVE}.sha256"

docker compose -f deploy/compose.yaml start state-server state-relay
restart_required=0
printf '%s\n' "${STATE_ENCRYPTED_ARCHIVE}"
