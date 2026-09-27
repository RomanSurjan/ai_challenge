#!/usr/bin/env bash
set -Eeuo pipefail

readonly DEPLOY_USER="${1:-user}"
failures=0

pass() {
  printf 'PASS  %s\n' "$*"
}

fail() {
  printf 'FAIL  %s\n' "$*" >&2
  failures=$((failures + 1))
}

check_command() {
  local command_name="$1"
  command -v "${command_name}" >/dev/null 2>&1 \
    && pass "command available: ${command_name}" \
    || fail "command missing: ${command_name}"
}

check_service() {
  local service_name="$1"
  systemctl is-active --quiet "${service_name}" \
    && pass "service active: ${service_name}" \
    || fail "service inactive: ${service_name}"
}

[[ "${EUID}" -eq 0 ]] || { printf 'Run as root.\n' >&2; exit 1; }

check_command docker
check_command fail2ban-client
check_command ufw
check_service ssh
check_service docker
check_service fail2ban

sshd -t && pass 'SSH configuration is valid' || fail 'SSH configuration is invalid'

ssh_effective="$(sshd -T)"
grep -q '^permitrootlogin no$' <<<"${ssh_effective}" \
  && pass 'root SSH login disabled' || fail 'root SSH login is not disabled'
grep -q '^passwordauthentication no$' <<<"${ssh_effective}" \
  && pass 'password SSH login disabled' || fail 'password SSH login is not disabled'

ufw status | grep -q '^Status: active' \
  && pass 'UFW active' || fail 'UFW inactive'
ufw status | grep -Eq '^22/tcp[[:space:]]+LIMIT' \
  && pass 'SSH firewall rate limit active' || fail 'SSH firewall rule missing'

id -nG "${DEPLOY_USER}" | tr ' ' '\n' | grep -qx docker \
  && pass "${DEPLOY_USER} belongs to docker group" \
  || fail "${DEPLOY_USER} does not belong to docker group"

for directory in /srv/day16/app /srv/day16/data /srv/day16/logs; do
  [[ -d "${directory}" ]] && pass "directory exists: ${directory}" \
    || fail "directory missing: ${directory}"
done

if (( failures > 0 )); then
  printf '\n%d verification check(s) failed.\n' "${failures}" >&2
  exit 1
fi

printf '\nAll VPS baseline checks passed.\n'

