// Package buildinfo 提供构建版本，用于日志的 version 字段和启动日志。
package buildinfo

import "runtime/debug"

// version 由构建时注入：go build -ldflags "-X app.local/internal/platform/buildinfo.version=v1.2.3"。
var version string

// Version 依次取：构建时注入的版本、Go 嵌入的 VCS 提交（前 12 位，有未提交改动时加 -dirty）、"dev"。
func Version() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	return fromSettings(info.Settings)
}

func fromSettings(settings []debug.BuildSetting) string {
	var rev string
	var dirty bool
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}
