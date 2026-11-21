// Package metrics 构造 Prometheus Registry 和 /metrics handler。每个端口一个 Registry：
// 同一进程的多个端口各自被抓取，内容不能重复，否则按服务求和时会算重。
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"app.local/internal/platform/process"
)

// NewRegistry 只带 service_info：恒为 1，标签与日志的静态字段同名同值。Prometheus 的 job/instance
// 来自抓取配置，查询时 join 它就能按 service_name、service_version 过滤，所以每个端口都要有。
func NewRegistry(id process.Identity) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	info := prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        "service_info",
		Help:        "Identity of this process; always 1.",
		ConstLabels: id.Labels(),
	})
	info.Set(1)
	reg.MustRegister(info)
	return reg
}

// NewRuntimeRegistry 在 NewRegistry 之上带 Go 运行时和进程指标。它们属于进程而不是端口，
// 每个进程只由一个端口输出（api 的前台端口、worker 的内部端口）。
func NewRuntimeRegistry(id process.Identity) *prometheus.Registry {
	reg := NewRegistry(id)
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return reg
}

// Handler 输出 OpenMetrics 格式（客户端在 Accept 里要求时）：exemplar 只在这个格式里出现。
func Handler(g prometheus.Gatherer) http.Handler {
	return promhttp.HandlerFor(g, promhttp.HandlerOpts{EnableOpenMetrics: true})
}
