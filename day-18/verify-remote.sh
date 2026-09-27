#!/usr/bin/env bash
set -Eeuo pipefail

readonly SERVER_HOST="${SERVER_HOST:-91.188.214.231}"
readonly SERVER_USER="${SERVER_USER:-user}"
readonly SSH_KEY="${SSH_KEY:-${HOME}/.ssh/ssh-key-5298}"
readonly KNOWN_HOSTS_FILE="${KNOWN_HOSTS_FILE:-${HOME}/.ssh/known_hosts}"

ssh_opts=(-i "${SSH_KEY}" -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=yes -o UserKnownHostsFile="${KNOWN_HOSTS_FILE}")
ssh "${ssh_opts[@]}" "${SERVER_USER}@${SERVER_HOST}" 'bash -s' <<'REMOTE'
set -Eeuo pipefail

mcp_call() {
  curl --silent --show-error --fail \
    -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' \
    -H 'MCP-Protocol-Version: 2025-11-25' \
    --data-binary "$1" \
    http://127.0.0.1:8083/mcp
}

currency_mcp_call() {
  curl --silent --show-error --fail \
    -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' \
    -H 'MCP-Protocol-Version: 2025-11-25' \
    --data-binary "$1" \
    http://127.0.0.1:8085/mcp
}

cleanup_schedule() {
  if [[ -n "${schedule_id:-}" && "${schedule_id}" != "null" ]]; then
    mcp_call "{\"jsonrpc\":\"2.0\",\"id\":99,\"method\":\"tools/call\",\"params\":{\"name\":\"cancel_repository_monitor\",\"arguments\":{\"schedule_id\":\"${schedule_id}\"}}}" >/dev/null 2>&1 || true
  fi
}
trap cleanup_schedule EXIT

test -d /srv/day18/app
test -d /srv/day18/data
test -d /srv/day18/data/backups
test -d /srv/day18/logs
test "$(stat -c %a /srv/day18/data/runtime.env)" = 600

curl --silent --fail http://127.0.0.1:8080/healthz >/dev/null
curl --silent --fail http://127.0.0.1:8081/healthz | grep -q 'day17-github-mcp'
curl --silent --fail http://127.0.0.1:8082/healthz | grep -q 'day17-agent-app'
curl --silent --fail http://127.0.0.1:8083/healthz | grep -q 'day18-periodic-github-mcp'
curl --silent --fail http://127.0.0.1:8085/healthz | grep -q 'day18-currency-monitor-mcp'
curl --silent --fail http://127.0.0.1:8084/healthz | grep -q 'day18-agent-app'

for port in 8080 8081 8082 8083 8084 8085; do
  ss -lnt | grep -q "127.0.0.1:${port}"
  ! ss -lnt | grep -q "0.0.0.0:${port}"
done

for container in day16-mcp-server day17-mcp-server day17-agent-app day18-github-mcp day18-currency-mcp day18-agent-app; do
  docker inspect --format '{{.State.Running}}' "${container}" | grep -qx true
done
for container in day17-mcp-server day17-agent-app day18-github-mcp day18-currency-mcp day18-agent-app; do
  docker inspect --format '{{.State.Health.Status}}' "${container}" | grep -qx healthy
done
docker exec day18-github-mcp test -f /data/day18.db
docker exec day18-currency-mcp test -f /data/currency.db

tools_json="$(mcp_call '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}')"
for tool in cancel_repository_monitor get_repository_monitor_summary list_repository_monitors schedule_repository_monitor; do
  printf '%s' "${tools_json}" | jq -e --arg tool "${tool}" '.result.tools | any(.name == $tool)' >/dev/null
done
printf '%s' "${tools_json}" | jq -e '.result.tools | length == 4' >/dev/null

