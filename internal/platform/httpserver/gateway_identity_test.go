package httpserver_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/prometheus/client_golang/prometheus"

	"app.local/internal/config"
	"app.local/internal/kit/authn"
	"app.local/internal/platform/crypto"
	"app.local/internal/platform/httpserver"
	"app.local/internal/platform/tracing"
)

func assertionPair(t *testing.T, kid string) (*crypto.AssertionSigner, string) {
	t.Helper()
	priv, pub, err := crypto.NewAssertionKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	s, err := crypto.NewAssertionSigner(priv, kid)
	if err != nil {
		t.Fatal(err)
	}
	return s, pub
}

// runGateway 让请求经过网关身份中间件，返回中间件的错误和放行时写入的身份。
func runGateway(t *testing.T, v *crypto.AssertionVerifier, header map[string]string) (httpserver.UserSession, bool, error) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for k, val := range header {
		req.Header.Set(k, val)
	}
	c := echo.New().NewContext(req, httptest.NewRecorder())
	var got httpserver.UserSession
	called := false
	err := httpserver.NewGatewayIdentityMiddleware(v)(func(c *echo.Context) error {
		called = true
		got, _ = httpserver.UserSessionFrom(c)
		return nil
	})(c)
	return got, called, err
}

func TestGatewayIdentity_ValidAssertionCarriesFullIdentity(t *testing.T) {
	s, pub := assertionPair(t, "k1")
	v, _ := crypto.NewAssertionVerifier(map[string]string{"k1": pub})
	authTime := time.Now().Add(-time.Minute).Truncate(time.Second)
	token, _ := s.Sign(crypto.Assertion{UserID: "u1", SessionID: "s1", AuthTime: authTime, AMR: []string{"wechat"},
		ReauthUntil: authTime.Add(10 * time.Minute)})
	got, called, err := runGateway(t, v, map[string]string{httpserver.HeaderAuthAssertion: token})
	if err != nil || !called {
		t.Fatalf("error = %v called = %v", err, called)
	}
	if got.UserID != "u1" || got.SessionID != "s1" || !got.AuthTime.Equal(authTime) || len(got.AMR) != 1 ||
		!got.ReauthUntil.Equal(authTime.Add(10*time.Minute)) {
		t.Errorf("UserSession = %+v", got)
	}
}

// 每一种拒绝的结果：没有断言时按 X-Auth-Status 返回 401；带了断言但验不过一律 503（经网关到达的断言验不过只能是
// 服务端的问题，不能让客户端因此清令牌）。
func TestGatewayIdentity_Rejections(t *testing.T) {
	_, pub := assertionPair(t, "k1")
	forger, _ := assertionPair(t, "k1")
	stranger, _ := assertionPair(t, "k-other")
	v, _ := crypto.NewAssertionVerifier(map[string]string{"k1": pub})
	sign := func(s *crypto.AssertionSigner) string {
		tok, _ := s.Sign(crypto.Assertion{UserID: "u", SessionID: "s"})
		return tok
	}
	access, _ := crypto.NewTokenIssuer("hs256-secret-0123456789abcdef-0123", "k1").Issue("u", "s", time.Now().Add(time.Minute))
	for name, tc := range map[string]struct {
		header map[string]string
		want   error
	}{
		"什么都没有":               {nil, authn.ErrInvalidToken},
		"X-Auth-Status 失效":    {map[string]string{httpserver.HeaderAuthStatus: "invalid_token"}, authn.ErrInvalidToken},
		"X-Auth-Status 被顶掉":   {map[string]string{httpserver.HeaderAuthStatus: "session_replaced"}, authn.ErrSessionReplaced},
		"X-Auth-Status 不认识的值": {map[string]string{httpserver.HeaderAuthStatus: "ok"}, authn.ErrInvalidToken},
		"签名不对（公钥贴错或伪造）":       {map[string]string{httpserver.HeaderAuthAssertion: sign(forger)}, authn.ErrAuthUnavailable},
		"access token 冒充断言":   {map[string]string{httpserver.HeaderAuthAssertion: access}, authn.ErrAuthUnavailable},
		"kid 不认识（轮换漏配）":       {map[string]string{httpserver.HeaderAuthAssertion: sign(stranger)}, authn.ErrAuthUnavailable},
		"断言优先于 X-Auth-Status": {map[string]string{httpserver.HeaderAuthAssertion: sign(forger), httpserver.HeaderAuthStatus: "session_replaced"}, authn.ErrAuthUnavailable},
	} {
		_, called, err := runGateway(t, v, tc.header)
		if called || !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v called = %v, want %v", name, err, called, tc.want)
		}
	}
	if code := httpserver.StatusOf(errors.Join(authn.ErrAuthUnavailable, crypto.ErrAssertionUnavailable)); code != http.StatusServiceUnavailable {
		t.Errorf("auth_unavailable 状态码 = %d, want 503", code)
	}
}

