#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
  printf '%s\n' "usage: verify-backup.sh <state-backup.tar.gz.age>" >&2
  exit 2
fi

STATE_SERVICE_ROOT=${STATE_SERVICE_ROOT:-/root/services/state}
STATE_AGE_IDENTITY_FILE=${STATE_AGE_IDENTITY_FILE:-${STATE_SERVICE_ROOT}/secrets/backup-identity.txt}
STATE_SERVER_IMAGE=${STATE_SERVER_IMAGE:-ghcr.io/nicremo/state-server:0.1.0}
STATE_RELAY_IMAGE=${STATE_RELAY_IMAGE:-ghcr.io/nicremo/state-relay:0.1.0}
STATE_ENCRYPTED_ARCHIVE=$1
STATE_TIMESTAMP=$(date -u +%Y%m%dT%H%M%SZ)
STATE_VERIFY_PREFIX=state-restore-test-${STATE_TIMESTAMP}-$$
STATE_SERVER_VOLUME=${STATE_VERIFY_PREFIX}-server
STATE_RELAY_VOLUME=${STATE_VERIFY_PREFIX}-relay
STATE_SERVER_CONTAINER=${STATE_VERIFY_PREFIX}-server
STATE_RELAY_CONTAINER=${STATE_VERIFY_PREFIX}-relay
STATE_PLAIN_ARCHIVE=$(mktemp /tmp/state-restore-test.XXXXXX.tar.gz)

cleanup() {
  docker rm -f "${STATE_SERVER_CONTAINER}" "${STATE_RELAY_CONTAINER}" >/dev/null 2>&1 || true
  docker volume rm "${STATE_SERVER_VOLUME}" "${STATE_RELAY_VOLUME}" >/dev/null 2>&1 || true
  rm -f "${STATE_PLAIN_ARCHIVE}"
}
trap cleanup EXIT INT TERM

case "${STATE_ENCRYPTED_ARCHIVE}" in
  /*) ;;
  *)
    printf '%s\n' "backup path must be absolute" >&2
    exit 2
    ;;
esac

test -f "${STATE_ENCRYPTED_ARCHIVE}"
test -f "${STATE_ENCRYPTED_ARCHIVE}.sha256"
test -f "${STATE_AGE_IDENTITY_FILE}"
sha256sum -c "${STATE_ENCRYPTED_ARCHIVE}.sha256"
age -d -i "${STATE_AGE_IDENTITY_FILE}" -o "${STATE_PLAIN_ARCHIVE}" "${STATE_ENCRYPTED_ARCHIVE}"
tar -tzf "${STATE_PLAIN_ARCHIVE}" >/dev/null

docker volume create "${STATE_SERVER_VOLUME}" >/dev/null
docker volume create "${STATE_RELAY_VOLUME}" >/dev/null
docker run --rm \
  -v "${STATE_SERVER_VOLUME}:/target/server" \
  -v "${STATE_RELAY_VOLUME}:/target/relay" \
  -v /tmp:/input:ro \
  alpine:3.23.3 \
  tar -C /target -xzf "/input/$(basename "${STATE_PLAIN_ARCHIVE}")"

docker run -d \
  --name "${STATE_SERVER_CONTAINER}" \
  --read-only \
  --user 10001:10001 \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --tmpfs /tmp:rw,noexec,nosuid,size=32m \
  -e STATE_DATA_DIR=/data \
  -e STATE_HTTP_ADDR=127.0.0.1:8090 \
  -v "${STATE_SERVER_VOLUME}:/data" \
  "${STATE_SERVER_IMAGE}" serve >/dev/null

docker run -d \
  --name "${STATE_RELAY_CONTAINER}" \
  --read-only \
  --user 10001:10001 \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --tmpfs /tmp:rw,noexec,nosuid,size=32m \
  -e STATE_RELAY_DATA_DIR=/data \
  -e STATE_RELAY_HTTP_ADDR=127.0.0.1:8091 \
  -e STATE_RELAY_APP_ID=5DKU7FFK4X.com.fabincrm.state \
  -e STATE_RELAY_ALLOW_DEVELOPMENT_ATTEST=true \
  -e STATE_RELAY_DRY_RUN_APNS=true \
  -e STATE_RELAY_APNS_TOPIC=com.fabincrm.state \
  -v "${STATE_RELAY_VOLUME}:/data" \
  "${STATE_RELAY_IMAGE}" serve >/dev/null

wait_for_ready() {
  container=$1
  port=$2
  attempt=0
  while [ "${attempt}" -lt 30 ]; do
    if docker exec "${container}" wget -qO- "http://127.0.0.1:${port}/health/ready" 2>/dev/null | grep -q '"status":"ready"'; then
      return 0
    fi
    attempt=$((attempt + 1))
    sleep 1
  done
  docker logs "${container}" >&2
  return 1
}

wait_for_ready "${STATE_SERVER_CONTAINER}" 8090
wait_for_ready "${STATE_RELAY_CONTAINER}" 8091
docker stop "${STATE_SERVER_CONTAINER}" >/dev/null
docker run --rm \
  --user 10001:10001 \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  -v "${STATE_SERVER_VOLUME}:/data" \
  "${STATE_SERVER_IMAGE}" verify-audit --data /data >/dev/null

printf '%s\n' "backup restore verified"
