#!/usr/bin/env bash
set -Eeuo pipefail

readonly SERVER_HOST="${SERVER_HOST:-91.188.214.231}"
readonly SERVER_USER="${SERVER_USER:-user}"
readonly SSH_KEY="${SSH_KEY:-${HOME}/.ssh/ssh-key-5298}"
readonly KNOWN_HOSTS_FILE="${KNOWN_HOSTS_FILE:-${HOME}/.ssh/known_hosts}"

ssh_opts=(-i "${SSH_KEY}" -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=yes -o UserKnownHostsFile="${KNOWN_HOSTS_FILE}")
ssh "${ssh_opts[@]}" "${SERVER_USER}@${SERVER_HOST}" 'set -Eeuo pipefail
  test -d /srv/day17/app && test -d /srv/day17/data && test -d /srv/day17/logs
  test "$(stat -c %a /srv/day17/data/runtime.env)" = 600
  curl --silent --fail http://127.0.0.1:8081/healthz | grep -q "day17-github-mcp"
  curl --silent --fail http://127.0.0.1:8082/healthz
  curl --silent --fail http://127.0.0.1:8080/healthz
  ss -lnt | grep -q "127.0.0.1:8081"
  ss -lnt | grep -q "127.0.0.1:8082"
  ! ss -lnt | grep -q "0.0.0.0:8081"
  ! ss -lnt | grep -q "0.0.0.0:8082"
  docker inspect --format "{{.State.Health.Status}}" day17-mcp-server | grep -qx healthy
  docker inspect --format "{{.State.Health.Status}}" day17-agent-app | grep -qx healthy
  docker inspect --format "{{.State.Running}}" day16-mcp-server | grep -qx true
  curl --silent --show-error --fail \
    -H "Content-Type: application/json" \
    -H "Accept: application/json, text/event-stream" \
    -H "MCP-Protocol-Version: 2025-11-25" \
    --data-binary "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\",\"params\":{}}" \
    http://127.0.0.1:8081/mcp | grep -q "get_github_repository"
  printf "remote verification: PASS\n"'
