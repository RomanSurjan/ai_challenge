#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT="/Users/romansurzhan/ai_challenge"
DEMO_DIR="$PROJECT_ROOT/day-11-15/video-demo"
POINTER_FILE="/tmp/day11-web-demo-current"

[[ -f "$POINTER_FILE" ]] || { echo "Сначала выполните prepare.sh" >&2; exit 1; }
DEMO_ROOT="$(<"$POINTER_FILE")"
[[ "$DEMO_ROOT" == /tmp/day11-web-demo.* && -f "$DEMO_ROOT/state.env" ]] || { echo "Некорректное demo-окружение" >&2; exit 1; }
# shellcheck disable=SC1090
source "$DEMO_ROOT/state.env"

target_config() {
  case "$1" in
    main) printf '%s\t%s\n' "$MAIN_MEMORY" "$MAIN_PORT" ;;
    profile-a) printf '%s\t%s\n' "$PROFILE_A_MEMORY" "$PROFILE_A_PORT" ;;
    profile-b) printf '%s\t%s\n' "$PROFILE_B_MEMORY" "$PROFILE_B_PORT" ;;
    empty-profile) printf '%s\t%s\n' "$EMPTY_PROFILE_MEMORY" "$EMPTY_PROFILE_PORT" ;;
    corrupt-dialog) printf '%s\t%s\n' "$CORRUPT_DIALOG_MEMORY" "$CORRUPT_DIALOG_PORT" ;;
    corrupt-invariant) printf '%s\t%s\n' "$CORRUPT_INVARIANT_MEMORY" "$CORRUPT_INVARIANT_PORT" ;;
    *) echo "Неизвестная цель: $1" >&2; return 1 ;;
  esac
}

start_mock() {
  if [[ -f "$DEMO_ROOT/pids/mock.pid" ]] && kill -0 "$(<"$DEMO_ROOT/pids/mock.pid")" 2>/dev/null; then
    return
  fi
  python3 "$DEMO_DIR/mock_server.py" --port "$MOCK_PORT" --log-dir "$DEMO_ROOT/mock-payloads" \
    >"$DEMO_ROOT/mock.log" 2>&1 &
  echo "$!" > "$DEMO_ROOT/pids/mock.pid"
  for _ in 1 2 3 4 5; do
    curl -fsS "http://127.0.0.1:$MOCK_PORT/health" >/dev/null 2>&1 && return
    sleep 1
  done
  echo "Mock transport не запустился" >&2
  exit 1
}

start_app() {
  local target="$1" config memory port pid_file
  config="$(target_config "$target")"
  memory="${config%%$'\t'*}"
  port="${config##*$'\t'}"
  pid_file="$DEMO_ROOT/pids/$target.pid"
  if [[ -f "$pid_file" ]] && kill -0 "$(<"$pid_file")" 2>/dev/null; then
    return
  fi
  DEEPSEEK_API_KEY="day11-local-demo-not-a-secret" \
  DAY11_MEMORY_DIR="$memory" \
    "$DEMO_ROOT/day11-app" -serve -addr "127.0.0.1:$port" -base-url "http://127.0.0.1:$MOCK_PORT" \
    >"$DEMO_ROOT/app-$target.log" 2>&1 &
  echo "$!" > "$pid_file"
  for _ in 1 2 3 4 5; do
    curl -fsS "http://127.0.0.1:$port/api/dialogs" >/dev/null 2>&1 && return
    sleep 1
  done
  echo "Приложение $target не запустилось; см. $DEMO_ROOT/app-$target.log" >&2
  exit 1
}

stop_one() {
  local name="$1" pid_file="$DEMO_ROOT/pids/$1.pid"
  if [[ -f "$pid_file" ]] && kill -0 "$(<"$pid_file")" 2>/dev/null; then
    kill "$(<"$pid_file")"
    wait "$(<"$pid_file")" 2>/dev/null || true
  fi
  rm -f "$pid_file"
}

command="${1:-}"
target="${2:-}"
case "$command" in
  start)
    start_mock
    if [[ "$target" == "all" ]]; then
      for item in main profile-a profile-b empty-profile corrupt-dialog corrupt-invariant; do start_app "$item"; done
    else
      start_app "$target"
    fi
    ;;
  stop)
    if [[ "$target" == "all" ]]; then
      for item in main profile-a profile-b empty-profile corrupt-dialog corrupt-invariant mock; do stop_one "$item"; done
    else
      stop_one "$target"
    fi
    ;;
  restart)
    stop_one "$target"
    start_mock
    start_app "$target"
    ;;
  *)
    echo "Использование: control.sh {start|stop|restart} {main|profile-a|profile-b|empty-profile|corrupt-dialog|corrupt-invariant|all}" >&2
    exit 2
    ;;
esac
