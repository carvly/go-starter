#!/usr/bin/env bash
# 同一个版本号出现在多个文件里时，以一处为准校验其余各处，避免升级时漏改。
# 找不到应有的版本号、或者写成了 latest，同样算失败：匹配不到就跳过等于没检查。

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# extract 多在 $(...)、< <(...) 的子 shell 里调用，子 shell 里改变量传不回来，失败记在文件里。
failed="$(mktemp)"
trap 'rm -f "$failed"' EXIT
err() {
	echo "$1" >&2
	echo x >>"$failed"
}

# extract 文件 sed正则：输出所有匹配到的版本号，一个都没有就报错
extract() {
	local out
	out="$(sed -nE "$2" "$1")"
	if [ -z "$out" ]; then
		err "$1: 找不到应有的版本号（期望匹配 $2）"
	fi
	printf '%s' "$out"
}

# read_vs：把标准输入逐行读进数组 vs。不用 mapfile：macOS 自带的 bash 3.2 没有它。
# extract 的输出末尾没有换行，最后一行靠 [ -n "$l" ] 读进来。
read_vs() {
	vs=()
	local l
	while IFS= read -r l || [ -n "$l" ]; do
		vs+=("$l")
	done
}

expect() { # 名称 基准值 位置 实际值...
	local name="$1" want="$2" where="$3"
	shift 3
	for got in "$@"; do
		[ "$got" = "$want" ] || err "$where: $name 版本是 '$got'，应与基准 '$want' 一致"
	done
}

# 不允许浮动版本
if grep -nE '^[^#]*(@latest|version:[[:space:]]*latest)' Makefile .github/workflows/*.yml lefthook.yml; then
	err "上面这些地方用了 latest：必须固定版本，否则 CI 会因为上游发版而变色"
fi

# Go：以 go.mod 的 major.minor 为准
go_ver="$(extract go.mod 's/^go ([0-9]+\.[0-9]+).*/\1/p')"
for wf in .github/workflows/*.yml; do
	lines="$(grep -c 'go-version:' "$wf" || true)"
	[ "$lines" = 0 ] && continue
	read_vs < <(sed -nE "s/.*go-version:[[:space:]]*[\"']?([0-9]+\.[0-9]+)[\"']?[[:space:]]*$/\1/p" "$wf")
	[ "${#vs[@]}" = "$lines" ] || err "$wf: 有 go-version 写法无法识别（应为 \"1.27\" 这种 major.minor）"
	expect Go "$go_ver" "$wf" ${vs[@]+"${vs[@]}"}
done
expect Go "$go_ver" deploy/Dockerfile "$(extract deploy/Dockerfile 's/^FROM golang:([0-9]+\.[0-9]+)[-.].*/\1/p')"

# govulncheck、golangci-lint：以 Makefile 为准
vuln_ver="$(extract Makefile 's/.*govulncheck@(v[0-9.]+).*/\1/p' | head -1)"
read_vs < <(extract .github/workflows/ci.yml 's/.*govulncheck@(v[0-9.]+).*/\1/p')
expect govulncheck "$vuln_ver" .github/workflows/ci.yml ${vs[@]+"${vs[@]}"}

lint_ver="$(extract Makefile 's/^GOLANGCI_LINT_VERSION[[:space:]]*:?=[[:space:]]*(v[0-9.]+).*/\1/p')"
read_vs < <(extract .github/workflows/ci.yml 's/^[[:space:]]*version:[[:space:]]*(v[0-9.]+).*/\1/p')
expect golangci-lint "$lint_ver" .github/workflows/ci.yml ${vs[@]+"${vs[@]}"}
# 生成器的夹具测试在生成出来的模块上跑同一个 golangci-lint。
expect golangci-lint "$lint_ver" tools/gen/fixture_test.go \
	"$(extract tools/gen/fixture_test.go 's/.*golangci-lint@(v[0-9.]+)".*/\1/p')"

# Postgres 镜像：以 compose 为准，集成测试用同一个
pg_img="$(extract deploy/docker-compose.yml 's/.*image:[[:space:]]*(postgres:[^[:space:]]+).*/\1/p' | head -1)"
expect Postgres "$pg_img" internal/platform/postgres/pgtest/pgtest.go \
	"$(extract internal/platform/postgres/pgtest/pgtest.go 's/^const image = "(postgres:[^"]+)".*/\1/p')"

# 后台前端。Node：以 web/admin/package.json 的 engines 主版本为准
web=web/admin/package.json
node_ver="$(extract "$web" 's/.*"node":[[:space:]]*">=([0-9]+)\..*/\1/p')"
for wf in .github/workflows/*.yml; do
	lines="$(grep -c 'node-version:' "$wf" || true)"
	[ "$lines" = 0 ] && continue
	read_vs < <(sed -nE "s/.*node-version:[[:space:]]*[\"']?([0-9]+)[\"']?[[:space:]]*$/\1/p" "$wf")
	[ "${#vs[@]}" = "$lines" ] || err "$wf: 有 node-version 写法无法识别（应为 \"24\" 这种主版本号）"
	expect Node "$node_ver" "$wf" ${vs[@]+"${vs[@]}"}
done
expect Node "$node_ver" deploy/Dockerfile "$(extract deploy/Dockerfile 's/^FROM node:([0-9]+)[-.].*/\1/p')"

# pnpm：以 packageManager 为准（CI 的 pnpm/action-setup 直接读它）
pnpm_ver="$(extract "$web" 's/.*"packageManager":[[:space:]]*"pnpm@([0-9]+\.[0-9]+\.[0-9]+)".*/\1/p')"
expect pnpm "$pnpm_ver" deploy/Dockerfile "$(extract deploy/Dockerfile 's/.*npm install -g pnpm@([^[:space:]]+).*/\1/p')"

# 客户端生成器在 TS 5 下单独运行（pnpm dlx），版本只写在生成脚本里，必须是确切版本
for pkg in OPENAPI_TYPESCRIPT TYPESCRIPT; do
	extract web/admin/scripts/gen-api.ts "s/^const $pkg = \"([0-9]+\.[0-9]+\.[0-9]+)\";.*/\1/p" >/dev/null
done

[ ! -s "$failed" ]
