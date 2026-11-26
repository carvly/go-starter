package httpserver

import (
	"net/http"
	"regexp"

	"uuid"
)

// 客户端自报的请求头，约定见 docs/agents/http.md 的"客户端请求头"。
const (
	HeaderClientPlatform = "X-Client-Platform"
	HeaderClientOS       = "X-Client-OS"
	HeaderClientVersion  = "X-Client-Version"
	HeaderInstallationID = "X-Installation-Id"
)

var (
	clientNamePattern    = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
	clientVersionPattern = regexp.MustCompile(`^[0-9A-Za-z._+-]{1,32}$`)
)

// ClientInfo 是客户端自报的端、宿主系统、版本和安装 ID。任何人都能伪造，只能用于日志、展示和统计，
// 不能用于鉴权、限流豁免等安全判断；不进 span 属性（见 docs/agents/observability.md 的"tracing"）。
type ClientInfo struct {
	Platform       string
	OS             string
	Version        string
	InstallationID string
}

// ParseClientInfo 从请求头读取客户端信息，格式不合法的字段置空。
func ParseClientInfo(h http.Header) ClientInfo {
	return ClientInfo{
		Platform:       h.Get(HeaderClientPlatform),
		OS:             h.Get(HeaderClientOS),
		Version:        h.Get(HeaderClientVersion),
		InstallationID: h.Get(HeaderInstallationID),
	}.Sanitized()
}

// Sanitized 只检查格式，不检查取值：合法的端有哪些由使用方决定，新的端不需要改这里。
// platform、os 是小写字母、数字和连字符，version 是常见的版本号字符；安装 ID 是带连字符的 36 位 UUID，大小写都收
// （iOS 的 UUID().uuidString 是大写），统一转成小写，同一台设备在日志里只有一种写法。
func (c ClientInfo) Sanitized() ClientInfo {
	if !clientNamePattern.MatchString(c.Platform) {
		c.Platform = ""
	}
	if !clientNamePattern.MatchString(c.OS) {
		c.OS = ""
	}
	if !clientVersionPattern.MatchString(c.Version) {
		c.Version = ""
	}
	c.InstallationID = normalizeUUID(c.InstallationID)
	return c
}

// normalizeUUID 只接受带连字符的 36 位形式（uuid.Parse 还接受花括号、urn: 前缀和不带连字符的写法），返回小写；
// 不合法时返回空字符串。
func normalizeUUID(s string) string {
	if len(s) != 36 {
		return ""
	}
	u, err := uuid.Parse(s)
	if err != nil {
		return ""
	}
	return u.String()
}
