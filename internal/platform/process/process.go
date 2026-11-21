// Package process 标识当前进程：哪个服务、哪个版本、哪个实例。日志的静态字段和指标的 service_info
// 都从 Identity 取，名字和值只有这一份。
package process

import (
	"log/slog"

	"app.local/internal/platform/buildinfo"
)

// 键名是 OpenTelemetry 资源属性（service.name、service.version、service.instance.id）把点换成下划线：
// Loki 接收 OTLP 时用的就是这组名字，Grafana 默认按 service_name 关联日志、trace 和指标。
// 不用 instance：Prometheus 的 instance 标签是抓取地址 host:port，含义不同。
const (
	keyServiceName       = "service_name"
	keyServiceVersion    = "service_version"
	keyServiceInstanceID = "service_instance_id"
)

type Identity struct {
	// Name 是服务名：api、worker。
	Name    string
	Version string
	// InstanceID 区分同一服务的多个实例（配置 INSTANCE_ID，默认主机名）。
	InstanceID string
}

// New 的版本取自 buildinfo.Version。
func New(name, instanceID string) Identity {
	return Identity{Name: name, Version: buildinfo.Version(), InstanceID: instanceID}
}

// LogAttrs 是每条日志都带的静态字段，传给 logger.New。
func (id Identity) LogAttrs() []slog.Attr {
	return []slog.Attr{
		slog.String(keyServiceName, id.Name),
		slog.String(keyServiceVersion, id.Version),
		slog.String(keyServiceInstanceID, id.InstanceID),
	}
}

// Labels 是同一组字段作为指标标签的形式。
func (id Identity) Labels() map[string]string {
	return map[string]string{
		keyServiceName:       id.Name,
		keyServiceVersion:    id.Version,
		keyServiceInstanceID: id.InstanceID,
	}
}
