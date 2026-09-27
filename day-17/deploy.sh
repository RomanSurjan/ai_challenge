#!/usr/bin/env bash
set -Eeuo pipefail

readonly SERVER_HOST="${SERVER_HOST:-91.188.214.231}"
readonly SERVER_USER="${SERVER_USER:-user}"
readonly SSH_KEY="${SSH_KEY:-${HOME}/.ssh/ssh-key-5298}"
readonly REMOTE_ROOT="/srv/day17"
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
  "sudo install -d -o '${SERVER_USER}' -g '${SERVER_USER}' -m 0750 '${REMOTE_ROOT}' '${REMOTE_APP}' '${REMOTE_ROOT}/data' '${REMOTE_ROOT}/logs'"

ssh_command="ssh -i ${SSH_KEY} -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=${KNOWN_HOSTS_FILE}"
rsync -az --delete --exclude='.git/' --exclude='.env' --exclude='artifacts/' --exclude='*.mp4' \
  -e "${ssh_command}" "${SCRIPT_DIR}/" "${SERVER_USER}@${SERVER_HOST}:${REMOTE_APP}/"

{
  grep '^DEEPSEEK_API_KEY=' "${REPO_ENV}"
  grep '^DEEPSEEK_MODEL=' "${REPO_ENV}" || true
  grep '^DEEPSEEK_BASE_URL=' "${REPO_ENV}" || true
  grep '^GITHUB_TOKEN=' "${REPO_ENV}" || true
} | ssh "${ssh_opts[@]}" "${SERVER_USER}@${SERVER_HOST}" \
  "umask 077; cat > '${REMOTE_ROOT}/data/runtime.env'; chmod 600 '${REMOTE_ROOT}/data/runtime.env'"

ssh "${ssh_opts[@]}" "${SERVER_USER}@${SERVER_HOST}" \
  "set -Eeuo pipefail; \
   if docker image inspect day17-agent:local >/dev/null 2>&1; then docker image tag day17-agent:local day17-agent:rollback; fi; \
   cd '${REMOTE_APP}'; \
   docker compose --project-name day17 --env-file '${REMOTE_ROOT}/data/runtime.env' build; \
   docker compose --project-name day17 --env-file '${REMOTE_ROOT}/data/runtime.env' up -d --remove-orphans; \
   docker compose --project-name day17 ps"
