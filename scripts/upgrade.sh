#!/usr/bin/env bash
# 把 Go 直接依赖和后台前端依赖升到兼容范围内的最新版（不跨大版本），重新生成派生代码，
# 跑检查，最后列出每个升级项的版本变化和 changelog 链接。看 changelog、决定要不要采用新
# 特性是升级的一部分，做法见 docs/agents/deploy-ci.md 的"依赖升级"。
#
# 工具链、Makefile 固定的工具、镜像、跨大版本不在这里处理，见 make outdated。

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
. scripts/lib-deps.sh

LOCK=web/admin/pnpm-lock.yaml
if [ -n "$(git status --porcelain -- go.mod go.sum web/admin/package.json "$LOCK")" ]; then
	echo "go.mod、go.sum、web/admin/package.json 或 $LOCK 有未提交的改动：先提交或放弃，" >&2
	echo "升级的结果才能单独审查和回退" >&2
	exit 1
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "== Go 直接依赖"
go_direct_versions >"$tmp/go-before"
cutoff="$(cooldown_cutoff)"
targets=""
skipped=""
while read -r path _ next released; do
	[ -n "$path" ] || continue
	if [[ "$released" > "$cutoff" ]]; then
		skipped="${skipped}  ${path} ${next}（${released%%T*} 发布，冷却 ${COOLDOWN_DAYS} 天）\n"
	else
		targets="$targets $path@$next"
	fi
done <<<"$(go_direct_updates)"
if [ -n "$targets" ]; then
	# shellcheck disable=SC2086 # 有意按空格拆成多个参数
	go get $targets
	go mod tidy
else
	echo "  没有可升级的"
fi
[ -z "$skipped" ] || printf '  跳过：\n%b' "$skipped"
go_direct_versions >"$tmp/go-after"

echo
echo "== 后台前端依赖（pnpm 按 pnpm-workspace.yaml 的 minimumReleaseAge 跳过刚发布的版本）"
(cd web/admin && pnpm list --depth 0 --json >"$tmp/web-before" && pnpm update && pnpm list --depth 0 --json >"$tmp/web-after")

echo
echo "== 重新生成派生代码（oapi-codegen 升级会改变 api.gen.go）"
go generate ./...

echo
echo "== 检查"
fail=0
go build ./... && go vet ./... && go test ./... || fail=1
(cd web/admin && pnpm typecheck && pnpm lint && pnpm test) || fail=1

echo
echo "== 版本变化：逐个看 changelog，按 docs/agents/deploy-ci.md 的\"依赖升级\"处理"
join "$tmp/go-before" "$tmp/go-after" | while read -r path old new; do
	[ "$old" = "$new" ] && continue
	printf '  %-58s %s -> %s\n    %s\n' "$path" "$old" "$new" "$(changelog_url "$path")"
done
# shellcheck disable=SC2016 # 单引号里是 JS 代码，不需要 shell 展开
node -e '
const [before, after] = process.argv.slice(1).map((f) => JSON.parse(require("fs").readFileSync(f, "utf8"))[0]);
const deps = (p) => ({ ...(p.dependencies ?? {}), ...(p.devDependencies ?? {}) });
const b = deps(before), a = deps(after);
for (const name of Object.keys(a).sort()) {
  const from = b[name]?.version, to = a[name].version;
  if (!from || from === to) continue;
  console.log(`  ${name.padEnd(58)} ${from} -> ${to}\n    https://www.npmjs.com/package/${name}?activeTab=versions`);
}' "$tmp/web-before" "$tmp/web-after"

if [ "$fail" -ne 0 ]; then
	echo
	echo "检查没有通过：先看上面对应版本的 changelog 里的不兼容变更。放弃这次升级：" >&2
	echo "  git checkout -- go.mod go.sum web/admin/package.json $LOCK && make generate && git status" >&2
	exit 1
fi
