package httpserver_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"app.local/internal/config"
	"app.local/internal/kit/logctx"
	"app.local/internal/platform/httpserver"
	"app.local/internal/platform/logger"
)

func parseLogLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("日志不是 JSON：%q", line)
		}
		out = append(out, m)
	}
	return out
}

// 一次登录态请求里，经任意 logger 输出的记录都带齐请求的关联字段。
func TestServer_EveryLogInRequestCarriesCorrelationFields(t *testing.T) {
	var buf bytes.Buffer
	root := logger.New("info", &buf, logger.Process("api", "host-a")...)
	e, err := httpserver.New(httpserver.Deps{
		Logger: root,
		HTTP: config.HTTPConfig{
			BodyLimitBytes: 1 << 20, RequestTimeout: 5 * time.Second, GlobalRateLimitPerSecond: 1000,
		},
		CORS: config.CORSConfig{AllowOrigins: []string{"https://example.com"}},
		Routes: []func(*echo.Group){func(g *echo.Group) {
			auth := func(next echo.HandlerFunc) echo.HandlerFunc {
				return func(c *echo.Context) error {
					httpserver.SetUserSession(c, httpserver.UserSession{UserID: "u-1"})
					return next(c)
				}
			}
			g.GET("/orders/:id", func(c *echo.Context) error {
				ctx := c.Request().Context()
				c.Logger().InfoContext(ctx, "via echo logger")
				logctx.From(ctx).InfoContext(ctx, "via logctx")
				slog.New(root.Handler()).InfoContext(ctx, "via rebuilt logger")
				return errWidgetNotFound
			}, auth)
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/orders/42", nil)
	req.Header.Set(echo.HeaderXRequestID, "rid-from-gateway")
	e.ServeHTTP(httptest.NewRecorder(), req)

	recs := parseLogLines(t, &buf)
	if len(recs) != 4 {
		t.Fatalf("期望 3 条业务日志 + 1 条错误日志，实际 %d 条：%s", len(recs), buf.String())
	}
	for _, rec := range recs {
		for key, want := range map[string]any{
			"request_id": "rid-from-gateway", "user_id": "u-1", "method": "GET", "route": "/v1/orders/:id",
			"service": "api", "instance": "host-a",
		} {
			if rec[key] != want {
				t.Errorf("%q 的 %s = %v, want %v", rec["msg"], key, rec[key], want)
			}
		}
	}
	last := recs[len(recs)-1]
	if last["msg"] != "http request failed" || last["error_kind"] != "not_found" || last["code"] != "widget_not_found" {
		t.Errorf("错误日志 = %v", last)
	}
}

func TestServer_UnmatchedRouteHasNoRouteField(t *testing.T) {
	var buf bytes.Buffer
	e := newTestServer(t, logger.New("info", &buf), false)

	get(e, "/v1/no/such/path?token=secret")

	recs := parseLogLines(t, &buf)
	if len(recs) == 0 {
		t.Fatal("404 应记一条错误日志")
	}
	for _, rec := range recs {
		if rec["request_id"] == nil || rec["method"] != "GET" {
			t.Errorf("缺少关联字段：%v", rec)
		}
		if route, ok := rec["route"]; ok {
			if s, _ := route.(string); strings.Contains(s, "no/such") {
				t.Errorf("route 不能用原始路径：%v", rec)
			}
		}
	}
}

func TestAdmin_StaffIDInLogs(t *testing.T) {
	var buf bytes.Buffer
	e, err := httpserver.NewAdmin(httpserver.AdminDeps{
		Logger: logger.New("info", &buf),
		HTTP: config.HTTPConfig{
			BodyLimitBytes: 1 << 20, RequestTimeout: 5 * time.Second, GlobalRateLimitPerSecond: 1000,
		},
		Origin: adminOrigin,
		Auth: []echo.MiddlewareFunc{func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c *echo.Context) error {
				httpserver.SetStaffSession(c, httpserver.StaffSession{StaffID: "s-1"})
				return next(c)
			}
		}},
		Routes: []func(*echo.Group){func(g *echo.Group) {
			g.GET("/things/:id", func(*echo.Context) error { return errWidgetNotFound })
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	do(e, http.MethodGet, httpserver.AdminAPIPrefix+"/things/1", nil)

	recs := parseLogLines(t, &buf)
	if len(recs) != 1 || recs[0]["staff_id"] != "s-1" || recs[0]["route"] != httpserver.AdminAPIPrefix+"/things/:id" {
		t.Errorf("records = %v", recs)
	}
}