currency_tools_json="$(currency_mcp_call '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}')"
for tool in cancel_exchange_rate_monitor convert_currency get_exchange_rate_monitor_summary list_exchange_rate_monitors schedule_exchange_rate_monitor; do
  printf '%s' "${currency_tools_json}" | jq -e --arg tool "${tool}" '.result.tools | any(.name == $tool)' >/dev/null
done
printf '%s' "${currency_tools_json}" | jq -e '.result.tools | length == 5' >/dev/null

conversion_json="$(currency_mcp_call '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"convert_currency","arguments":{"amount":"125.50","base_currency":"USD","quote_currency":"EUR"}}}')"
printf '%s' "${conversion_json}" | jq -e '.result.isError != true and .result.structuredContent.amount == "125.5" and .result.structuredContent.base_currency == "USD" and .result.structuredContent.quote_currency == "EUR" and (.result.structuredContent.converted_amount | length > 0)' >/dev/null

create_json="$(mcp_call '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"schedule_repository_monitor","arguments":{"owner":"modelcontextprotocol","repository":"go-sdk","interval_seconds":60,"max_runs":2}}}')"
schedule_id="$(printf '%s' "${create_json}" | jq -er '.result.structuredContent.schedule_id')"
printf '%s' "${create_json}" | jq -e '.result.isError != true and .result.structuredContent.status == "active"' >/dev/null

summary_json=''
for _ in $(seq 1 20); do
  summary_json="$(mcp_call "{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\",\"params\":{\"name\":\"get_repository_monitor_summary\",\"arguments\":{\"schedule_id\":\"${schedule_id}\"}}}")"
  snapshots="$(printf '%s' "${summary_json}" | jq -r '.result.structuredContent.snapshot_count // 0')"
  [[ "${snapshots}" -ge 2 ]] && break
  sleep 5
done

printf '%s' "${summary_json}" | jq -e '.result.structuredContent.snapshot_count >= 2 and .result.structuredContent.successful_runs >= 2 and .result.structuredContent.insufficient_data == false and (.result.structuredContent.aggregation_window_from != .result.structuredContent.aggregation_window_to) and .result.structuredContent.source == "GitHub REST API"' >/dev/null
window_before="$(printf '%s' "${summary_json}" | jq -c '[.result.structuredContent.aggregation_window_from,.result.structuredContent.aggregation_window_to,.result.structuredContent.snapshot_count]')"

docker restart day18-github-mcp >/dev/null
for _ in $(seq 1 30); do
  curl --silent --fail http://127.0.0.1:8083/healthz >/dev/null 2>&1 && break
  sleep 1
done
curl --silent --fail http://127.0.0.1:8083/healthz >/dev/null
currency_mcp_call '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"list_exchange_rate_monitors","arguments":{}}}' >/dev/null

after_restart="$(mcp_call "{\"jsonrpc\":\"2.0\",\"id\":4,\"method\":\"tools/call\",\"params\":{\"name\":\"get_repository_monitor_summary\",\"arguments\":{\"schedule_id\":\"${schedule_id}\"}}}")"
window_after="$(printf '%s' "${after_restart}" | jq -c '[.result.structuredContent.aggregation_window_from,.result.structuredContent.aggregation_window_to,.result.structuredContent.snapshot_count]')"
[[ "${window_before}" == "${window_after}" ]]

cancel_json="$(mcp_call "{\"jsonrpc\":\"2.0\",\"id\":5,\"method\":\"tools/call\",\"params\":{\"name\":\"cancel_repository_monitor\",\"arguments\":{\"schedule_id\":\"${schedule_id}\"}}}")"
printf '%s' "${cancel_json}" | jq -e '.result.structuredContent.status == "cancelled" and .result.structuredContent.snapshot_count >= 2' >/dev/null
schedule_id=''

curl --silent --fail http://127.0.0.1:8080/healthz >/dev/null
curl --silent --fail http://127.0.0.1:8081/healthz >/dev/null
curl --silent --fail http://127.0.0.1:8082/healthz >/dev/null
printf 'remote verification: PASS\n'
REMOTE
