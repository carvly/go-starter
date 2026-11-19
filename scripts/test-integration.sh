#!/usr/bin/env bash
# 跑集成测试（go test -tags=integration），按本机的容器引擎准备 testcontainers 需要的环境：
#
#   - 已设置 DOCKER_HOST：原样使用；
#   - docker 命令可用（不是 podman-docker）：testcontainers 不读 docker context，当前
#     context 的地址不是默认的 /var/run/docker.sock 时（socktainer、colima 等），把它
#     设成 DOCKER_HOST；Linux 上的默认 context 什么都不改，和直接 go test 一样；
#   - 否则用 Podman（包括 podman-docker 提供的 docker 兼容命令）：DOCKER_HOST 指向
#     Podman 的 API socket（没启动就启动 systemd 用户单元 podman.socket），并设置
#     TESTCONTAINERS_RYUK_CONTAINER_PRIVILEGED=true——rootless Podman 下 ryuk 不加特权
#     起不来；不用 TESTCONTAINERS_RYUK_DISABLED，没有 ryuk 测试容器和匿名卷不会被清理。
#
# socktainer（macOS 上把 Apple Container 包装成 Docker API）：同一次 go test 里的各个包
# 共用一个 testcontainers 会话和 ryuk，在 socktainer 下会撞名或被上一个包的 ryuk 清掉
# 容器。所以带 //go:build integration 的包每个单独跑一次 go test，其余的包合在一次里跑。
# pgtest 的容器靠 ryuk 清理，不能关掉 ryuk。
#
# 参数传给 go test，默认 ./...。以 ./、../ 开头的参数或单独的 . 当作包，其余当作 go test
# 的参数（带值的写成 -run=X 这种形式）。CI 用的是 Docker，直接跑 go test，不经过这个脚本。
#
# 兼容 macOS 原装的 bash 3.2 和 Linux，见 docs/agents/workflow.md 的"脚本"。

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

is_docker_cli() {
	command -v docker >/dev/null 2>&1 || return 1
	docker --version 2>/dev/null | grep -qi podman && return 1
	docker info >/dev/null 2>&1
}

if [ -n "${DOCKER_HOST:-}" ]; then
	echo "test-integration: 使用已设置的 DOCKER_HOST=${DOCKER_HOST}" >&2
elif is_docker_cli; then
	endpoint="$(docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null || true)"
	if [ -n "$endpoint" ] && [ "$endpoint" != "unix:///var/run/docker.sock" ]; then
		export DOCKER_HOST="$endpoint"
		echo "test-integration: 使用 docker context 的地址 DOCKER_HOST=${DOCKER_HOST}" >&2
	fi
elif command -v podman >/dev/null 2>&1; then
	sock="$(podman info --format '{{.Host.RemoteSocket.Path}}')"
	# 只看文件存在不够：podman.socket 停掉后 socket 文件可能还在，但连不上。
	if ! podman --url "unix://$sock" version >/dev/null 2>&1; then
		if [ "$(podman info --format '{{.Host.Security.Rootless}}')" != true ]; then
			echo "test-integration: 连不上 Podman socket ${sock}，先执行 sudo systemctl start podman.socket" >&2
			exit 1
		fi
		echo "test-integration: 启动 podman.socket（${sock}）" >&2
		systemctl --user start podman.socket
	fi
	export DOCKER_HOST="unix://$sock"
	export TESTCONTAINERS_RYUK_CONTAINER_PRIVILEGED="${TESTCONTAINERS_RYUK_CONTAINER_PRIVILEGED:-true}"
	echo "test-integration: 使用 Podman，DOCKER_HOST=${DOCKER_HOST}" >&2
else
	echo "test-integration: 需要 Docker、Podman 或 socktainer" >&2
	exit 1
fi

if [ "$#" -eq 0 ]; then
	set -- ./...
fi

platform="$(docker version --format '{{.Server.Platform.Name}}' 2>/dev/null || true)"
if [ "$platform" != "socktainer" ]; then
	exec go test -tags=integration "$@"
fi

flags=()
patterns=()
for a in "$@"; do
	case "$a" in
	. | ./* | ../*) patterns+=("$a") ;;
	*) flags+=("$a") ;;
	esac
done
[ "${#patterns[@]}" -gt 0 ] || patterns=(./...)

# 先存进变量：写在 < <(...) 里，go list 失败（包写错）会被吞掉，变成零个包"通过"。
list="$(go list -tags=integration -f '{{.ImportPath}} {{.Dir}}' "${patterns[@]}")"
shared=()
alone=()
while read -r pkg dir; do
	if grep -lq '^//go:build integration' "$dir"/*_test.go 2>/dev/null; then
		alone+=("$pkg")
	else
		shared+=("$pkg")
	fi
done <<<"$list"

echo "test-integration: socktainer 下 ${#alone[@]} 个用容器的包逐个运行" >&2
failed=()
if [ "${#shared[@]}" -gt 0 ]; then
	go test -tags=integration ${flags[@]+"${flags[@]}"} "${shared[@]}" || failed+=("（不用容器的包）")
fi
for pkg in ${alone[@]+"${alone[@]}"}; do
	go test -tags=integration ${flags[@]+"${flags[@]}"} "$pkg" || failed+=("$pkg")
done

if [ "${#failed[@]}" -gt 0 ]; then
	echo "test-integration: 失败的包：" >&2
	printf '  %s\n' "${failed[@]}" >&2
	exit 1
fi
