#!/usr/bin/env bash
set -euo pipefail

POINTER_FILE="/tmp/day11-web-demo-current"
[[ -f "$POINTER_FILE" ]] || { echo "Сначала выполните prepare.sh" >&2; exit 1; }
DEMO_ROOT="$(<"$POINTER_FILE")"
[[ "$DEMO_ROOT" == /tmp/day11-web-demo.* && -f "$DEMO_ROOT/state.env" ]] || { echo "Некорректное demo-окружение" >&2; exit 1; }
# shellcheck disable=SC1090
source "$DEMO_ROOT/state.env"
TITLE="${1:-Жизненный цикл задачи}"
ID="$(python3 - "$MAIN_MEMORY" "$TITLE" <<'PY'
import json, pathlib, sys
root = pathlib.Path(sys.argv[1])
data = json.loads((root / "dialogs.json").read_text(encoding="utf-8"))
for dialog in data["data"]["dialogs"]:
    if dialog["title"] == sys.argv[2]:
        print(dialog["id"])
        raise SystemExit(0)
raise SystemExit(f"dialog not found: {sys.argv[2]}")
PY
)"
SOURCE="$MAIN_MEMORY/dialogs/$ID/short_term.json"
BACKUP_DIR="$DEMO_ROOT/cleared-short-term"
mkdir -p "$BACKUP_DIR"
if [[ -f "$SOURCE" ]]; then
  mv "$SOURCE" "$BACKUP_DIR/$ID.json"
fi
printf 'Short-term очищена только для demo-диалога «%s»; backup: %s/%s.json\n' "$TITLE" "$BACKUP_DIR" "$ID"
