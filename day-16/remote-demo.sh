#!/usr/bin/env bash
set -Eeuo pipefail

readonly SERVER_HOST="${SERVER_HOST:-91.188.214.231}"
readonly SERVER_USER="${SERVER_USER:-user}"
readonly SSH_KEY="${SSH_KEY:-${HOME}/.ssh/ssh-key-5298}"
readonly LOCAL_PORT="${LOCAL_PORT:-18080}"
readonly KNOWN_HOSTS_FILE="${KNOWN_HOSTS_FILE:-${HOME}/.ssh/known_hosts}"
readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

ssh -i "${SSH_KEY}" \
  -o BatchMode=yes \
  -o ExitOnForwardFailure=yes \
  -o StrictHostKeyChecking=yes \
  -o UserKnownHostsFile="${KNOWN_HOSTS_FILE}" \
  -N -L "${LOCAL_PORT}:127.0.0.1:8080" \
  "${SERVER_USER}@${SERVER_HOST}" &
tunnel_pid=$!
trap 'kill "${tunnel_pid}" 2>/dev/null || true' EXIT

for _ in {1..20}; do
  if curl --silent --fail "http://127.0.0.1:${LOCAL_PORT}/healthz" >/dev/null; then
    break
  fi
  sleep 0.25
done

cd "${SCRIPT_DIR}"
go run ./cmd/client -endpoint "http://127.0.0.1:${LOCAL_PORT}/mcp"

