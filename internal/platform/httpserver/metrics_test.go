package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"app.local/internal/config"
	"app.local/internal/platform/httpserver"
	"app.local/internal/platform/metrics"
	"app.local/internal/platform/process"
	"app.local/internal/platform/tracing"
)

// newMetricsServers 按组合根的方式接线：前台带运行时指标，后台只有自己的。
func newMetricsServers(t *testing.T) (api, admin *echo.Echo) {
	t.Helper()
	id := process.New("api", "host-a")
	httpCfg := config.HTTPConfig{BodyLimitBytes: 1 << 20, RequestTimeout: 5 * time.Second, GlobalRateLimitPerSecond: 1000}
	abort := func(g *echo.Group) {
		g.GET("/widgets/abort", func(*echo.Context) error { panic(http.ErrAbortHandler) })
	}
	api, err := httpserver.New(httpserver.Deps{
		Tracer: tracing.Disabled(),
		Logger: discardLogger(), HTTP: httpCfg, Metrics: metrics.NewRuntimeRegistry(id),
		CORS:   config.CORSConfig{AllowOrigins: []string{"https://example.com"}},
		Routes: []func(*echo.Group){testRoutes, abort},
	})
	if err != nil {
		t.Fatal(err)
	}
	admin, err = httpserver.NewAdmin(httpserver.AdminDeps{
		Tracer: tracing.Disabled(),
		Logger: discardLogger(), HTTP: httpCfg, Metrics: metrics.NewRegistry(id), Origin: adminOrigin,
	})
	if err != nil {
		t.Fatal(err)
	}
	return api, admin
}

func metricLines(e *echo.Echo, name string) []string {
	var out []string
	for line := range strings.SplitSeq(get(e, "/metrics").Body.String(), "\n") {
		if strings.HasPrefix(line, name) {
			out = append(out, line)
		}
	}
	return out
}

func hasLine(lines []string, parts ...string) bool {
next:
	for _, l := range lines {
		for _, p := range parts {
			if !strings.Contains(l, p) {
				continue next
			}
		}
		return true
	}
	return false
}

// 被 panic 恢复的请求也要计入，状态码是客户端实际收到的；前台和后台按 server 标签区分。
func TestMetrics_CountsEveryRequestWithFinalStatus(t *testing.T) {
	api, admin := newMetricsServers(t)
	get(api, httpserver.APIPrefix+"/widgets/panic")
	get(admin, "/healthz")
	req := httptest.NewRequest("BREW", httpserver.APIPrefix+"/widgets/1", nil)
	api.ServeHTTP(httptest.NewRecorder(), req)

	lines := metricLines(api, "http_server_request_duration_seconds_count")
	if !hasLine(lines, `server="api"`, `route="`+httpserver.APIPrefix+`/widgets/panic"`, `status="500"`) {
		t.Errorf("panic 的请求应按 500 计入：%v", lines)
	}
	if !hasLine(lines, `method="_OTHER"`) {
		t.Errorf("非标准方法应归为 _OTHER：%v", lines)
	}
	if hasLine(lines, `route="/healthz"`) || hasLine(lines, `route="/metrics"`) {
		t.Errorf("健康检查和 /metrics 不计入：%v", lines)
	}
}

// Recover 原样再抛的 http.ErrAbortHandler 不经过错误处理器，也要计入。
func TestMetrics_CountsAbortedRequests(t *testing.T) {
	api, _ := newMetricsServers(t)
	func() {
		defer func() {
			if r := recover(); r != http.ErrAbortHandler {
				t.Errorf("recover() = %v，ErrAbortHandler 应继续向上抛", r)
			}
		}()
		get(api, httpserver.APIPrefix+"/widgets/abort")
	}()
	lines := metricLines(api, "http_server_request_duration_seconds_count")
	if !hasLine(lines, `route="`+httpserver.APIPrefix+`/widgets/abort"`, `status="500"`) {
		t.Errorf("中断的请求应按 500 计入：%v", lines)
	}
}

// 两个端口各自被抓取，输出的内容不能重叠：都带 service_info 供 join，HTTP 指标只有本端口的，
// 运行时指标只在前台端口。
func TestMetrics_EachPortServesOnlyItsOwnSeries(t *testing.T) {
	api, admin := newMetricsServers(t)
	get(api, httpserver.APIPrefix+"/widgets/1")
	get(admin, "/admin.yaml")

	for name, e := range map[string]*echo.Echo{"api": api, "admin": admin} {
		lines := metricLines(e, "service_info")
		if !hasLine(lines, `service_name="api"`, `service_instance_id="host-a"`, `service_version="`) {
			t.Errorf("%s 的 service_info = %v", name, lines)
		}
	}
	if len(metricLines(api, "go_goroutines")) == 0 {
		t.Error("前台端口缺少 Go 运行时指标")
	}
	if got := metricLines(admin, "go_goroutines"); len(got) != 0 {
		t.Errorf("后台端口不应重复输出运行时指标：%v", got)
	}
	if got := metricLines(admin, "process_"); len(got) != 0 {
		t.Errorf("后台端口不应重复输出进程指标：%v", got)
	}
	adminHTTP := metricLines(admin, "http_server_request_duration_seconds_count")
	if !hasLine(adminHTTP, `server="admin"`) || hasLine(adminHTTP, `server="api"`) {
		t.Errorf("后台端口只输出自己的 HTTP 指标：%v", adminHTTP)
	}
	if hasLine(metricLines(api, "http_server_request_duration_seconds_count"), `server="admin"`) {
		t.Error("前台端口不应输出后台的 HTTP 指标")
	}
}

func TestNewInternal_NilRegistryServesEmptyMetrics(t *testing.T) {
	e := httpserver.NewInternal(discardLogger(), nil)
	if rec := get(e, "/metrics"); rec.Code != http.StatusOK {
		t.Errorf("/metrics = %d", rec.Code)
	}
}

// exemplar 只在 OpenMetrics 格式里输出，客户端要求时必须能拿到这个格式。
func TestMetrics_ServesOpenMetricsWhenAsked(t *testing.T) {
	api, _ := newMetricsServers(t)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Accept", "application/openmetrics-text; version=1.0.0")
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/openmetrics-text") {
		t.Errorf("Content-Type = %q", ct)
	}
}

// opaqueWriter 包装响应却不实现 Unwrap，中间件取不到实际写出的状态码。
type opaqueWriter struct{ http.ResponseWriter }

// 取不到实际状态码时按错误推算，错误请求不能被记成 200。
func TestMetrics_WrappedResponseFallsBackToErrorStatus(t *testing.T) {
	api, _ := newMetricsServers(t)
	api.Pre(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.SetResponse(opaqueWriter{c.Response()})
			return next(c)
		}
	})
	get(api, httpserver.APIPrefix+"/widgets/missing")
	lines := metricLines(api, "http_server_request_duration_seconds_count")
	if !hasLine(lines, `route="`+httpserver.APIPrefix+`/widgets/missing"`, `status="404"`) {
		t.Errorf("应按错误的状态码 404 计入：%v", lines)
	}
}
