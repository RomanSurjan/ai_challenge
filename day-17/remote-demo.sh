#!/usr/bin/env bash
set -Eeuo pipefail

readonly SERVER_HOST="${SERVER_HOST:-91.188.214.231}"
readonly SERVER_USER="${SERVER_USER:-user}"
readonly SSH_KEY="${SSH_KEY:-${HOME}/.ssh/ssh-key-5298}"
readonly LOCAL_APP_PORT="${LOCAL_APP_PORT:-18082}"
readonly KNOWN_HOSTS_FILE="${KNOWN_HOSTS_FILE:-${HOME}/.ssh/known_hosts}"
readonly PROMPT="${1:-Расскажи по-русски об актуальном состоянии репозитория modelcontextprotocol/go-sdk: назначение, основной язык, ветка по умолчанию, звёзды, форки, открытые issues, лицензия и даты последнего обновления.}"

ssh -i "${SSH_KEY}" -o BatchMode=yes -o ExitOnForwardFailure=yes -o StrictHostKeyChecking=yes \
  -o UserKnownHostsFile="${KNOWN_HOSTS_FILE}" -N -L "${LOCAL_APP_PORT}:127.0.0.1:8082" \
  "${SERVER_USER}@${SERVER_HOST}" &
tunnel_pid=$!
trap 'kill "${tunnel_pid}" 2>/dev/null || true' EXIT

for _ in {1..30}; do
  curl --silent --fail "http://127.0.0.1:${LOCAL_APP_PORT}/healthz" >/dev/null && break
  sleep 0.25
done

curl --silent --show-error --fail \
  -H 'Content-Type: application/json' \
  --data "$(jq -nc --arg message "${PROMPT}" '{message:$message}')" \
  "http://127.0.0.1:${LOCAL_APP_PORT}/api/chat" | jq .
