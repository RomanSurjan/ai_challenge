#!/usr/bin/env bash
set -Eeuo pipefail

readonly SERVER_HOST="${SERVER_HOST:-91.188.214.231}"
readonly SERVER_USER="${SERVER_USER:-user}"
readonly SSH_KEY="${SSH_KEY:-${HOME}/.ssh/ssh-key-5298}"
readonly REMOTE_ROOT="/srv/day18"
readonly REMOTE_APP="${REMOTE_ROOT}/app"
readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_ENV="${REPO_ENV:-${SCRIPT_DIR}/../.env}"
readonly KNOWN_HOSTS_FILE="${KNOWN_HOSTS_FILE:-${HOME}/.ssh/known_hosts}"

[[ -f "${SSH_KEY}" ]] || { printf 'SSH key not found: %s\n' "${SSH_KEY}" >&2; exit 1; }
[[ "$(stat -f '%Lp' "${SSH_KEY}" 2>/dev/null || stat -c '%a' "${SSH_KEY}")" == "600" ]] || { printf 'SSH key must have mode 600\n' >&2; exit 1; }
[[ -f "${REPO_ENV}" ]] || { printf 'Environment file not found: %s\n' "${REPO_ENV}" >&2; exit 1; }
grep -q '^DEEPSEEK_API_KEY=.' "${REPO_ENV}" || { printf 'DEEPSEEK_API_KEY is missing in %s\n' "${REPO_ENV}" >&2; exit 1; }

ssh_opts=(-i "${SSH_KEY}" -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=yes -o UserKnownHostsFile="${KNOWN_HOSTS_FILE}")

ssh "${ssh_opts[@]}" "${SERVER_USER}@${SERVER_HOST}" \
  "sudo install -d -o '${SERVER_USER}' -g '${SERVER_USER}' -m 0750 '${REMOTE_ROOT}' '${REMOTE_APP}' '${REMOTE_ROOT}/data' '${REMOTE_ROOT}/data/backups' '${REMOTE_ROOT}/logs'"

ssh_command="ssh -i ${SSH_KEY} -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=${KNOWN_HOSTS_FILE}"
rsync -az --exclude='.git/' --exclude='.env' --exclude='artifacts/' --exclude='*.mp4' --exclude='*.db*' \
  -e "${ssh_command}" "${SCRIPT_DIR}/" "${SERVER_USER}@${SERVER_HOST}:${REMOTE_APP}/"

{
  grep '^DEEPSEEK_API_KEY=' "${REPO_ENV}"
  grep '^DEEPSEEK_MODEL=' "${REPO_ENV}" || true
  grep '^DEEPSEEK_BASE_URL=' "${REPO_ENV}" || true
  grep '^GITHUB_TOKEN=' "${REPO_ENV}" || true
} | ssh "${ssh_opts[@]}" "${SERVER_USER}@${SERVER_HOST}" \
  "umask 077; cat > '${REMOTE_ROOT}/data/runtime.env'; chmod 600 '${REMOTE_ROOT}/data/runtime.env'"

ssh "${ssh_opts[@]}" "${SERVER_USER}@${SERVER_HOST}" bash -s -- "${REMOTE_APP}" "${REMOTE_ROOT}" <<'REMOTE'
set -Eeuo pipefail
remote_app="$1"
remote_root="$2"
if docker image inspect day18-agent:local >/dev/null 2>&1; then
  docker image tag day18-agent:local day18-agent:rollback
fi
cd "${remote_app}"
docker compose --project-name day18 --env-file "${remote_root}/data/runtime.env" build
if docker volume inspect day18-data >/dev/null 2>&1 && \
   docker run --rm --entrypoint /bin/sh -v day18-data:/source:ro day18-agent:local -c 'test -f /source/day18.db'; then
  docker stop day18-github-mcp >/dev/null 2>&1 || true
  backup_stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  docker run --rm --user "$(id -u):$(id -g)" --entrypoint /bin/sh \
    -v day18-data:/source:ro -v "${remote_root}/data/backups:/backup" day18-agent:local \
    -c "cp /source/day18.db /backup/day18-${backup_stamp}.db"
  printf 'SQLite backup: %s/data/backups/day18-%s.db\n' "${remote_root}" "${backup_stamp}"
fi
if docker volume inspect day18-data >/dev/null 2>&1 && \
   docker run --rm --entrypoint /bin/sh -v day18-data:/source:ro day18-agent:local -c 'test -f /source/currency.db'; then
  docker stop day18-currency-mcp >/dev/null 2>&1 || true
  backup_stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  docker run --rm --user "$(id -u):$(id -g)" --entrypoint /bin/sh \
    -v day18-data:/source:ro -v "${remote_root}/data/backups:/backup" day18-agent:local \
    -c "cp /source/currency.db /backup/currency-${backup_stamp}.db"
  printf 'SQLite backup: %s/data/backups/currency-%s.db\n' "${remote_root}" "${backup_stamp}"
fi
docker compose --project-name day18 --env-file "${remote_root}/data/runtime.env" up -d --remove-orphans
docker compose --project-name day18 ps
REMOTE
