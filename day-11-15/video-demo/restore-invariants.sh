#!/usr/bin/env bash
set -euo pipefail

POINTER_FILE="/tmp/day11-web-demo-current"
[[ -f "$POINTER_FILE" ]] || { echo "Сначала выполните prepare.sh" >&2; exit 1; }
DEMO_ROOT="$(<"$POINTER_FILE")"
[[ "$DEMO_ROOT" == /tmp/day11-web-demo.* && -f "$DEMO_ROOT/state.env" ]] || { echo "Некорректное demo-окружение" >&2; exit 1; }
# shellcheck disable=SC1090
source "$DEMO_ROOT/state.env"
cp "$DEMO_ROOT/invariants.good.json" "$CORRUPT_INVARIANT_MEMORY/invariants.json"
"$(dirname "$0")/control.sh" restart corrupt-invariant
printf 'Доверенный demo-файл восстановлен; приложение перезапущено на http://127.0.0.1:%s\n' "$CORRUPT_INVARIANT_PORT"
