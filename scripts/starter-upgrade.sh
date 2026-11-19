#!/usr/bin/env bash
set -euo pipefail
VERSION="${VERSION:-${1:-}}"
if [ -z "$VERSION" ]; then
	echo "用法：scripts/starter-upgrade.sh <tag> [CONTINUE=1]" >&2
	exit 2
fi
if [ -z "${TARGET_ROOT:-}" ]; then
	echo "TARGET_ROOT 未设置" >&2
	exit 2
fi
project_root="${PROJECT_ROOT:-$PWD}"
cd "$TARGET_ROOT"
args=(-version "$VERSION" -project-root "$project_root" -target-root "$TARGET_ROOT")
if [ -n "${CONTINUE:-}" ]; then
	args+=(-continue)
fi
exec go run ./tools/starter upgrade ${args[@]+"${args[@]}"}
