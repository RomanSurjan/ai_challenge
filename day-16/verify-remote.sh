#!/usr/bin/env bash
set -Eeuo pipefail

readonly SERVER_HOST="${SERVER_HOST:-91.188.214.231}"
readonly SERVER_USER="${SERVER_USER:-user}"
readonly SSH_KEY="${SSH_KEY:-${HOME}/.ssh/ssh-key-5298}"
readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly KNOWN_HOSTS_FILE="${KNOWN_HOSTS_FILE:-${HOME}/.ssh/known_hosts}"

ssh_opts=(
  -i "${SSH_KEY}"
  -o BatchMode=yes
  -o ConnectTimeout=10
  -o StrictHostKeyChecking=yes
  -o UserKnownHostsFile="${KNOWN_HOSTS_FILE}"
)

scp "${ssh_opts[@]}" "${SCRIPT_DIR}/server/verify.sh" \
  "${SERVER_USER}@${SERVER_HOST}:/tmp/day16-verify.sh"
ssh "${ssh_opts[@]}" "${SERVER_USER}@${SERVER_HOST}" \
  "sudo bash /tmp/day16-verify.sh '${SERVER_USER}'"

