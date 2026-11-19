package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/labstack/echo/v5"

	"app.local/internal/config"
	"app.local/internal/platform/httpserver"
)

const adminOrigin = "https://admin.example.com"

func adminRoutes(g *echo.Group) {
	g.GET("/things", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	g.POST("/things", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	g.GET("/whoami", func(c *echo.Context) error {
		s, err := httpserver.StaffSessionFrom(c)
		if err != nil {
			return err
		}
		return c.String(http.StatusOK, s.StaffID)
	})
}

func newAdminServer(t *testing.T, deps httpserver.AdminDeps) *echo.Echo {
	t.Helper()
	deps.Logger = discardLogger()
	deps.HTTP = config.HTTPConfig{
		BodyLimitBytes:           2 * 1024 * 1024,
		RequestTimeout:           5 * time.Second,
		GlobalRateLimitPerSecond: 1000,
		APIDocsEnabled:           true,
	}
	deps.Origin = adminOrigin
	deps.Routes = append(deps.Routes, adminRoutes)
	if len(deps.Auth) == 0 {
		deps.Auth = []echo.MiddlewareFunc{passthrough}
	}
	e, err := httpserver.NewAdmin(deps)
	if err != nil {
		t.Fatalf("NewAdmin() error = %v", err)
	}
	return e
}

func do(e *echo.Echo, method, path string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestAdmin_RoutesUnderAdminPrefixOnly(t *testing.T) {
	e := newAdminServer(t, httpserver.AdminDeps{})
	if rec := get(e, "/admin/v1/things"); rec.Code != http.StatusNoContent {
		t.Errorf("GET /admin/v1/things = %d, want 204", rec.Code)
	}
	if rec := get(e, "/admin/v1/nosuch"); rec.Code != http.StatusNotFound {
		t.Errorf("GET /admin/v1/nosuch = %d, want 404", rec.Code)
	}
	if rec := get(e, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("/healthz = %d, want 200", rec.Code)
	}
}

// 前台 server 上不能出现后台路由。
func TestAdmin_PublicServerHasNoAdminRoutes(t *testing.T) {
	e := newTestServer(t, discardLogger(), true)
	for _, r := range e.Router().Routes() {
		if strings.HasPrefix(r.Path, httpserver.AdminAPIPrefix) || r.Path == "/admin.yaml" {
			t.Errorf("前台 server 注册了后台路由 %s %s", r.Method, r.Path)
		}
	}
}

func TestAdmin_RejectsCrossSiteWrites(t *testing.T) {
	e := newAdminServer(t, httpserver.AdminDeps{})
	tests := []struct {
		name   string
		method string
		header map[string]string
		want   int
	}{
		{"同源写请求", http.MethodPost, map[string]string{"Origin": adminOrigin, "Sec-Fetch-Site": "same-origin"}, http.StatusNoContent},
		{"非浏览器客户端（不带 Origin）", http.MethodPost, nil, http.StatusNoContent},
		{"跨源 Origin", http.MethodPost, map[string]string{"Origin": "https://evil.example.com"}, http.StatusForbidden},
		{"Sec-Fetch-Site 为 cross-site", http.MethodPost, map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"Sec-Fetch-Site 为 same-site", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
		{"跨源 GET 不拦（读请求没有副作用）", http.MethodGet, map[string]string{"Origin": "https://evil.example.com"}, http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if rec := do(e, tt.method, "/admin/v1/things", tt.header); rec.Code != tt.want {
				t.Errorf("%s = %d, want %d", tt.method, rec.Code, tt.want)
			}
		})
	}
}

func TestAdmin_GroupMiddlewaresRunAfterOriginCheck(t *testing.T) {
	session := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			httpserver.SetStaffSession(c, httpserver.StaffSession{StaffID: "staff-1"})
			return next(c)
		}
	}
	e := newAdminServer(t, httpserver.AdminDeps{Auth: []echo.MiddlewareFunc{session}})
	rec := get(e, "/admin/v1/whoami")
	if rec.Code != http.StatusOK || rec.Body.String() != "staff-1" {
		t.Errorf("GET /admin/v1/whoami = %d %q, want 200 staff-1", rec.Code, rec.Body.String())
	}

	bare := newAdminServer(t, httpserver.AdminDeps{})
	if rec := get(bare, "/admin/v1/whoami"); rec.Code != http.StatusUnauthorized {
		t.Errorf("没有会话中间件时 StaffSessionFrom 应返回 401，got %d", rec.Code)
	}
}

func TestAdmin_SessionKeysAreSeparate(t *testing.T) {
	e := echo.New()
	c := e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
	httpserver.SetUserSession(c, httpserver.UserSession{UserID: "user-1"})
	if _, err := httpserver.StaffSessionFrom(c); err == nil {
		t.Error("只写了 UserSession，StaffSessionFrom 却没有返回错误：两类主体必须用不同的键")
	}
}

func TestAdmin_DocsServeAdminSpecOnly(t *testing.T) {
	e := newAdminServer(t, httpserver.AdminDeps{})
	rec := get(e, "/admin.yaml")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/admin/v1") {
		t.Errorf("/admin.yaml = %d, body 不是 admin.yaml: %.80s", rec.Code, rec.Body.String())
	}
	if body := get(e, "/docs").Body.String(); !strings.Contains(body, `spec-url="/admin.yaml"`) {
		t.Errorf("/docs 应指向 /admin.yaml: %s", body)
	}
	if rec := get(e, "/openapi.yaml"); rec.Code != http.StatusNotFound {
		t.Errorf("后台 server 不应暴露前台 spec，/openapi.yaml = %d", rec.Code)
	}
}

