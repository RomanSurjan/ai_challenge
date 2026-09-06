#!/usr/bin/env sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(dirname "$SCRIPT_DIR")

cd "$REPO_ROOT"

go run ./day-5 \
  -task-name model_comparison \
  -out day-5/answer_model_comparison.md \
  < day-5/task.md
