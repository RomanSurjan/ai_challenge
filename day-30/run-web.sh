#!/usr/bin/env bash
set -euo pipefail
DAY30_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$DAY30_DIR/web_server.py" --config "$DAY30_DIR/.runtime/config.json"
