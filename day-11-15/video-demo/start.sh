#!/usr/bin/env bash
set -euo pipefail
"$(dirname "$0")/control.sh" start all
DEMO_ROOT="$(</tmp/day11-web-demo-current)"
# shellcheck disable=SC1090
source "$DEMO_ROOT/state.env"
printf 'Main:              http://127.0.0.1:%s\n' "$MAIN_PORT"
printf 'Profile A:         http://127.0.0.1:%s\n' "$PROFILE_A_PORT"
printf 'Profile B:         http://127.0.0.1:%s\n' "$PROFILE_B_PORT"
printf 'Without profile:   http://127.0.0.1:%s\n' "$EMPTY_PROFILE_PORT"
printf 'Corrupt dialog:    http://127.0.0.1:%s\n' "$CORRUPT_DIALOG_PORT"
printf 'Corrupt invariant: http://127.0.0.1:%s\n' "$CORRUPT_INVARIANT_PORT"
