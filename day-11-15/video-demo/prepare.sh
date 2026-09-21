#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT="/Users/romansurzhan/ai_challenge"
DAY11_ROOT="$PROJECT_ROOT/day-11-15"
POINTER_FILE="/tmp/day11-web-demo-current"

if [[ -e "$POINTER_FILE" ]]; then
  printf 'ОТКАЗ: уже существует %s. Сначала выполните cleanup.sh.\n' "$POINTER_FILE" >&2
  exit 1
fi

DEMO_ROOT="$(mktemp -d /tmp/day11-web-demo.XXXXXX)"
MAIN_MEMORY="$DEMO_ROOT/main-memory"
PROFILE_A_MEMORY="$DEMO_ROOT/profile-a-memory"
PROFILE_B_MEMORY="$DEMO_ROOT/profile-b-memory"
EMPTY_PROFILE_MEMORY="$DEMO_ROOT/empty-profile-memory"
CORRUPT_DIALOG_MEMORY="$DEMO_ROOT/corrupt-dialog-memory"
CORRUPT_INVARIANT_MEMORY="$DEMO_ROOT/corrupt-invariant-memory"
mkdir -p "$MAIN_MEMORY" "$PROFILE_A_MEMORY" "$PROFILE_B_MEMORY" \
  "$EMPTY_PROFILE_MEMORY" "$CORRUPT_DIALOG_MEMORY" "$CORRUPT_INVARIANT_MEMORY" \
  "$DEMO_ROOT/mock-payloads" "$DEMO_ROOT/pids"

GOCACHE="$DEMO_ROOT/go-cache" go build -o "$DEMO_ROOT/day11-app" "$DAY11_ROOT"

add_dialog() {
  local memory="$1" title="$2"
  "$DEMO_ROOT/day11-app" -memory-dir "$memory" -dialog-new -dialog-title "$title" >/dev/null
}

dialog_id_by_title() {
  python3 - "$1" "$2" <<'PY'
import json, pathlib, sys
data = json.loads((pathlib.Path(sys.argv[1]) / "dialogs.json").read_text(encoding="utf-8"))
for dialog in data["data"]["dialogs"]:
    if dialog["title"] == sys.argv[2]:
        print(dialog["id"])
        raise SystemExit(0)
raise SystemExit(f"dialog not found: {sys.argv[2]}")
PY
}

for title in \
  "Проект A" "Проект B" "Ошибки модели" "Проверка инструментов" \
  "Жизненный цикл задачи" "Вторая задача" "Инварианты" \
  "FSM Planning" "FSM Execution" "FSM Validation" "FSM Done" "FSM Tool Guards"; do
  add_dialog "$MAIN_MEMORY" "$title"
done

add_dialog "$PROFILE_A_MEMORY" "Профиль A — основной"
add_dialog "$PROFILE_A_MEMORY" "Профиль A — второй диалог"
add_dialog "$PROFILE_B_MEMORY" "Профиль B"
add_dialog "$EMPTY_PROFILE_MEMORY" "Без профиля"

add_dialog "$CORRUPT_DIALOG_MEMORY" "Повреждённый диалог"
CORRUPT_DIALOG_ID="$(dialog_id_by_title "$CORRUPT_DIALOG_MEMORY" "Повреждённый диалог")"
mkdir -p "$CORRUPT_DIALOG_MEMORY/dialogs/$CORRUPT_DIALOG_ID"
printf '{broken-json\n' > "$CORRUPT_DIALOG_MEMORY/dialogs/$CORRUPT_DIALOG_ID/short_term.json"
add_dialog "$CORRUPT_DIALOG_MEMORY" "Исправный диалог"

"$DEMO_ROOT/day11-app" -memory-dir "$CORRUPT_INVARIANT_MEMORY" -dialog-list >/dev/null
printf '{broken-json\n' > "$CORRUPT_INVARIANT_MEMORY/invariants.json"

python3 - "$MAIN_MEMORY/invariants.json" <<'PY'
import json, pathlib, sys
path = pathlib.Path(sys.argv[1])
data = {
  "version": 1,
  "data": {"invariants": [
    {"id":"architecture-no-global-state","category":"architecture",
     "description":"Глобальное изменяемое состояние запрещено",
     "alternative":"Используйте внедрение зависимостей.",
     "rule":{"type":"forbidden_terms","terms":["global mutable state"],"targets":["request","response"]}},
    {"id":"decision-postgresql","category":"technical_decision",
     "description":"MongoDB не входит в принятое техническое решение",
     "alternative":"Используйте PostgreSQL.",
     "rule":{"type":"forbidden_terms","terms":["MongoDB"],"targets":["response"]}},
    {"id":"stack-kotlin","category":"stack",
     "description":"Java запрещена выбранным стеком",
     "alternative":"Используйте Kotlin.",
     "rule":{"type":"forbidden_terms","terms":["Java"],"targets":["request","response"]}},
    {"id":"business-moscow","category":"business_rule",
     "description":"Доставка за пределы Москвы запрещена",
     "alternative":"Предложите доставку в Москве.",
     "rule":{"type":"forbidden_terms","terms":["доставка в Казань"],"targets":["request","response"]}}
  ]},
  "updated_at": "2026-09-19T00:00:00Z"
}
path.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
PY
cp "$MAIN_MEMORY/invariants.json" "$DEMO_ROOT/invariants.good.json"

cat > "$DEMO_ROOT/state.env" <<EOF
DEMO_ROOT=$DEMO_ROOT
MAIN_MEMORY=$MAIN_MEMORY
PROFILE_A_MEMORY=$PROFILE_A_MEMORY
PROFILE_B_MEMORY=$PROFILE_B_MEMORY
EMPTY_PROFILE_MEMORY=$EMPTY_PROFILE_MEMORY
CORRUPT_DIALOG_MEMORY=$CORRUPT_DIALOG_MEMORY
CORRUPT_INVARIANT_MEMORY=$CORRUPT_INVARIANT_MEMORY
MOCK_PORT=18111
MAIN_PORT=18110
PROFILE_A_PORT=18112
PROFILE_B_PORT=18113
EMPTY_PROFILE_PORT=18114
CORRUPT_DIALOG_PORT=18115
CORRUPT_INVARIANT_PORT=18116
EOF
printf '%s\n' "$DEMO_ROOT" > "$POINTER_FILE"

printf 'Подготовлено изолированное окружение: %s\n' "$DEMO_ROOT"
printf 'Production memory не использована: %s\n' "$DAY11_ROOT/memory"
printf 'Следующий шаг: %s/start.sh\n' "$DAY11_ROOT/video-demo"
