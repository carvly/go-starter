#!/usr/bin/env bash
# 契约固定的升级入口：只负责取得目标版本并执行目标工具。
set -euo pipefail
VERSION="${1:-}"
CONTINUE="${2:-}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
if [ -z "$VERSION" ]; then
	echo "用法：make starter-upgrade VERSION=v0.1.0 [CONTINUE=1]" >&2
	exit 2
fi

git -C "$ROOT" fetch starter --tags
TMP="$(mktemp -d)"
trap 'git -C "$ROOT" worktree remove --force "$TMP/target" 2>/dev/null || true; rm -rf "$TMP"' EXIT
git -C "$ROOT" worktree add --detach "$TMP/target" "$VERSION"
PROJECT_ROOT="$ROOT" TARGET_ROOT="$TMP/target" VERSION="$VERSION" CONTINUE="$CONTINUE" "$TMP/target/scripts/starter-upgrade.sh"
