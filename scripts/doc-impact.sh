#!/usr/bin/env bash
# 列出可能被这次改动影响的文档，提醒逐篇核对。只提示、不失败：文档是否真的要改由人判断，
# 但每篇都要看过。check-docs.sh 只能发现引用了不存在的路径和标识符，内容过时（数字、行为、
# 漏写的新东西）要靠这里把该看的文档找出来。
#
# 一篇文档算受影响，满足其一即可：
#   - 改动落在它的"事实来源："那一行列出的路径下；
#   - 它引用了改动的文件路径；
#   - 它引用了这次删掉的导出函数或类型名。
#
# 用法：scripts/doc-impact.sh            和 main 的分叉点比较（含未提交、未跟踪的改动）
#       scripts/doc-impact.sh <ref>      和指定的 ref 比较
#       scripts/doc-impact.sh --staged   只看暂存区（提交钩子用）

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [ "${1:-}" = "--staged" ]; then
	changed="$(git diff --cached --name-only)"
	diff_args=(--cached)
else
	base="${1:-$(git merge-base HEAD main 2>/dev/null || echo HEAD)}"
	changed="$( (git diff --no-ext-diff --name-only "$base" && git ls-files --others --exclude-standard) | sort -u)"
	diff_args=("$base")
fi

code="$(echo "$changed" | grep -vE '\.md$|^docs/' || true)"
if [ -z "$code" ]; then
	exit 0
fi

# 这次删掉、且改动后已不存在的导出函数和类型。只看驼峰复合名（NewJWTMiddleware），
# Message 这类单词和正文里的普通用词分不开。
removed=""
while IFS= read -r name; do
	[ -n "$name" ] || continue
	if ! git grep --untracked -qE "^(func (\([^)]*\) )?|type )$name([^A-Za-z0-9_]|\$)" -- '*.go'; then
		removed="$removed $name"
	fi
done < <(git diff --no-ext-diff --no-color -U0 "${diff_args[@]}" -- '*.go' |
	sed -nE 's/^-(func (\([^)]*\) )?|type )([A-Z][A-Za-z0-9_]*).*/\3/p' |
	grep -E '^[A-Z][a-z0-9_]*[A-Z]' | sort -u)

docs=(AGENTS.md README.md docs/ARCHITECTURE.md)
while IFS= read -r f; do docs+=("$f"); done < <(ls docs/agents/*.md docs/diagrams/*.html internal/modules/*/docs/*.md 2>/dev/null)

untouched=""
touched=""
for doc in "${docs[@]}"; do
	reasons=""
	# shellcheck disable=SC2016 # 匹配反引号本身
	sources="$(sed -n 's/^事实来源：//p' "$doc" | grep -oE '`[^`]+`' | tr -d '`' || true)"
	for src in $sources; do
		hit="$(echo "$code" | while IFS= read -r c; do if [[ "$c" == $src* ]]; then echo "$c"; fi; done | head -3 | tr '\n' ' ')"
		if [ -n "$hit" ]; then reasons="$reasons\n    事实来源 $src 有改动：$hit"; fi
	done
	while IFS= read -r c; do
		if [ -n "$c" ] && grep -qF -- "$c" "$doc"; then reasons="$reasons\n    引用了改动的文件 $c"; fi
	done <<<"$code"
	for name in $removed; do
		if grep -qw -- "$name" "$doc"; then reasons="$reasons\n    引用了这次删掉的 $name"; fi
	done
	[ -n "$reasons" ] || continue
	if echo "$changed" | grep -qxF -- "$doc"; then
		touched="$touched\n  $doc$reasons"
	else
		untouched="$untouched\n  $doc$reasons"
	fi
done

if [ -n "$untouched$touched" ]; then
	echo "文档影响：逐篇对照代码核对，需要改的在同一个改动里改（见 docs/agents/workflow.md 的\"文档\"）。"
	[ -z "$untouched" ] || printf '这次没改、需要核对：%b\n' "$untouched"
	[ -z "$touched" ] || printf '这次改过、确认已覆盖：%b\n' "$touched"
fi
