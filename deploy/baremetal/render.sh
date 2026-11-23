#!/usr/bin/env bash
# 按项目名、端口和域名渲染 deploy/baremetal 的模板，同一台机器可以部署多个项目：单元名、运行用户、目录、锁、日志标识、
# 网关 upstream、Alloy 组件都由项目名派生，端口和域名由参数给出，互不冲突。见 docs/agents/deploy-ci.md 的"裸机发布"。
#
#   render.sh --name myshop --api-port 8080 --worker-port 9090 --api-domain api.myshop.com \
#             [--admin-port 8081 --admin-domain admin.myshop.com] [--root DIR] [--conf-dir DIR] [--out DIR]
#
# --root 是部署目录（releases/、current、bin/），默认 /opt/<项目>；--conf-dir 放 app.env，默认 /etc/<项目>。
# 不启用后台（没有后台路由，如删除了 staff 模块）时省略 --admin-port、--admin-domain，后台相关的行全部去掉。
# 全机一份、不随项目渲染的文件：openresty/http.conf、openresty/snippets/proxy.conf、alloy/host.alloy、sysctl/。
set -euo pipefail

die() {
    echo "render.sh：$*" >&2
    exit 1
}

NAME='' API_PORT='' ADMIN_PORT='' WORKER_PORT='' API_DOMAIN='' ADMIN_DOMAIN='' ROOT='' CONF_DIR='' OUT=''
while (($#)); do
    case $1 in
    --name) NAME=${2:-} ;;
    --api-port) API_PORT=${2:-} ;;
    --admin-port) ADMIN_PORT=${2:-} ;;
    --worker-port) WORKER_PORT=${2:-} ;;
    --api-domain) API_DOMAIN=${2:-} ;;
    --admin-domain) ADMIN_DOMAIN=${2:-} ;;
    --root) ROOT=${2:-} ;;
    --conf-dir) CONF_DIR=${2:-} ;;
    --out) OUT=${2:-} ;;
    *) die "未知参数 $1" ;;
    esac
    shift 2 || die "$1 缺少值"
done

valid_port() { [[ $1 =~ ^[0-9]+$ ]] && (($1 > 0 && $1 < 65536)); }
valid_domain() { [[ $1 =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$ ]]; }
valid_dir() { [[ $1 =~ ^(/[A-Za-z0-9._-]+)+$ ]]; } # 绝对路径、不以 / 结尾、不含空白和 sed 的分隔符

# 校验过的值只含字母数字、._-/，可以安全地放进 sed 的替换串（路径用 | 作分隔符）。项目名同时是 Linux 用户名、systemd 单元名前缀和目录名。
[[ $NAME =~ ^[a-z][a-z0-9-]{0,30}[a-z0-9]$ ]] || die "--name 必须是小写字母开头、只含小写字母数字和 -，2~32 个字符：$NAME"
valid_port "$API_PORT" || die "--api-port 必须是 1~65535 的端口：$API_PORT"
valid_port "$WORKER_PORT" || die "--worker-port 必须是 1~65535 的端口：$WORKER_PORT"
valid_domain "$API_DOMAIN" || die "--api-domain 必须是域名：$API_DOMAIN"
ADMIN=false
if [[ -n $ADMIN_PORT || -n $ADMIN_DOMAIN ]]; then
    valid_port "$ADMIN_PORT" || die "--admin-port 必须是 1~65535 的端口：$ADMIN_PORT"
    valid_domain "$ADMIN_DOMAIN" || die "启用后台时 --admin-domain 必须是域名：$ADMIN_DOMAIN"
    ADMIN=true
fi
ROOT=${ROOT:-/opt/$NAME}
CONF_DIR=${CONF_DIR:-/etc/$NAME}
valid_dir "$ROOT" || die "--root 必须是绝对路径、不以 / 结尾，只含字母数字和 ._-：$ROOT"
valid_dir "$CONF_DIR" || die "--conf-dir 必须是绝对路径、不以 / 结尾，只含字母数字和 ._-：$CONF_DIR"
ports=("$API_PORT" "$WORKER_PORT")
$ADMIN && ports+=("$ADMIN_PORT")
[[ $(printf '%s\n' "${ports[@]}" | sort -u | wc -l) -eq ${#ports[@]} ]] || die "端口不能重复：${ports[*]}"

SRC=$(cd "$(dirname "$0")" && pwd)
OUT=${OUT:-./rendered-$NAME}
[[ ! -e $OUT ]] || die "$OUT 已存在，换一个 --out 或先删掉"
mkdir -p "$OUT"/{systemd,openresty,alloy,bin,etc}

# render <模板> <目标>：替换占位符；以 "@admin " 开头的行在启用后台时去掉前缀，否则整行删除。
render() {
    local admin_rule='/^[[:space:]]*@admin /d'
    $ADMIN && admin_rule='s/^\([[:space:]]*\)@admin /\1/'
    sed -e "$admin_rule" \
        -e "s/{{name}}/$NAME/g" -e "s/{{name_id}}/${NAME//-/_}/g" \
        -e "s/{{api_port}}/$API_PORT/g" -e "s/{{admin_port}}/$ADMIN_PORT/g" -e "s/{{worker_port}}/$WORKER_PORT/g" \
        -e "s/{{api_domain}}/$API_DOMAIN/g" -e "s/{{admin_domain}}/$ADMIN_DOMAIN/g" \
        -e "s|{{root}}|$ROOT|g" -e "s|{{conf_dir}}|$CONF_DIR|g" \
        "$SRC/$1" >"$2"
}

render systemd/api.socket "$OUT/systemd/$NAME-api.socket"
render systemd/api@.service "$OUT/systemd/$NAME-api@.service"
render systemd/worker.service "$OUT/systemd/$NAME-worker.service"
render systemd/migrate@.service "$OUT/systemd/$NAME-migrate@.service"
render openresty/project.conf "$OUT/openresty/$NAME.conf"
if $ADMIN; then
    render openresty/admin.conf "$OUT/openresty/admin.tmp"
    cat "$OUT/openresty/admin.tmp" >>"$OUT/openresty/$NAME.conf"
    rm "$OUT/openresty/admin.tmp"
fi
render alloy/project.alloy "$OUT/alloy/$NAME.alloy"
render rollout.sh "$OUT/bin/rollout.sh"
render app-cmd "$OUT/bin/app-cmd"
render app.env.example "$OUT/etc/app.env.example"
chmod 755 "$OUT/bin/rollout.sh" "$OUT/bin/app-cmd"

if left=$(grep -rn -e '{{' -e '@admin ' "$OUT"); then
    die "渲染结果里还有未替换的占位符：$left"
fi

cat <<MSG
已生成到 ${OUT}。安装（root）：
  useradd -r -s /sbin/nologin -M $NAME
  install -d $ROOT/releases $ROOT/bin $CONF_DIR
  cp $OUT/systemd/* /etc/systemd/system/ && systemctl daemon-reload
  install -m 755 $OUT/bin/* $ROOT/bin/
  cp $OUT/etc/app.env.example $CONF_DIR/app.env   # 补全后：chown root:${NAME}，chmod 640
  cp $OUT/openresty/$NAME.conf /etc/openresty/conf.d/  # 全机一份的 http.conf、snippets/proxy.conf 已就位时，openresty -t 后 reload
  cp $OUT/alloy/$NAME.alloy /etc/alloy/                # 与全机一份的 host.alloy 同目录，重启或 reload Alloy
首次部署和之后的发布见 docs/agents/deploy-ci.md 的"裸机发布"。
MSG
