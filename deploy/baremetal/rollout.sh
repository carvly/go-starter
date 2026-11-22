#!/usr/bin/env bash
# 单台应用机、双 api 实例的滚动发布，见 docs/agents/deploy-ci.md 的"裸机双实例发布"。
#
#   deploy/baremetal/rollout.sh v1.2.3              # 发布：迁移 → worker → api@a → api@b
#   deploy/baremetal/rollout.sh --rollback v1.2.2   # 回滚：跳过迁移，其余相同
#
# 前提：/opt/app/releases/<版本>/{api,worker} 已上传。
# 回滚必须带 --rollback：旧二进制执行 migrate up 时数据库已在新版本，旧二进制里没有这个迁移文件，
# 迁移直接失败；expand/contract 保证旧代码能跑在新表结构上，所以跳过迁移即可。
set -euo pipefail

ROLLBACK=false
if [[ ${1:-} == --rollback ]]; then
    ROLLBACK=true
    shift
fi
VERSION=${1:?用法：rollout.sh [--rollback] <版本>}
ROOT=${APP_ROOT:-/opt/app}
READY_TIMEOUT=${READY_TIMEOUT:-60} # 新实例就绪的最长等待，秒
FAIL_TIMEOUT=${FAIL_TIMEOUT:-5}    # 与 OpenResty upstream 的 fail_timeout 一致
WORKER_PORT=9090
RELEASE=$ROOT/releases/$VERSION

log() { printf '[rollout %s] %s\n' "$(date +%T)" "$*"; }
die() {
    log "失败：$*"
    exit 1
}

api_port() { case $1 in a) echo 8080 ;; b) echo 8082 ;; esac }
other_of() { case $1 in a) echo b ;; b) echo a ;; esac }

# 等 127.0.0.1:$1 的路径 $2 返回 200，最多 $3 秒。
wait_ok() {
    local port=$1 path=$2 deadline=$((SECONDS + $3))
    until curl -fsS -o /dev/null --max-time 2 "http://127.0.0.1:$port$path"; do
        ((SECONDS < deadline)) || return 1
        sleep 1
    done
}
wait_ready() { wait_ok "$1" /readyz "$2"; }

[[ -x $RELEASE/api && -x $RELEASE/worker ]] || die "$RELEASE 下缺少 api/worker"

if $ROLLBACK; then
    log "回滚到 $VERSION：跳过迁移，表结构保持新版本"
else
    log "执行迁移（必须向后兼容：expand/contract）"
    systemctl start "app-migrate@$VERSION" || die "迁移失败，实例未改动；看 journalctl -u app-migrate@$VERSION"
fi

# 原子切换：ln -sfn 是先删后建，中间若有实例崩溃被 systemd 拉起会找不到二进制。
ln -sfn "releases/$VERSION" "$ROOT/current.tmp"
mv -T "$ROOT/current.tmp" "$ROOT/current"

# worker 先于 api：新 api 入队的任务必须由新 worker 处理（旧 worker 会忽略事件新增的字段），见 queue.md 的"发布顺序"。
log "重启 worker（优雅停止最多约 25s，期间任务在库里排队）"
systemctl restart app-worker
wait_ok "$WORKER_PORT" /healthz "$READY_TIMEOUT" ||
    die "app-worker 未在 ${READY_TIMEOUT}s 内启动，api 未改动，任务在库里排队；看 journalctl -u app-worker，修复后重跑或 --rollback 旧版本"

for inst in a b; do
    other=$(other_of "$inst")
    wait_ready "$(api_port "$other")" 5 || die "app-api@$other 不健康，重启 app-api@$inst 会中断服务"

    log "重启 app-api@$inst（排空 + 优雅关闭 + 启动 $VERSION）"
    systemctl restart "app-api@$inst"

    if ! wait_ready "$(api_port "$inst")" "$READY_TIMEOUT"; then
        systemctl stop "app-api@$inst" || true
        die "app-api@$inst 未在 ${READY_TIMEOUT}s 内就绪，已停止，app-api@$other 继续服务；看 journalctl -u app-api@$inst"
    fi

    # 等 OpenResty 解除对该实例的失败标记、主动检查（rise=2）恢复它，避免下一步时两个实例同时被视为不可用（no live upstreams）。
    sleep $((FAIL_TIMEOUT + 1))
done

log "完成：$VERSION（发布中删过模块时，确认所有实例都是新版本后执行 api queue cancel-orphans）"
