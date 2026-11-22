package httpserver_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"app.local/internal/config"
	"app.local/internal/platform/httpserver"
	"app.local/internal/platform/logger"
	"app.local/internal/platform/metrics"
	"app.local/internal/platform/process"
	"app.local/internal/platform/tracing"
)

const (
	gatewayTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	parentTraceID  = "0af7651916cd43dd8448eb211c80319c"
	traceparent    = "00-" + parentTraceID + "-b7ad6b7169203331-01"
)

type traceServer struct {
	e     *echo.Echo
	spans *tracetest.SpanRecorder
	logs  *bytes.Buffer
}

func newTraceServer(t *testing.T, trusted bool) traceServer {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	var logs bytes.Buffer
	e, err := httpserver.New(httpserver.Deps{
		Logger: logger.New("info", &logs),
		HTTP: config.HTTPConfig{
			BodyLimitBytes: 1 << 20, RequestTimeout: 5 * time.Second, GlobalRateLimitPerSecond: 1000, TrustedProxy: trusted,
		},
		CORS:    config.CORSConfig{AllowOrigins: []string{"https://example.com"}},
		Routes:  []func(*echo.Group){testRoutes},
		Tracer:  tracing.NewWithProcessor(rec),
		Metrics: metrics.NewRegistry(process.New("api", "test")),
	})
	if err != nil {
		t.Fatal(err)
	}
	return traceServer{e: e, spans: rec, logs: &logs}
}

func serve(e *echo.Echo, path string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// 在网关后：网关给的 X-Trace-Id 就是日志、响应头、导出的根 span 的 trace_id，根 span 没有父 span。
func TestTrace_TrustedGatewayTraceIDIsRootTraceID(t *testing.T) {
	s := newTraceServer(t, true)
	rec := serve(s.e, "/v1/widgets/1", map[string]string{httpserver.HeaderTraceID: gatewayTraceID})

	if got := rec.Header().Get(httpserver.HeaderTraceID); got != gatewayTraceID {
		t.Errorf("响应头 X-Trace-Id = %q, want %q", got, gatewayTraceID)
	}
	ended := s.spans.Ended()
	if len(ended) != 1 {
		t.Fatalf("spans = %d, want 1", len(ended))
	}
	span := ended[0]
	if span.SpanContext().TraceID().String() != gatewayTraceID || span.Parent().IsValid() {
		t.Errorf("根 span trace_id = %s, parent = %v", span.SpanContext().TraceID(), span.Parent())
	}
	if span.Name() != "GET /v1/widgets/:id" {
		t.Errorf("span 名 = %q, want 路由模板", span.Name())
	}
}

// traceparent 优先于 X-Trace-Id（以后拆出的内部服务之间传播）。
func TestTrace_TrustedTraceparentTakesPrecedence(t *testing.T) {
	s := newTraceServer(t, true)
	rec := serve(s.e, "/v1/widgets/1", map[string]string{"traceparent": traceparent, httpserver.HeaderTraceID: gatewayTraceID})
	if got := rec.Header().Get(httpserver.HeaderTraceID); got != parentTraceID {
		t.Errorf("X-Trace-Id = %q, want traceparent 的 %q", got, parentTraceID)
	}
}

// 不在网关后：客户端给的 X-Trace-Id 和 traceparent 一律忽略，不能把自己的请求混进别人的日志。
func TestTrace_UntrustedIgnoresUpstreamTraceIDs(t *testing.T) {
	s := newTraceServer(t, false)
	rec := serve(s.e, "/v1/widgets/1", map[string]string{"traceparent": traceparent, httpserver.HeaderTraceID: gatewayTraceID})
	got := rec.Header().Get(httpserver.HeaderTraceID)
	if got == "" || got == gatewayTraceID || got == parentTraceID {
		t.Errorf("X-Trace-Id = %q, 应是新生成的", got)
	}
	if span := s.spans.Ended()[0]; span.Parent().IsValid() {
		t.Errorf("不应接受上游的父 span：%v", span.Parent())
	}
}

// 不合法的 X-Trace-Id（长度不对、全零）被忽略，换成新生成的。
func TestTrace_InvalidGatewayTraceIDIsIgnored(t *testing.T) {
	s := newTraceServer(t, true)
	for _, bad := range []string{"rid-from-gateway", "00000000000000000000000000000000"} {
		got := serve(s.e, "/v1/widgets/1", map[string]string{httpserver.HeaderTraceID: bad}).Header().Get(httpserver.HeaderTraceID)
		if got == bad || len(got) != 32 {
			t.Errorf("X-Trace-Id %q → %q, 应换成新生成的", bad, got)
		}
	}
}

// 5xx 和 panic 标 Error，4xx 不标；健康检查和 /metrics 不产生 span、不带 X-Trace-Id。
func TestTrace_SpanStatusAndSkippedEndpoints(t *testing.T) {
	s := newTraceServer(t, false)
	serve(s.e, "/v1/widgets/missing", nil)
	serve(s.e, "/v1/widgets/panic", nil)
	for _, p := range []string{"/healthz", "/readyz", "/metrics"} {
		if rec := serve(s.e, p, nil); rec.Header().Get(httpserver.HeaderTraceID) != "" {
			t.Errorf("%s 不应带 X-Trace-Id", p)
		}
	}
	ended := s.spans.Ended()
	if len(ended) != 2 {
		t.Fatalf("spans = %d, want 2（健康检查和 /metrics 不产生 span）", len(ended))
	}
	if ended[0].Status().Code == codes.Error {
		t.Errorf("4xx 不应标 Error：%s", ended[0].Name())
	}
	if ended[1].Status().Code != codes.Error {
		t.Errorf("panic 应标 Error：%s", ended[1].Name())
	}
}

// 跨域的前端要能读到 X-Trace-Id。
func TestTrace_CORSExposesTraceIDHeader(t *testing.T) {
	s := newTraceServer(t, false)
	rec := serve(s.e, "/v1/widgets/1", map[string]string{echo.HeaderOrigin: "https://example.com"})
	if got := rec.Header().Get(echo.HeaderAccessControlExposeHeaders); !strings.Contains(got, httpserver.HeaderTraceID) {
		t.Errorf("Access-Control-Expose-Headers = %q, want 含 X-Trace-Id", got)
	}
}

// 采样的请求在 OpenMetrics 输出里带 trace_id exemplar，可以从指标跳到 trace。
func TestTrace_SampledRequestHasExemplar(t *testing.T) {
	s := newTraceServer(t, false)
	traceID := serve(s.e, "/v1/widgets/1", nil).Header().Get(httpserver.HeaderTraceID)
	rec := serve(s.e, "/metrics", map[string]string{"Accept": "application/openmetrics-text; version=1.0.0"})
	if !strings.Contains(rec.Body.String(), `trace_id="`+traceID+`"`) {
		t.Errorf("OpenMetrics 输出里没有 trace_id=%s 的 exemplar", traceID)
	}
}
