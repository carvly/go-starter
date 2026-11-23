#!/usr/bin/env bash
# {{name}} 的发布，由 deploy/baremetal/render.sh 生成，见 docs/agents/deploy-ci.md 的"裸机发布"。api 实例 {{name}}-api@1、@2
# 共享 {{name}}-api.socket，平时只有一个在线，发布时轮流接管：启动备用实例（等 READY）→ 观察 → 停在线实例。
# 适用于 systemd 219 及以上。
#
#   rollout.sh v1.2.3              迁移 → 切 current → worker → api
#   rollout.sh --rollback v1.2.2   跳过迁移；切 current → api → worker（与发布相反）
#   rollout.sh --restart           不换版本，零排队地换一个 api 进程
#
# 前提：{{root}}/releases/<版本>/{api,worker} 已上传。在 tmux/screen 里执行：ssh 断开会中断脚本，而已提交给 systemd 的
# 操作会继续，留下需要人工确认的状态（重跑脚本会先收敛实例状态）。
set -euo pipefail

MODE=deploy
case ${1:-} in
--rollback)
    MODE=rollback
    shift
    ;;
--restart) MODE=restart ;;
esac
NAME='{{name}}'
ROOT=${APP_ROOT:-'{{root}}'}
WORKER_PORT='{{worker_port}}'
READY_TIMEOUT=${READY_TIMEOUT:-60}    # worker 就绪的最长等待，秒（api 的上限是单元的 TimeoutStartSec）
OBSERVE_SECONDS=${OBSERVE_SECONDS:-5} # 新 api 承接真实流量、旧 api 仍在服务的观察期，秒
LOCK=${ROLLOUT_LOCK:-/run/$NAME-rollout.lock}
ENV_FILE=${APP_ENV_FILE:-'{{conf_dir}}/app.env'}
PREV_FILE=$ROOT/.rollout-prev # 发布前的版本；中断后重跑时以它为准，发布或回滚成功、或退回后删除
# 单元里用 Environment= 设定的键：EnvironmentFile= 的值会覆盖 Environment=（与书写顺序无关），出现在 app.env 里会悄悄改掉
# 部署形态（例如 HTTP_SOCKET_ACTIVATION=false 让实例自己去绑已被 systemd 占用的端口，HTTP_SHUTDOWN_DRAIN=0s 关掉排空）。
UNIT_KEYS='APP_ENV|HTTP_TRUSTED_PROXY|HTTP_SOCKET_ACTIVATION|HTTP_SHUTDOWN_DRAIN|INSTANCE_ID|HTTP_INTERNAL_ADDR'

log() { printf '[rollout %s] %s\n' "$(date +%T)" "$*"; }
die() {
    log "失败：$*"
    exit 1
}
API=$NAME-api # 实例单元 $API@1、$API@2，socket 单元 $API.socket，日志标识 $API-1、$API-2
running() { systemctl is-active --quiet "$API@$1"; }
main_pid() { systemctl show -p MainPID "$API@$1" | cut -d= -f2; } # 219 没有 --value

switch_current() {
    # 原子切换：ln -sfn 是先删后建，中间若有实例被拉起会找不到二进制。
    ln -sfn "$1" "$ROOT/current.tmp"
    mv -T "$ROOT/current.tmp" "$ROOT/current"
}

restart_worker() {
    log "重启 worker（优雅停止最多约 25s，期间任务在库里排队）"
    systemctl restart "$NAME-worker"
    local deadline=$((SECONDS + READY_TIMEOUT))
    until curl -fs -o /dev/null --max-time 2 "http://127.0.0.1:$WORKER_PORT/healthz"; do
        ((SECONDS < deadline)) || return 1
        sleep 1
    done
}

# 新版本失败时把 current（发布时还有 worker）切回原版本：否则在线的旧 api 一旦崩溃、开机或被 socket 兜底拉起，
# 跑的就是失败的版本。
revert() {
    [[ $MODE != restart && -n $PREV ]] || return 0
    switch_current "$PREV"
    rm -f "$PREV_FILE"
    log "已把 current 切回 $PREV"
    if [[ $MODE == deploy ]]; then
        if restart_worker; then
            log "worker 已切回 $PREV"
        else
            log "警告：worker 切回 $PREV 后未就绪，看 journalctl -t $NAME-worker"
        fi
    fi
}

# 同一时间只允许一个发布。
exec 9>"$LOCK"
flock -n 9 || die "另一个 rollout.sh 正在执行"

