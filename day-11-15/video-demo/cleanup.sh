#!/usr/bin/env bash
set -euo pipefail
POINTER_FILE="/tmp/day11-web-demo-current"
[[ -f "$POINTER_FILE" ]] || { echo "Demo-окружение уже удалено."; exit 0; }
DEMO_ROOT="$(<"$POINTER_FILE")"
[[ "$DEMO_ROOT" == /tmp/day11-web-demo.* ]] || { echo "ОТКАЗ: неожиданный путь $DEMO_ROOT" >&2; exit 1; }
"$(dirname "$0")/control.sh" stop all
find "$DEMO_ROOT" -maxdepth 2 -not -path "$DEMO_ROOT/go-cache/*" -print | sort
rm -rf -- "$DEMO_ROOT"
rm -f -- "$POINTER_FILE"
printf 'Удалён только временный каталог: %s\n' "$DEMO_ROOT"
