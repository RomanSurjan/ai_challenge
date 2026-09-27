#!/usr/bin/env bash
set -Eeuo pipefail

readonly DEPLOY_USER="${1:-user}"
readonly SSH_DROP_IN="/etc/ssh/sshd_config.d/00-day16-hardening.conf"
readonly FAIL2BAN_JAIL="/etc/fail2ban/jail.d/sshd.local"
readonly DOCKER_CONFIG="/etc/docker/daemon.json"

log() {
  printf '[day16] %s\n' "$*"
}

die() {
  printf '[day16] ERROR: %s\n' "$*" >&2
  exit 1
}

[[ "${EUID}" -eq 0 ]] || die 'run as root'
[[ -r /etc/os-release ]] || die '/etc/os-release is missing'
id "${DEPLOY_USER}" >/dev/null 2>&1 || die "user does not exist: ${DEPLOY_USER}"

. /etc/os-release
log "configuring ${PRETTY_NAME} for deploy user ${DEPLOY_USER}"

apt_update() {
  DEBIAN_FRONTEND=noninteractive apt-get update
}

if ! apt_update; then
  if [[ "${ID:-}" == "ubuntu" ]]; then
    log 'standard repositories are unavailable; switching Ubuntu sources to old-releases'
    timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
    cp -a /etc/apt/sources.list.d/ubuntu.sources \
      "/etc/apt/sources.list.d/ubuntu.sources.${timestamp}.bak" 2>/dev/null || true
    sed -i \
      -e 's|http://archive.ubuntu.com/ubuntu|http://old-releases.ubuntu.com/ubuntu|g' \
      -e 's|http://security.ubuntu.com/ubuntu|http://old-releases.ubuntu.com/ubuntu|g' \
      /etc/apt/sources.list /etc/apt/sources.list.d/*.sources 2>/dev/null || true
    apt_update
  else
    die 'package index update failed'
  fi
fi

log 'installing all available OS updates'
DEBIAN_FRONTEND=noninteractive apt-get full-upgrade -y \
  -o Dpkg::Options::=--force-confold

log 'installing base packages'
DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
  ca-certificates \
  curl \
  docker-compose-v2 \
  docker.io \
  fail2ban \
  git \
  jq \
  openssh-server \
  rsync \
  unattended-upgrades \
  ufw

log 'configuring SSH hardening'
install -d -m 0755 /etc/ssh/sshd_config.d
install -d -m 0755 /run/sshd
cat >"${SSH_DROP_IN}" <<EOF
# Managed by day-16/server/bootstrap.sh
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
X11Forwarding no
MaxAuthTries 3
LoginGraceTime 30
AllowUsers ${DEPLOY_USER}
EOF
chmod 0644 "${SSH_DROP_IN}"
rm -f /etc/ssh/sshd_config.d/99-day16-hardening.conf
sshd -t
systemctl reload ssh

log 'configuring firewall'
ufw default deny incoming
ufw default allow outgoing
ufw limit 22/tcp comment 'SSH rate limit'
ufw --force enable

log 'configuring Fail2ban'
install -d -m 0755 /etc/fail2ban/jail.d
cat >"${FAIL2BAN_JAIL}" <<'EOF'
# Managed by day-16/server/bootstrap.sh
[sshd]
enabled = true
backend = systemd
bantime = 1h
findtime = 10m
maxretry = 5
EOF
chmod 0644 "${FAIL2BAN_JAIL}"
systemctl enable --now fail2ban
fail2ban-client reload

log 'configuring Docker'
install -d -m 0755 /etc/docker
cat >"${DOCKER_CONFIG}" <<'EOF'
{
  "live-restore": true,
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "10m",
    "max-file": "3"
  }
}
EOF
chmod 0644 "${DOCKER_CONFIG}"
usermod -aG docker "${DEPLOY_USER}"
systemctl enable --now docker
systemctl restart docker

log 'creating application directories'
install -d -o "${DEPLOY_USER}" -g "${DEPLOY_USER}" -m 0750 \
  /srv/day16 /srv/day16/app /srv/day16/data /srv/day16/logs

log 'enabling unattended upgrades where repositories provide updates'
cat >/etc/apt/apt.conf.d/60day16-unattended-upgrades <<'EOF'
// Managed by day-16/server/bootstrap.sh
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
APT::Periodic::AutocleanInterval "7";
Unattended-Upgrade::Automatic-Reboot "false";
EOF
chmod 0644 /etc/apt/apt.conf.d/60day16-unattended-upgrades
systemctl enable --now unattended-upgrades

log 'bootstrap completed'
