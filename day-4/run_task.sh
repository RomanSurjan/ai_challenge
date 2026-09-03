#!/usr/bin/env sh
set -eu

if [ "$#" -ne 1 ]; then
  echo "Использование: $0 path/to/task.md" >&2
  exit 1
fi

TASK_FILE=$1

if [ ! -f "$TASK_FILE" ]; then
  echo "Файл не найден: $TASK_FILE" >&2
  exit 1
fi

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(dirname "$SCRIPT_DIR")

cd "$REPO_ROOT"

task_name=$(basename "$TASK_FILE" .md)
task_name=${task_name#task_}
output_file="day-4/answer_${task_name}.md"

echo "Запуск: $TASK_FILE -> $output_file"

go run ./day-4 \
  -task-name "$task_name" \
  -out "$output_file" \
  < "$TASK_FILE"
