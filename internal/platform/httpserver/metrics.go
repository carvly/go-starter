package httpserver

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/prometheus/client_golang/prometheus"
)

// 标签名与日志字段同名（method、route、status），按同一个名字既能查指标也能查日志。
var httpLabels = []string{"method", "route", "status"}

// newMetricsMiddleware 记录每个请求的耗时分布（直方图的 _count 就是请求数）。server 作为常量标签区分
// 前台和后台：两者各用自己的 Registry，但查询时常把多个端口的数据放在一起看。
//
// 放在中间件链最外层：被限流、请求体超限、panic 的请求也要计入；此时路由已经匹配，c.Path() 是路由模板。
// route 只用模板，没匹配上路由时为空：ID 和扫描器乱扫的路径不能成为标签值。
//
// 返回的错误在这里交给 HTTPErrorHandler 渲染：耗时要包含写出错误响应的时间，状态码取实际写出的。
// Recover 原样再抛的 panic（http.ErrAbortHandler）没有响应，按 500 计入后继续抛。
func newMetricsMiddleware(reg prometheus.Registerer, server string) (echo.MiddlewareFunc, error) {
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:        "http_server_request_duration_seconds",
		Help:        "Duration of HTTP server requests.",
		Buckets:     prometheus.DefBuckets,
		ConstLabels: prometheus.Labels{"server": server},
	}, httpLabels)
	if err := reg.Register(duration); err != nil {
		return nil, err
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if healthEndpointSkipper(c) {
				return next(c)
			}
			start := time.Now()
			observe := func(status int) {
				duration.WithLabelValues(methodName(c.Request().Method), c.Path(), strconv.Itoa(status)).
					Observe(time.Since(start).Seconds())
			}
			defer func() {
				if r := recover(); r != nil {
					observe(http.StatusInternalServerError)
					panic(r)
				}
			}()
			err := next(c)
			if err != nil {
				c.Echo().HTTPErrorHandler(c, err)
			}
			observe(responseStatus(c, err))
			return nil
		}
	}, nil
}

// responseStatus 是客户端收到的状态码，取实际写出的。取不到时（响应被包装且没有 Unwrap）按错误推算，
// 不能把错误请求记成 200；没有错误、handler 什么都没写时 net/http 回 200。
func responseStatus(c *echo.Context, err error) int {
	if r, uErr := echo.UnwrapResponse(c.Response()); uErr == nil && r.Committed {
		return r.Status
	}
	if err != nil {
		return StatusOf(err)
	}
	return http.StatusOK
}

// methodName 把非标准方法归成 _OTHER（与 OpenTelemetry 的约定相同）：方法名来自客户端，不能任意增加取值。
// 日志字段和指标标签都用它，同名同值。
func methodName(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return m
	default:
		return "_OTHER"
	}
}
