#!/usr/bin/env bash
set -Eeuo pipefail

readonly SERVER_HOST="${SERVER_HOST:-91.188.214.231}"
readonly SERVER_USER="${SERVER_USER:-user}"
readonly SSH_KEY="${SSH_KEY:-${HOME}/.ssh/ssh-key-5298}"
readonly REMOTE_DIR="/srv/day16/app"
readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly KNOWN_HOSTS_FILE="${KNOWN_HOSTS_FILE:-${HOME}/.ssh/known_hosts}"

ssh_command="ssh -i ${SSH_KEY} -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=${KNOWN_HOSTS_FILE}"

rsync -az --delete \
  --exclude='.git/' \
  --exclude='artifacts/' \
  --exclude='*.mp4' \
  -e "${ssh_command}" \
  "${SCRIPT_DIR}/" "${SERVER_USER}@${SERVER_HOST}:${REMOTE_DIR}/"

ssh -i "${SSH_KEY}" \
  -o BatchMode=yes \
  -o StrictHostKeyChecking=yes \
  -o UserKnownHostsFile="${KNOWN_HOSTS_FILE}" \
  "${SERVER_USER}@${SERVER_HOST}" \
  "cd '${REMOTE_DIR}' && docker compose up -d --build --remove-orphans && docker compose ps"

