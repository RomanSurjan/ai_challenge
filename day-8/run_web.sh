#!/usr/bin/env sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(dirname "$SCRIPT_DIR")

cd "$REPO_ROOT"

if [ "$#" -eq 0 ]; then
  set -- -addr "${DAY8_ADDR:-:8080}"
elif [ "$#" -eq 1 ]; then
  case "$1" in
    -*) ;;
    *) set -- -addr "$1" ;;
  esac
fi

go run ./day-8 -serve "$@"