func TestAdmin_CSPAndNoCORS(t *testing.T) {
	e := newAdminServer(t, httpserver.AdminDeps{})
	rec := do(e, http.MethodGet, "/admin/v1/things", map[string]string{"Origin": adminOrigin})
	if csp := rec.Header().Get(echo.HeaderContentSecurityPolicy); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q, want 默认的后台 CSP", csp)
	}
	if acao := rec.Header().Get(echo.HeaderAccessControlAllowOrigin); acao != "" {
		t.Errorf("后台同源部署，不应返回 CORS 头，got %q", acao)
	}
}

func TestAdmin_SPA(t *testing.T) {
	static := fstest.MapFS{
		"index.html":        {Data: []byte("<html>admin</html>")},
		"assets/app-abc.js": {Data: []byte("console.log(1)")},
		"favicon.svg":       {Data: []byte("<svg/>")},
	}
	e := newAdminServer(t, httpserver.AdminDeps{Static: static})

	tests := []struct {
		path, wantBody, wantCache string
		wantCode                  int
	}{
		{"/", "<html>admin</html>", "no-cache", http.StatusOK},
		{"/items/123", "<html>admin</html>", "no-cache", http.StatusOK},
		{"/assets/app-abc.js", "console.log(1)", "public, max-age=31536000, immutable", http.StatusOK},
		{"/favicon.svg", "<svg/>", "no-cache", http.StatusOK},
		{"/staff/john.doe", "<html>admin</html>", "no-cache", http.StatusOK},
		{"/assets/app-old.js", "", "", http.StatusNotFound},
	}
	for _, tt := range tests {
		rec := get(e, tt.path)
		if rec.Code != tt.wantCode {
			t.Errorf("GET %s = %d, want %d", tt.path, rec.Code, tt.wantCode)
			continue
		}
		if tt.wantCode != http.StatusOK {
			continue
		}
		if rec.Body.String() != tt.wantBody || rec.Header().Get(echo.HeaderCacheControl) != tt.wantCache {
			t.Errorf("GET %s = %q (Cache-Control %q), want %q (%q)", tt.path, rec.Body.String(), rec.Header().Get(echo.HeaderCacheControl), tt.wantBody, tt.wantCache)
		}
	}
	// 后台接口的 404 不能被前端路由回退吞掉。
	if rec := get(e, "/admin/v1/nosuch"); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "<html>") {
		t.Errorf("GET /admin/v1/nosuch = %d %q, want JSON 404", rec.Code, rec.Body.String())
	}
	if rec := get(e, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("托管前端后 /healthz = %d, want 200", rec.Code)
	}
}

// 冷启动打开页面要并发加载几十个资源文件，全局限流只限接口，不能让页面加载一半。
func TestAdmin_StaticFilesSkipGlobalRateLimit(t *testing.T) {
	e, err := httpserver.NewAdmin(httpserver.AdminDeps{
		Logger: discardLogger(),
		HTTP:   config.HTTPConfig{BodyLimitBytes: 1 << 20, RequestTimeout: 5 * time.Second, GlobalRateLimitPerSecond: 1},
		Origin: adminOrigin,
		Routes: []func(g *echo.Group){adminRoutes},
		Auth:   []echo.MiddlewareFunc{passthrough},
		Static: fstest.MapFS{"index.html": {Data: []byte("<html/>")}, "assets/a.js": {Data: []byte("1")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		for _, p := range []string{"/", "/accounts", "/assets/a.js"} {
			if rec := get(e, p); rec.Code != http.StatusOK {
				t.Fatalf("第 %d 轮 GET %s = %d, want 200", i, p, rec.Code)
			}
		}
	}
	limited := false
	for range 20 {
		if get(e, "/admin/v1/things").Code == http.StatusTooManyRequests {
			limited = true
		}
	}
	if !limited {
		t.Error("后台接口应仍受全局限流")
	}
}

func passthrough(next echo.HandlerFunc) echo.HandlerFunc { return next }

// 有后台路由时必须有 Auth；只挂了别的中间件（如操作日志）不算鉴权。
func TestAdmin_RefusesRoutesWithoutAuth(t *testing.T) {
	_, err := httpserver.NewAdmin(httpserver.AdminDeps{
		Logger:      discardLogger(),
		HTTP:        config.HTTPConfig{BodyLimitBytes: 1024},
		Origin:      adminOrigin,
		Routes:      []func(*echo.Group){adminRoutes},
		Middlewares: []echo.MiddlewareFunc{passthrough},
	})
	if err == nil {
		t.Fatal("有后台路由但没有 Auth 时 NewAdmin 应该报错，否则后台接口会裸露")
	}
}

func TestAdmin_MiddlewaresRunAfterAuth(t *testing.T) {
	var order []string
	mark := func(name string) echo.MiddlewareFunc {
		return func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c *echo.Context) error { order = append(order, name); return next(c) }
		}
	}
	e := newAdminServer(t, httpserver.AdminDeps{Auth: []echo.MiddlewareFunc{mark("auth")}, Middlewares: []echo.MiddlewareFunc{mark("audit")}})
	get(e, "/admin/v1/things")
	if strings.Join(order, ",") != "auth,audit" {
		t.Errorf("中间件顺序 = %v, want [auth audit]", order)
	}
}
