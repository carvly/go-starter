#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
args=()
if [ -n "${STARTER_VERSION:-}" ]; then
	args=(-target "$STARTER_VERSION")
fi
go run ./tools/starter paths ${args[@]+"${args[@]}"}
