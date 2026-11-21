package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"app.local/internal/config"
	"app.local/internal/kit/errs"
	"app.local/internal/platform/httpserver"
)

var (
	errWidgetNotFound = errs.New(errs.KindNotFound, "widget_not_found", "widget not found")
	errWidgetBusy     = errs.New(errs.KindTooManyRequests, "widget_busy", "try again later")
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func testRoutes(g *echo.Group) {
	g.POST("/widgets", func(c *echo.Context) error {
		var v struct{}
		return c.Bind(&v)
	})
	g.GET("/widgets/:id", func(c *echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})
	g.GET("/widgets/missing", func(*echo.Context) error {
		return fmt.Errorf("get widget: %w", fmt.Errorf("repo: %w", errWidgetNotFound))
	})
	g.GET("/widgets/busy", func(*echo.Context) error {
		return fmt.Errorf("busy: %w", errWidgetBusy.WithRetryAfter(89500*time.Millisecond))
	})
	g.GET("/widgets/broken", func(*echo.Context) error {
		return errors.New(`pq: relation "widgets" does not exist at /srv/app/internal/x.go:42`)
	})
	g.GET("/widgets/panic", func(*echo.Context) error {
		panic("boom: secret at /srv/app/internal/x.go:42")
	})
	g.POST("/widgets", func(c *echo.Context) error {
		var req widgetRequest
		if err := c.Bind(&req); err != nil {
			return err
		}
		if err := c.Validate(&req); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	})
}

type widgetRequest struct {
	ProductID string `json:"product_id" validate:"required,uuid"`
	Quantity  int64  `json:"quantity" validate:"gt=0"`
}

func newTestServer(t *testing.T, logger *slog.Logger, apiDocs bool, pingers ...httpserver.Pinger) *echo.Echo {
	t.Helper()
	e, err := httpserver.New(httpserver.Deps{
		Logger: logger,
		HTTP: config.HTTPConfig{
			BodyLimitBytes:           2 * 1024 * 1024,
			RequestTimeout:           5 * time.Second,
			GlobalRateLimitPerSecond: 1000,
			APIDocsEnabled:           apiDocs,
		},
		CORS:    config.CORSConfig{AllowOrigins: []string{"https://example.com"}},
		Pingers: pingers,
		Routes:  []func(*echo.Group){testRoutes},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return e
}

func get(e *echo.Echo, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) httpserver.ErrorBody {
	t.Helper()
	var body httpserver.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body
}

type pingerFunc func(context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error { return f(ctx) }

func TestServer_HealthEndpoints(t *testing.T) {
	e := newTestServer(t, discardLogger(), false)
	if rec := get(e, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("/healthz status = %d, want 200", rec.Code)
	}
	if rec := get(e, "/readyz"); rec.Code != http.StatusOK {
		t.Errorf("/readyz status = %d, want 200（没有依赖时直接就绪）", rec.Code)
	}

	down := newTestServer(t, discardLogger(), false, pingerFunc(func(context.Context) error { return errors.New("down") }))
	if rec := get(down, "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("依赖不可用时 /readyz status = %d, want 503", rec.Code)
	}
}

// 业务错误只返回 Code/Message，不能带 %w 包装出来的调用链前缀。
func TestServer_BusinessError_RendersCodeAndMessageOnly(t *testing.T) {
	e := newTestServer(t, discardLogger(), false)

	rec := get(e, "/v1/widgets/missing")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	body := decodeError(t, rec)
	if body.Code != "widget_not_found" || body.Message != "widget not found" {
		t.Errorf("body = %+v, want code=widget_not_found message=%q", body, "widget not found")
	}
}

// Bind 遇到不认识的 Content-Type 返回不带 message 的 415：响应用状态码的标准文案，不是 500 的文案。
func TestServer_UnsupportedMediaTypeHasStatusText(t *testing.T) {
	e := newTestServer(t, discardLogger(), false)
	req := httptest.NewRequest(http.MethodPost, "/v1/widgets", strings.NewReader("{bad"))
	req.Header.Set(echo.HeaderContentType, "text/plain")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if body := decodeError(t, rec); rec.Code != http.StatusUnsupportedMediaType || body.Message != "Unsupported Media Type" {
		t.Errorf("status = %d, body = %+v, want 415 Unsupported Media Type", rec.Code, body)
	}
}

// 需要等待的错误返回 429，剩余秒数（向上取整）同时写进 Retry-After 响应头和响应体。
func TestServer_TooManyRequestsCarriesRetryAfter(t *testing.T) {
	e := newTestServer(t, discardLogger(), false)

	rec := get(e, "/v1/widgets/busy")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "90" {
		t.Errorf("Retry-After = %q, want 90", got)
	}
	if body := decodeError(t, rec); body.Code != "widget_busy" || body.RetryAfter != 90 {
		t.Errorf("body = %+v, want code=widget_busy retry_after=90", body)
	}
	if strings.Contains(get(e, "/v1/widgets/missing").Body.String(), "retry_after") {
		t.Error("不需要等待的错误不带 retry_after")
	}
}

func TestServer_UnknownErrorDoesNotLeakDetails(t *testing.T) {
	e := newTestServer(t, discardLogger(), false)

	rec := get(e, "/v1/widgets/broken")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := decodeError(t, rec)
	if body.Code != "internal_error" || strings.Contains(rec.Body.String(), "pq:") || strings.Contains(rec.Body.String(), ".go:") {
		t.Errorf("500 响应泄露了内部细节: %s", rec.Body.String())
	}
}

func TestServer_ErrorLogsIncludeRequestID(t *testing.T) {
	var logBuf bytes.Buffer
	e := newTestServer(t, slog.New(slog.NewJSONHandler(&logBuf, nil)), false)

	rec := get(e, "/v1/widgets/missing")
	requestID := rec.Header().Get(echo.HeaderXRequestID)
	if requestID == "" {
		t.Fatal("响应没有带 X-Request-Id header")
	}
	if !strings.Contains(logBuf.String(), requestID) {
		t.Errorf("错误日志里没有 request_id = %q，日志内容: %s", requestID, logBuf.String())
	}
}

func TestServer_APIDocs(t *testing.T) {
	e := newTestServer(t, discardLogger(), true)

	t.Run("openapi.yaml 返回 spec 原文件", func(t *testing.T) {
		rec := get(e, "/openapi.yaml")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "openapi: 3.2.0") {
			t.Errorf("body 不像是 openapi.yaml 的内容: %.100s", rec.Body.String())
		}
	})

	t.Run("docs 页面挂载 Redoc 并指向本项目的 openapi.yaml", func(t *testing.T) {
		rec := get(e, "/docs")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `spec-url="/openapi.yaml"`) {
			t.Errorf("/docs 没有指向 /openapi.yaml，实际内容: %s", body)
		}
		if !strings.Contains(body, "cdn.redoc.ly/redoc/v3.2.0/") {
			t.Errorf("/docs 没有引用锁定版本的 redoc bundle，实际内容: %s", body)
		}
		if !strings.Contains(body, `disable-telemetry="true"`) {
			t.Error(`/docs 缺少 disable-telemetry="true"：redoc v3 默认向 Redocly 上报匿名使用数据，且裸写属性（空字符串值）不会被当作 true，必须显式 ="true"`)
		}
	})

	t.Run("docs 路由带 CSP，业务 API 路由不受影响", func(t *testing.T) {
		csp := get(e, "/docs").Header().Get(echo.HeaderContentSecurityPolicy)
		// CDN 域后面紧跟分号，说明 script-src 里没有别的来源（包括 unsafe-inline）。
		if !strings.Contains(csp, "script-src https://cdn.redoc.ly;") {
			t.Errorf("CSP = %q，script-src 应只放行 cdn.redoc.ly（redoc v3 是外联 ESM 脚本，不需要 unsafe-inline）", csp)
		}
		if csp := get(e, "/v1/widgets/missing").Header().Get(echo.HeaderContentSecurityPolicy); csp != "" {
			t.Errorf("业务 API 路由不应该带 CSP，实际 = %q", csp)
		}
	})

	t.Run("swagger 路由已移除", func(t *testing.T) {
		for _, path := range []string{"/swagger", "/swagger/index.html"} {
			if rec := get(e, path); rec.Code != http.StatusNotFound {
				t.Errorf("GET %s status = %d, want 404（文档 UI 已统一到 /docs）", path, rec.Code)
			}
		}
	})
}

func TestServer_APIDocs_DisabledByConfig(t *testing.T) {
	e := newTestServer(t, discardLogger(), false)
	for _, path := range []string{"/openapi.yaml", "/docs"} {
		if rec := get(e, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", path, rec.Code)
		}
	}
}

// 校验失败的 message 用 JSON 字段名，不带 Go 结构体名、字段名和 validator 的原始格式。
func TestServer_ValidationErrorUsesJSONFieldNames(t *testing.T) {
	e := newTestServer(t, discardLogger(), false)

	req := httptest.NewRequest(http.MethodPost, "/v1/widgets", strings.NewReader(`{"product_id":"x","quantity":0}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	body := decodeError(t, rec)
	if body.Code != "invalid_request" {
		t.Errorf("code = %q, want invalid_request", body.Code)
	}
	for _, want := range []string{"product_id", "quantity"} {
		if !strings.Contains(body.Message, want) {
			t.Errorf("message = %q，缺少字段 %s", body.Message, want)
		}
	}
	for _, leak := range []string{"widgetRequest", "ProductID", "Key:"} {
		if strings.Contains(body.Message, leak) {
			t.Errorf("message = %q，带出了 Go 侧的 %q", body.Message, leak)
		}
	}
}

// handler panic 由 Recover 渲染成固定文案的 500，panic 内容只进带 request_id 的日志。
func TestServer_PanicRendersInternalErrorAndLogsRequestID(t *testing.T) {
	var logBuf bytes.Buffer
	e := newTestServer(t, slog.New(slog.NewJSONHandler(&logBuf, nil)), false)

	rec := get(e, "/v1/widgets/panic")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := decodeError(t, rec); body.Code != "internal_error" || strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("panic 响应泄露了内部细节: %s", rec.Body.String())
	}
	requestID := rec.Header().Get(echo.HeaderXRequestID)
	if requestID == "" || !strings.Contains(logBuf.String(), requestID) || !strings.Contains(logBuf.String(), "boom") {
		t.Errorf("日志应带 request_id = %q 和 panic 内容，实际: %s", requestID, logBuf.String())
	}
}

// 指标的 url 标签必须是路由模板：带 ID 的路径和扫描器打出的 404 路径都不能成为标签值，
// 否则每个 ID、每个乱扫的路径各占一条时间序列。
func TestServer_MetricsURLLabelIsRouteTemplate(t *testing.T) {
	e := newTestServer(t, discardLogger(), false)
	get(e, httpserver.APIPrefix+"/widgets/0193f0a2-0000-7000-8000-000000000001")
	get(e, "/no/such/path-1")
	get(e, "/no/such/path-2")

	body := get(e, "/metrics").Body.String()
	for _, leaked := range []string{"0193f0a2-0000-7000-8000-000000000001", "/no/such/path-1", "/no/such/path-2"} {
		if strings.Contains(body, leaked) {
			t.Errorf("/metrics 里出现了原始路径 %q，url 标签应该是路由模板", leaked)
		}
	}
	if want := `url="` + httpserver.APIPrefix + `/widgets/:id"`; !strings.Contains(body, want) {
		t.Errorf("/metrics 里没有 %s，带参数的路由应按模板计数", want)
	}
}