systemctl is-active --quiet "$API.socket" || die "$API.socket 未运行，先按首次部署启动"
[[ -r $ENV_FILE ]] || die "读不到 ${ENV_FILE}：设置 APP_ENV_FILE 指向单元 EnvironmentFile= 的那个文件"
if bad=$(grep -oE "^($UNIT_KEYS)=" "$ENV_FILE" | tr -d '=' | paste -sd ' ') && [[ -n $bad ]]; then
    die "$ENV_FILE 里不能设置 ${bad}：这些键由 systemd 单元的 Environment= 设定，写在 app.env 里会覆盖单元的值"
fi
if [[ -f $PREV_FILE ]]; then
    PREV=$(cat "$PREV_FILE")
    log "上次发布中断过：以中断前的版本 $PREV 作为失败时的退回目标"
else
    PREV=$(readlink "$ROOT/current" || true)
fi

if [[ $MODE != restart ]]; then
    VERSION=${1:?用法：rollout.sh [--rollback] <版本> | --restart}
    TARGET=releases/$VERSION
    [[ -x $ROOT/$TARGET/api && -x $ROOT/$TARGET/worker ]] || die "$ROOT/$TARGET 下缺少 api/worker"

    if [[ $MODE == deploy ]]; then
        log "执行迁移（必须向后兼容：expand/contract）"
        systemctl start "$NAME-migrate@$VERSION" || die "迁移失败，实例未改动；看 journalctl -t $NAME-migrate"
    else
        # 旧二进制里没有新迁移文件，执行 migrate up 会直接失败；expand/contract 保证旧代码能跑在新表结构上。
        log "回滚到 ${VERSION}：跳过迁移"
    fi
    [[ -f $PREV_FILE || -z $PREV ]] || echo "$PREV" >"$PREV_FILE"
    switch_current "$TARGET"

    # 发布时 worker 先于 api：新 api 入队的任务必须由新 worker 处理（queue.md 的"发布顺序"）。
    if [[ $MODE == deploy ]] && ! restart_worker; then
        revert
        die "新 worker 未在 ${READY_TIMEOUT}s 内就绪，api 未改动；看 journalctl -t $NAME-worker"
    fi
fi

# 收敛实例状态：停掉 failed、崩溃循环中的实例；socket 兜底拉起 1 之后 2 又被 Restart= 拉起时可能两个都在跑，停掉 2。
for i in 1 2; do
    running "$i" || systemctl stop "$API@$i" 2>/dev/null || true
done
if running 1 && running 2; then
    log "1、2 都在运行，先停 2"
    systemctl stop "$API@2"
fi
if running 1; then
    cur=1 next=2
elif running 2; then
    cur=2 next=1
else
    log "没有在线实例：启动 $API@1，启动期间请求在内核队列里等待"
    cur='' next=1
fi

fail_api() {
    systemctl stop "$API@$next" || true # Restart=on-failure 会在后台继续重试，必须显式停掉
    revert
    die "$1${cur:+，$API@$cur 继续服务}；看 journalctl -t $API-$next"
}

# Type=notify：阻塞到新实例依赖检查通过、发出 READY；失败或超过 TimeoutStartSec 返回非 0。
log "启动 $API@${next}，等待 READY"
systemctl start "$API@$next" || fail_api "$API@$next 启动失败"

# 观察期：两个实例共享 accept 队列，新实例已承接真实流量，旧实例仍在服务。退出或被 Restart= 重新拉起（PID 变化，
# 219 没有 NRestarts）都判失败。
if [[ -n $cur ]]; then
    log "观察 ${OBSERVE_SECONDS}s：新旧实例共同服务"
    pid=$(main_pid "$next")
    for ((s = 0; s < OBSERVE_SECONDS; s++)); do
        sleep 1
        if ! running "$next" || [[ $(main_pid "$next") != "$pid" ]]; then
            fail_api "$API@$next 在观察期内退出或被重启"
        fi
    done
fi
systemctl enable --quiet "$API@$next"

if [[ -n $cur ]]; then
    log "停止 $API@${cur}（立即不再接新连接；已建立的连接排空、做完）"
    systemctl stop "$API@$cur"
    systemctl disable --quiet "$API@$cur"
fi

# 回滚时 api 先于 worker：仍在线的新 api 入队的任务不能交给旧 worker（会丢掉新字段或不认识新任务类型）。
if [[ $MODE == rollback ]]; then
    restart_worker || die "api 已回滚，worker 未在 ${READY_TIMEOUT}s 内就绪；看 journalctl -t $NAME-worker"
fi
# --restart 不切换版本：中断留下的退回目标要留给之后的发布。
[[ $MODE == restart ]] || rm -f "$PREV_FILE"
log "完成：$API@$next 在线${VERSION:+，版本 ${VERSION}（发布中删过模块时，确认后执行 ${ROOT}/bin/app-cmd queue cancel-orphans）}"
