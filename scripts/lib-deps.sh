# shellcheck shell=bash
# outdated.sh 和 upgrade.sh 共用的函数，由它们 source，不单独执行。
# 兼容 macOS 自带的 bash 3.2：不用关联数组、mapfile。

# 和 dependabot 的 cooldown 一致：刚发布的版本先不升，对冲被投毒后几天内撤回的 release。
COOLDOWN_DAYS="${COOLDOWN_DAYS:-3}"

cooldown_cutoff() {
	date -u -v-"${COOLDOWN_DAYS}"d +%Y-%m-%dT%H:%M:%SZ 2>/dev/null ||
		date -u -d "${COOLDOWN_DAYS} days ago" +%Y-%m-%dT%H:%M:%SZ
}

# 每行：模块路径 当前版本 可升到的版本 发布时间（UTC，RFC 3339）。只看直接依赖（含 tool 指令的模块）。
go_direct_updates() {
	go list -m -u -f '{{if and (not .Main) (not .Indirect) .Update}}{{.Path}} {{.Version}} {{.Update.Version}} {{.Update.Time.UTC.Format "2006-01-02T15:04:05Z"}}{{end}}' all
}

# 每行：模块路径 版本。用来对比升级前后。
go_direct_versions() {
	go list -m -f '{{if and (not .Main) (not .Indirect)}}{{.Path}} {{.Version}}{{end}}' all
}

changelog_url() {
	case "$1" in
	github.com/*) echo "https://github.com/$(echo "$1" | cut -d/ -f2-3)/releases" ;;
	*) echo "https://pkg.go.dev/$1?tab=versions" ;;
	esac
}

pinned_tool() {
	local name="$1" mod="$2" cur="$3" latest
	latest="$(go list -m -f '{{.Version}} ({{.Time.UTC.Format "2006-01-02"}})' "$mod@latest" 2>/dev/null || echo "（查询失败）")"
	if [ "${latest%% *}" = "$cur" ]; then
		printf '  %-15s %s 已是最新\n' "$name" "$cur"
		return
	fi
	printf '  %-15s %s -> %s\n' "$name" "$cur" "$latest"
	printf '    %s\n' "$(changelog_url "$mod")"
}
