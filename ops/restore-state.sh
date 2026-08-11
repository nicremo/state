#!/bin/sh
set -eu

if [ "${STATE_RESTORE_CONFIRM:-}" != "restore" ]; then
  printf '%s\n' "Set STATE_RESTORE_CONFIRM=restore to continue." >&2
  exit 2
fi
if [ "$#" -ne 1 ]; then
  printf '%s\n' "usage: restore-state.sh <state-backup.tar.gz.age>" >&2
  exit 2
fi

STATE_SERVICE_ROOT=${STATE_SERVICE_ROOT:-/root/services/state}
STATE_AGE_IDENTITY_FILE=${STATE_AGE_IDENTITY_FILE:-${STATE_SERVICE_ROOT}/secrets/backup-identity.txt}
STATE_ENCRYPTED_ARCHIVE=$1
STATE_PLAIN_ARCHIVE=$(mktemp /tmp/state-restore.XXXXXX.tar.gz)
restart_required=0

cleanup() {
  rm -f "${STATE_PLAIN_ARCHIVE}"
}
trap 'cleanup; if [ "${restart_required:-0}" = 1 ]; then docker compose -f "${STATE_SERVICE_ROOT}/deploy/compose.yaml" start state-server state-relay >/dev/null; fi' EXIT INT TERM

test -f "${STATE_ENCRYPTED_ARCHIVE}"
test -f "${STATE_ENCRYPTED_ARCHIVE}.sha256"
test -f "${STATE_AGE_IDENTITY_FILE}"
sha256sum -c "${STATE_ENCRYPTED_ARCHIVE}.sha256"
age -d -i "${STATE_AGE_IDENTITY_FILE}" -o "${STATE_PLAIN_ARCHIVE}" "${STATE_ENCRYPTED_ARCHIVE}"
tar -tzf "${STATE_PLAIN_ARCHIVE}" >/dev/null

cd "${STATE_SERVICE_ROOT}"
restart_required=1
docker compose -f deploy/compose.yaml stop state-server state-relay
docker run --rm \
  -v state-server-data:/target/server \
  -v state-relay-data:/target/relay \
  -v /tmp:/input:ro \
  alpine:3.23.3 \
  sh -eu -c 'find /target/server -mindepth 1 -delete; find /target/relay -mindepth 1 -delete; tar -C /target -xzf "/input/$1"' sh "$(basename "${STATE_PLAIN_ARCHIVE}")"
docker compose -f deploy/compose.yaml start state-server state-relay
restart_required=0
