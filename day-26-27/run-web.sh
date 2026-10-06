#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENDPOINT="http://127.0.0.1:11434"
HOST="127.0.0.1"
PORT="${WEB_PORT:-8080}"
MODEL="${OLLAMA_MODEL:-qwen2.5:3b}"
STARTED_OLLAMA_PID=""
OLLAMA_LOG=""

cleanup() {
  if [[ -n "$STARTED_OLLAMA_PID" ]] && kill -0 "$STARTED_OLLAMA_PID" 2>/dev/null; then
    echo
    echo "Останавливаем только Ollama, запущенный этим скриптом (PID $STARTED_OLLAMA_PID)…"
    kill "$STARTED_OLLAMA_PID"
    wait "$STARTED_OLLAMA_PID" 2>/dev/null || true
  fi
  if [[ -n "$OLLAMA_LOG" && -f "$OLLAMA_LOG" ]]; then
    rm -f "$OLLAMA_LOG"
  fi
}
trap cleanup EXIT INT TERM

fail() {
  echo "ОШИБКА: $*" >&2
  exit 1
}

command -v python3 >/dev/null 2>&1 || fail "Python 3 не найден."
command -v curl >/dev/null 2>&1 || fail "curl не найден."

if command -v ollama >/dev/null 2>&1; then
  OLLAMA_BIN="$(command -v ollama)"
elif [[ -x /opt/homebrew/bin/ollama ]]; then
  OLLAMA_BIN=/opt/homebrew/bin/ollama
else
  fail "Ollama не найден. Установите Ollama и повторите запуск."
fi

api_ready() {
  curl --silent --show-error --fail --max-time 3 "$ENDPOINT/api/version" >/dev/null
}

echo "============================================================"
echo "День 26–27 — локальный веб-чат"
echo "Ollama: $ENDPOINT"
echo "Модель: $MODEL"
echo "============================================================"

if api_ready; then
  echo "Ollama уже работает; второй сервер не запускается."
else
  echo "Ollama не отвечает; запускаем локальный ollama serve…"
  OLLAMA_LOG="$(mktemp "${TMPDIR:-/tmp}/day-26-27-ollama.XXXXXX")"
  OLLAMA_HOST=127.0.0.1:11434 \
    "$OLLAMA_BIN" serve >"$OLLAMA_LOG" 2>&1 &
  STARTED_OLLAMA_PID=$!
  for _ in {1..30}; do
    if api_ready; then
      break
    fi
    if ! kill -0 "$STARTED_OLLAMA_PID" 2>/dev/null; then
      cat "$OLLAMA_LOG" >&2
      fail "Ollama завершился до готовности."
    fi
    sleep 1
  done
  api_ready || fail "Ollama не запустился за 30 секунд."
  echo "Ollama запущен этим скриптом (PID $STARTED_OLLAMA_PID)."
fi

if ! python3 - "$ENDPOINT/api/tags" "$MODEL" <<'PY'
import json
import sys
import urllib.request

url, expected = sys.argv[1:]
try:
    with urllib.request.urlopen(url, timeout=5) as response:
        payload = json.load(response)
except Exception as exc:
    raise SystemExit(f"Не удалось получить список моделей: {exc}")

names = []
for item in payload.get("models", []):
    if isinstance(item, dict):
        name = item.get("name") or item.get("model")
        if isinstance(name, str):
            names.append(name)
if expected not in names:
    print(f"Модель {expected!r} не установлена.", file=sys.stderr)
    print(f"Установленные модели: {', '.join(names) or '(нет)'}", file=sys.stderr)
    print(f"Выполните: ollama pull {expected}", file=sys.stderr)
    raise SystemExit(1)
PY
then
  fail "Выбранная модель недоступна."
fi

echo "Модель найдена."
echo
echo "Веб-чат: http://$HOST:$PORT"
echo "Откройте этот адрес в браузере. Для остановки нажмите Ctrl+C."
echo

OLLAMA_MODEL="$MODEL" WEB_PORT="$PORT" \
  python3 "$SCRIPT_DIR/web_server.py" --host "$HOST" --port "$PORT" --model "$MODEL"
