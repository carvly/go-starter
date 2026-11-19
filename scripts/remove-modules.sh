#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
args=()
while [ "$#" -gt 0 ]; do
	case "$1" in
	--no-record) args+=(-no-record) ;;
	--dry-run) args+=(-dry-run) ;;
	--replay) args+=(-target "$2"); shift ;;
	--) shift; break ;;
	*) break ;;
	esac
	shift
done
if [ "$#" -gt 0 ]; then
	args+=(-removed "$1")
fi
go run ./tools/starter remove ${args[@]+"${args[@]}"}
