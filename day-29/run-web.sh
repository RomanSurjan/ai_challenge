#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
export GOCACHE="${GOCACHE:-$(pwd)/../.gocache}"
exec go run ./cmd/rag-web "$@"
