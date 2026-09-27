#!/usr/bin/env bash
set -Eeuo pipefail

readonly SERVER_HOST="${SERVER_HOST:-91.188.214.231}"
readonly SERVER_USER="${SERVER_USER:-user}"
readonly SSH_KEY="${SSH_KEY:-${HOME}/.ssh/ssh-key-5298}"
readonly REMOTE_DIR="/tmp/day16-bootstrap"
readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly KNOWN_HOSTS_FILE="${KNOWN_HOSTS_FILE:-${HOME}/.ssh/known_hosts}"

if [[ ! -f "${SSH_KEY}" ]]; then
  printf 'SSH key not found: %s\n' "${SSH_KEY}" >&2
  exit 1
fi

chmod 600 "${SSH_KEY}"

ssh_opts=(
  -i "${SSH_KEY}"
  -o BatchMode=yes
  -o ConnectTimeout=10
  -o StrictHostKeyChecking=accept-new
  -o UserKnownHostsFile="${KNOWN_HOSTS_FILE}"
)

printf 'Uploading bootstrap files to %s@%s...\n' "${SERVER_USER}" "${SERVER_HOST}"
ssh "${ssh_opts[@]}" "${SERVER_USER}@${SERVER_HOST}" \
  "rm -rf '${REMOTE_DIR}' && mkdir -m 700 '${REMOTE_DIR}'"

scp "${ssh_opts[@]}" \
  "${SCRIPT_DIR}/server/bootstrap.sh" \
  "${SCRIPT_DIR}/server/verify.sh" \
  "${SERVER_USER}@${SERVER_HOST}:${REMOTE_DIR}/"

printf 'Applying VPS baseline...\n'
ssh "${ssh_opts[@]}" "${SERVER_USER}@${SERVER_HOST}" \
  "sudo bash '${REMOTE_DIR}/bootstrap.sh' '${SERVER_USER}'"

printf 'Running verification...\n'
ssh "${ssh_opts[@]}" "${SERVER_USER}@${SERVER_HOST}" \
  "sudo bash '${REMOTE_DIR}/verify.sh' '${SERVER_USER}'"

printf 'VPS baseline is ready. Start a new SSH session to use Docker without sudo.\n'