func TestRequireRecentAuth_ComparesReauthUntil(t *testing.T) {
	for name, tc := range map[string]struct {
		until time.Time
		ok    bool
	}{
		"还在窗口内": {time.Now().Add(time.Minute), true},
		"已过":    {time.Now().Add(-time.Second), false},
		"没有":    {time.Time{}, false},
	} {
		c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/", nil), httptest.NewRecorder())
		httpserver.SetUserSession(c, httpserver.UserSession{UserID: "u", SessionID: "s", ReauthUntil: tc.until})
		called := false
		err := httpserver.NewRequireRecentAuth()(func(*echo.Context) error { called = true; return nil })(c)
		if called != tc.ok || (!tc.ok && !errors.Is(err, authn.ErrReauthRequired)) {
			t.Errorf("%s: called = %v error = %v", name, called, err)
		}
	}
}

func newInternalTestServer(t *testing.T, reg *prometheus.Registry, rate float64) *echo.Echo {
	t.Helper()
	e, err := httpserver.NewInternalAPI(httpserver.InternalDeps{
		Logger: discardLogger(),
		HTTP:   config.HTTPConfig{BodyLimitBytes: 1 << 20, RequestTimeout: 5 * time.Second, GlobalRateLimitPerSecond: rate},
		Routes: []func(*echo.Group){func(g *echo.Group) {
			g.GET("/ping", func(c *echo.Context) error { return c.String(http.StatusOK, "pong") })
		}},
		Metrics: reg,
		Tracer:  tracing.Disabled(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// 内部端口：路由挂在 /internal 下，不挂 CORS，照常记 HTTP 指标（server="internal"）；按 IP 限流（网关在前台限流之前就调
// 会话校验，不限的话一个有效令牌就能让每个请求都先查一次库）。
func TestInternalAPI_RateLimitedNoCORS(t *testing.T) {
	reg := prometheus.NewRegistry()
	e := newInternalTestServer(t, reg, 0.000001)
	codes := []int{}
	for range 2 {
		req := httptest.NewRequest(http.MethodGet, "/internal/ping", nil)
		req.Header.Set(echo.HeaderOrigin, "https://example.com")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
		if rec.Header().Get(echo.HeaderAccessControlAllowOrigin) != "" {
			t.Error("内部端口不应有 CORS 头")
		}
	}
	if codes[0] != http.StatusOK || codes[1] != http.StatusTooManyRequests {
		t.Errorf("codes = %v, want [200 429]", codes)
	}
	if text := metricsText(t, reg); !strings.Contains(text, "http_server_request_duration_seconds") || !strings.Contains(text, `value:"internal"`) {
		t.Error("HTTP 指标应带 server=internal")
	}
}

// 内部路由只在内部端口上：前台端口访问 /internal/ 是 404。
func TestServer_FrontPortHasNoInternalRoutes(t *testing.T) {
	e := newTestServer(t, discardLogger(), false)
	if rec := get(e, "/internal/ping"); rec.Code != http.StatusNotFound {
		t.Errorf("前台端口 /internal/ping = %d, want 404", rec.Code)
	}
}

func metricsText(t *testing.T, reg *prometheus.Registry) string {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, mf := range mfs {
		b.WriteString(mf.String())
	}
	return b.String()
}
