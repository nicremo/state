#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

ENV_FILE="${REPO_ROOT}/deploy/.env"
COMPOSE_BASE="${REPO_ROOT}/deploy/compose.yaml"
COMPOSE_TRAEFIK="${REPO_ROOT}/deploy/compose.traefik.yaml"
BACKUP_DIR="${REPO_ROOT}/backups"
BACKUP_RECIPIENT_FILE="${REPO_ROOT}/deploy/secrets/backup-recipient.txt"
DOCUMENTED_BACKUP_SCRIPT="${REPO_ROOT}/ops/backup-state.sh"
ALPINE_IMAGE="alpine:3.23.3"

log() {
  printf '%s\n' "$*"
}

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
state.sh: operations entry point for the State VPS deployment.

Usage: scripts/vps/state.sh <command> [arguments]

Commands:
  up                Build images and start state-server and state-relay
  down              Stop and remove the containers, named volumes are kept
  logs [service]    Follow the last 200 log lines, optionally for one service
  ps                Show container status
  bootstrap-token   Print the one-time owner pairing token
  verify-audit      Verify the server audit hash chain
  backup            Create an encrypted backup of both data volumes
  update            git pull --ff-only, then up, then smoke
  smoke             Check both public HTTPS endpoints
  help              Show this message

Configuration lives in deploy/.env, see deploy/.env.example and docs/deploy-vps.md.
EOF
}

require_env_file() {
  [[ -f "${ENV_FILE}" ]] || fail "deploy/.env missing, copy deploy/.env.example and fill it in"
}

require_docker() {
  command -v docker >/dev/null 2>&1 || fail "docker is required but not on PATH"
}

compose() {
  docker compose --env-file "${ENV_FILE}" -f "${COMPOSE_BASE}" -f "${COMPOSE_TRAEFIK}" "$@"
}

# Read one key from deploy/.env without sourcing the file.
env_value() {
  local key="$1" line value
  line="$(grep -E "^[[:space:]]*${key}=" "${ENV_FILE}" | tail -n 1 || true)"
  [[ -n "${line}" ]] || return 1
  value="${line#*=}"
  value="${value%$'\r'}"
  value="$(printf '%s' "${value}" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
  value="${value%\"}"
  value="${value#\"}"
  value="${value%\'}"
  value="${value#\'}"
  printf '%s' "${value}"
}

cmd_up() {
  require_env_file
  require_docker
  compose up -d --build
}

cmd_down() {
  require_env_file
  require_docker
  compose down
}

cmd_logs() {
  require_env_file
  require_docker
  compose logs --tail 200 -f "$@"
}

cmd_ps() {
  require_env_file
  require_docker
  compose ps
}

cmd_bootstrap_token() {
  require_env_file
  require_docker
  compose exec state-server state-server bootstrap-token --data /data
}

cmd_verify_audit() {
  require_env_file
  require_docker
  compose exec state-server state-server verify-audit --data /data
}

cmd_smoke() {
  require_env_file
  command -v curl >/dev/null 2>&1 || fail "curl is required but not on PATH"
  local domain url
  domain="$(env_value STATE_DOMAIN || true)"
  [[ -n "${domain}" ]] || fail "STATE_DOMAIN is not set in deploy/.env"
  [[ "${domain}" != "example.com" ]] || fail "STATE_DOMAIN is still example.com, set your real domain in deploy/.env"
  for url in \
    "https://state.${domain}/health/ready" \
    "https://state.${domain}/version" \
    "https://relay.${domain}/health/ready"; do
    if ! curl -fsS --max-time 20 "${url}"; then
      printf '\n' >&2
      fail "smoke check failed for ${url}"
    fi
    printf '\n'
  done
  log "SMOKE_OK"
}

# Encrypted backup through the documented procedure in docs/operations.md.
cmd_backup_encrypted() {
  require_docker
  command -v age >/dev/null 2>&1 || fail "age is required for encrypted backups, install it first (see docs/deploy-vps.md)"
  local recipient_file="${STATE_AGE_RECIPIENT_FILE:-}"
  local generated_recipient=""
  if [[ -z "${recipient_file}" ]]; then
    if [[ -f "${BACKUP_RECIPIENT_FILE}" ]]; then
      recipient_file="${BACKUP_RECIPIENT_FILE}"
    elif [[ -n "${STATE_AGE_RECIPIENT:-}" ]]; then
      generated_recipient="$(mktemp)"
      chmod 0600 "${generated_recipient}"
      printf '%s\n' "${STATE_AGE_RECIPIENT}" > "${generated_recipient}"
      recipient_file="${generated_recipient}"
    else
      fail "no age recipient: set STATE_AGE_RECIPIENT or write the age public key to deploy/secrets/backup-recipient.txt"
    fi
  fi
  local backup_root="${STATE_BACKUP_ROOT:-${BACKUP_DIR}}"
  local archive="" status=0
  mkdir -p "${backup_root}"
  archive="$(STATE_SERVICE_ROOT="${REPO_ROOT}" \
    STATE_BACKUP_ROOT="${backup_root}" \
    STATE_AGE_RECIPIENT_FILE="${recipient_file}" \
    "${DOCUMENTED_BACKUP_SCRIPT}")" || status=$?
  if [[ -n "${generated_recipient}" ]]; then
    rm -f "${generated_recipient}"
  fi
  [[ ${status} -eq 0 ]] || fail "encrypted backup failed, the services were restarted by ops/backup-state.sh"
  log "backup written to ${archive}"
}

# Fallback used only when ops/backup-state.sh is unavailable. Writes unencrypted archives.
cmd_backup_volumes() {
  require_docker
  local stamp volume status=0
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  log "warning: ops/backup-state.sh is missing, writing unencrypted volume archives"
  compose stop state-server state-relay
  for volume in state-server-data state-relay-data; do
    docker run --rm \
      -v "${volume}:/data:ro" \
      -v "${BACKUP_DIR}:/out" \
      "${ALPINE_IMAGE}" \
      tar czf "/out/${volume}-${stamp}.tgz" -C /data . || status=$?
  done
  chmod 0600 "${BACKUP_DIR}"/*.tgz 2>/dev/null || true
  compose start state-server state-relay || log "warning: could not restart the services, run scripts/vps/state.sh up"
  [[ ${status} -eq 0 ]] || fail "volume backup failed"
  log "backup written to ${BACKUP_DIR}"
}

cmd_backup() {
  require_env_file
  mkdir -p "${BACKUP_DIR}"
  if [[ -x "${DOCUMENTED_BACKUP_SCRIPT}" ]]; then
    cmd_backup_encrypted
    return
  fi
  cmd_backup_volumes
}

cmd_update() {
  require_env_file
  require_docker
  command -v git >/dev/null 2>&1 || fail "git is required but not on PATH"
  log "pulling the latest revision"
  git -C "${REPO_ROOT}" pull --ff-only
  cmd_up
  cmd_smoke
}

main() {
  if [[ $# -eq 0 ]]; then
    usage >&2
    exit 2
  fi
  local command="$1"
  shift
  case "${command}" in
  up)
    cmd_up
    ;;
  down)
    cmd_down
    ;;
  logs)
    cmd_logs "$@"
    ;;
  ps)
    cmd_ps
    ;;
  bootstrap-token)
    cmd_bootstrap_token
    ;;
  verify-audit)
    cmd_verify_audit
    ;;
  backup)
    cmd_backup
    ;;
  update)
    cmd_update
    ;;
  smoke)
    cmd_smoke
    ;;
  help | -h | --help)
    usage
    ;;
  *)
    usage >&2
    fail "unknown command '${command}'"
    ;;
  esac
}

main "$@"
