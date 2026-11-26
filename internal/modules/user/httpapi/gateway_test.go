package httpapi_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/prometheus/client_golang/prometheus"

	"app.local/internal/config"
	"app.local/internal/modules/user/domain"
	"app.local/internal/modules/user/domain/domaintest"
	"app.local/internal/modules/user/httpapi"
	"app.local/internal/platform/crypto"
	"app.local/internal/platform/httpserver"
	"app.local/internal/platform/oauth"
	"app.local/internal/platform/tracing"
)

// gatewayEnv 是网关鉴权模式的测试环境：前台用网关身份中间件，内部端口挂校验端点；gateway 模拟 OpenResty 的
// gateway_auth.lua（清头 → 有令牌时调校验端点 → 转发断言或失效原因 → 校验不可用时 503）。
type gatewayEnv struct {
	front, internal *echo.Echo
	svc             *domain.Service
	fakes           *domaintest.Fakes
	issuer          *crypto.TokenIssuer
	reg             *prometheus.Registry
}

func newGatewayEnv(t *testing.T, signer *crypto.AssertionSigner, verifier *crypto.AssertionVerifier) gatewayEnv {
	t.Helper()
	registry := oauth.Registry{"mini-app": {Native: &stubNative{}}, "plain": {Provider: &stubProvider{}}}
	front, svc, fakes, issuer := newFlowServerAuth(t, registry, func(*domain.Service) echo.MiddlewareFunc {
		return httpserver.NewGatewayIdentityMiddleware(verifier)
	})
	reg := prometheus.NewRegistry()
	verify, err := httpapi.NewVerifyHandler(flowSessionAuth(svc), signer, reg)
	if err != nil {
		t.Fatal(err)
	}
	internal, err := httpserver.NewInternalAPI(httpserver.InternalDeps{
		Logger:  slog.New(slog.DiscardHandler),
		HTTP:    config.HTTPConfig{BodyLimitBytes: 1 << 20, RequestTimeout: 5 * time.Second, GlobalRateLimitPerSecond: 1000},
		Routes:  []func(*echo.Group){func(g *echo.Group) { httpapi.RegisterInternal(g, verify) }},
		Metrics: prometheus.NewRegistry(),
		Tracer:  tracing.Disabled(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return gatewayEnv{front: front, internal: internal, svc: svc, fakes: fakes, issuer: issuer, reg: reg}
}

func (g gatewayEnv) do(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	req.Header.Del(httpserver.HeaderAuthAssertion)
	req.Header.Del(httpserver.HeaderAuthStatus)
	if authz := req.Header.Get(echo.HeaderAuthorization); authz != "" {
		sub := httptest.NewRequest(http.MethodGet, httpserver.InternalPrefix+httpapi.VerifyPath, nil)
		sub.Header.Set(echo.HeaderAuthorization, authz)
		res := httptest.NewRecorder()
		g.internal.ServeHTTP(res, sub)
		if res.Code != http.StatusOK {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusServiceUnavailable)
			return rec
		}
		if a := res.Header().Get(httpserver.HeaderAuthAssertion); a != "" {
			req.Header.Set(httpserver.HeaderAuthAssertion, a)
		} else {
			req.Header.Set(httpserver.HeaderAuthStatus, res.Header().Get(httpserver.HeaderAuthStatus))
		}
	}
	return doRequest(t, g.front, req)
}

// login 走前台登录（公开接口不需要身份），返回会话和真签发器重签的 access token。
func (g gatewayEnv) login(t *testing.T, idToken string) (*domain.Session, string) {
	t.Helper()
	rec := nativeLoginWith(t, g.front, "mini-app", map[string]string{"id_token": idToken}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("登录 = %d %s", rec.Code, rec.Body.String())
	}
	var tokens map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &tokens)
	family, _, _ := strings.Cut(tokens["refresh_token"].(string), ".")
	sess, err := g.fakes.Sessions.GetByFamily(t.Context(), family)
	if err != nil {
		t.Fatal(err)
	}
	access, err := g.issuer.Issue(sess.UserID, sess.ID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return sess, access
}

func newAssertionKeys(t *testing.T) (*crypto.AssertionSigner, *crypto.AssertionVerifier) {
	t.Helper()
	priv, pub, err := crypto.NewAssertionKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := crypto.NewAssertionSigner(priv, "k1")
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := crypto.NewAssertionVerifier(map[string]string{"k1": pub})
	if err != nil {
		t.Fatal(err)
	}
	return signer, verifier
}

func authed(method, path, access string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+access)
	return req
}

// 网关模式下：已登录操作、† 操作（最近验证的截止时间经断言传过来）可用；吊销、被顶掉立即生效，错误码与会话模式相同。
func TestGatewayMode_EndToEnd(t *testing.T) {
	signer, verifier := newAssertionKeys(t)
	g := newGatewayEnv(t, signer, verifier)
	g.svc.SetSessionPolicy(domain.NewSlotLimit(1))

	sess, access := g.login(t, "T1")
	if rec := g.do(t, authed(http.MethodGet, "/v1/me/sessions", access)); rec.Code != http.StatusOK {
		t.Fatalf("已登录操作 = %d %s", rec.Code, rec.Body.String())
	}
	// † 操作：刚登录，auth_time 在窗口内。
	if rec := g.do(t, authed(http.MethodPost, "/v1/me/identities/oauth/plain", access)); rec.Code != http.StatusOK {
		t.Errorf("† 操作 = %d %s, want 200（ReauthUntil 经断言传给 RequireRecentAuth）", rec.Code, rec.Body.String())
	}
	// 不带令牌访问需要登录的操作：401 invalid_token；公开接口不受影响（上面的登录就是）。
	if rec := g.do(t, httptest.NewRequest(http.MethodGet, "/v1/me/sessions", nil)); !hasCode(rec, http.StatusUnauthorized, "invalid_token") {
		t.Errorf("没带令牌 = %d %s", rec.Code, rec.Body.String())
	}

	// 同类设备再登录一次：旧会话被顶掉。
	_, access2 := g.login(t, "T1")
	if rec := g.do(t, authed(http.MethodGet, "/v1/me/sessions", access)); !hasCode(rec, http.StatusUnauthorized, "session_replaced") {
		t.Errorf("被顶掉的会话 = %d %s, want 401 session_replaced", rec.Code, rec.Body.String())
	}
	// 吊销后下一次请求就失效（网关不缓存）。
	sess2, err := g.fakes.Sessions.GetByFamily(t.Context(), onlyActiveFamily(t, g, sess.UserID))
	if err != nil {
		t.Fatal(err)
	}
	if err := g.svc.RevokeMySession(t.Context(), sess2.UserID, sess2.ID); err != nil {
		t.Fatal(err)
	}
	if rec := g.do(t, authed(http.MethodGet, "/v1/me/sessions", access2)); !hasCode(rec, http.StatusUnauthorized, "invalid_token") {
		t.Errorf("吊销后 = %d %s, want 401 invalid_token", rec.Code, rec.Body.String())
	}
}

func onlyActiveFamily(t *testing.T, g gatewayEnv, userID string) string {
	t.Helper()
	for _, s := range g.fakes.Sessions.ListAll() {
		if s.UserID == userID && s.RevokedAt == nil {
			return s.RefreshFamily
		}
	}
	t.Fatal("没有有效会话")
	return ""
}

// 绕过网关直连前台：自带的断言验不过签名，被拒绝（503：经网关到达的断言验不过只能是服务端的问题，见网关身份中间件）。
func TestGatewayMode_DirectRequestsCannotForgeIdentity(t *testing.T) {
	signer, verifier := newAssertionKeys(t)
	g := newGatewayEnv(t, signer, verifier)
	forger, _ := newAssertionKeys(t)
	forged, _ := forger.Sign(crypto.Assertion{UserID: "victim", SessionID: "s"})
	req := httptest.NewRequest(http.MethodGet, "/v1/me/sessions", nil)
	req.Header.Set(httpserver.HeaderAuthAssertion, forged)
	if rec := doRequest(t, g.front, req); !hasCode(rec, http.StatusServiceUnavailable, "auth_unavailable") {
		t.Errorf("伪造的断言 = %d %s, want 503 被拒绝", rec.Code, rec.Body.String())
	}
}

// 校验端点每一种结果，以及结果指标。
func TestVerifyEndpoint_Responses(t *testing.T) {
	signer, verifier := newAssertionKeys(t)
	g := newGatewayEnv(t, signer, verifier)
	_, access := g.login(t, "T1")
	call := func(e *echo.Echo, authz string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, httpserver.InternalPrefix+httpapi.VerifyPath, nil)
		if authz != "" {
			req.Header.Set(echo.HeaderAuthorization, authz)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}
	rec := call(g.internal, "Bearer "+access)
	assertion := rec.Header().Get(httpserver.HeaderAuthAssertion)
	if rec.Code != http.StatusOK || assertion == "" || rec.Header().Get(echo.HeaderCacheControl) != "no-store" {
		t.Fatalf("有效令牌 = %d %v", rec.Code, rec.Header())
	}
	if a, err := verifier.Verify(assertion); err != nil || a.ReauthUntil.IsZero() || a.SessionID == "" {
		t.Errorf("断言 = %+v, %v", a, err)
	}
	for name, authz := range map[string]string{"没带": "", "不是 Bearer": "Basic x", "签名不对": "Bearer " + access + "x"} {
		rec := call(g.internal, authz)
		if rec.Code != http.StatusOK || rec.Header().Get(httpserver.HeaderAuthStatus) != "invalid_token" || rec.Header().Get(httpserver.HeaderAuthAssertion) != "" {
			t.Errorf("%s = %d %v, want 200 + X-Auth-Status: invalid_token", name, rec.Code, rec.Header())
		}
	}
	if text := gatherText(t, g.reg); !strings.Contains(text, `result="ok"} 1`) || !strings.Contains(text, `result="invalid_token"} 3`) {
		t.Errorf("结果指标 = %s", text)
	}

	// 会话被同类设备顶掉：200 + X-Auth-Status: session_replaced。
	replaced, _ := httpapi.NewVerifyHandler(httpapi.SessionAuthConfig{Sessions: authenticator{err: domain.ErrSessionReplaced}, KeyID: "flow-kid", Key: []byte("flow-secret")},
		signer, prometheus.NewRegistry())
	if rec := call(internalWith(t, replaced), "Bearer "+access); rec.Code != http.StatusOK ||
		rec.Header().Get(httpserver.HeaderAuthStatus) != "session_replaced" || rec.Header().Get(httpserver.HeaderAuthAssertion) != "" {
		t.Errorf("被顶掉 = %d %v, want 200 + X-Auth-Status: session_replaced", rec.Code, rec.Header())
	}
	// 查会话失败：500（网关据此返回 503，不让客户端清令牌）。
	dbDown := errors.New("db down")
	failing, _ := httpapi.NewVerifyHandler(httpapi.SessionAuthConfig{Sessions: authenticator{err: dbDown}, KeyID: "flow-kid", Key: []byte("flow-secret")},
		signer, prometheus.NewRegistry())
	if rec := call(internalWith(t, failing), "Bearer "+access); rec.Code != http.StatusInternalServerError {
		t.Errorf("查会话失败 = %d, want 500", rec.Code)
	}
	// 没配签名私钥：端点照样注册，一律 500。
	unsigned, _ := httpapi.NewVerifyHandler(flowSessionAuth(g.svc), nil, prometheus.NewRegistry())
	if rec := call(internalWith(t, unsigned), "Bearer "+access); rec.Code != http.StatusInternalServerError {
		t.Errorf("没配签名私钥 = %d, want 500", rec.Code)
	}
}

func internalWith(t *testing.T, verify echo.HandlerFunc) *echo.Echo {
	t.Helper()
	e, err := httpserver.NewInternalAPI(httpserver.InternalDeps{
		Logger:  slog.New(slog.DiscardHandler),
		HTTP:    config.HTTPConfig{BodyLimitBytes: 1 << 20, RequestTimeout: 5 * time.Second, GlobalRateLimitPerSecond: 1000},
		Routes:  []func(*echo.Group){func(g *echo.Group) { httpapi.RegisterInternal(g, verify) }},
		Metrics: prometheus.NewRegistry(),
		Tracer:  tracing.Disabled(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func gatherText(t *testing.T, reg *prometheus.Registry) string {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				b.WriteString(l.GetName() + `="` + l.GetValue() + `"} `)
			}
			b.WriteString(strconvFloat(m.GetCounter().GetValue()) + "\n")
		}
	}
	return b.String()
}

func strconvFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
