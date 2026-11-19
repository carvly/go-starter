package adminapi_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"app.local/api"
	"app.local/internal/config"
	"app.local/internal/kit/uow/uowtest"
	"app.local/internal/modules/staff/adminapi"
	staff "app.local/internal/modules/staff/domain"
	"app.local/internal/modules/staff/domain/domaintest"
	"app.local/internal/platform/httpserver"
)

type fakePasswords struct{}

func (fakePasswords) Hash(p string) (string, error)     { return "hash:" + p, nil }
func (fakePasswords) Compare(h, p string) (bool, error) { return h == "hash:"+p, nil }

type testServer struct {
	e    *echo.Echo
	svc  *staff.Service
	repo *domaintest.Repository
}

func newServer(t *testing.T) *testServer {
	t.Helper()
	parsed, err := httpserver.ParseAdminOperations(api.AdminSpec)
	if err != nil {
		t.Fatal(err)
	}
	var ops []staff.Operation
	for _, op := range parsed {
		ops = append(ops, staff.Operation{Method: op.Method, Route: op.Route, Permission: op.Permission, Tag: op.Tag})
	}
	repo := domaintest.NewRepository()
	svc := staff.NewService(repo, fakePasswords{}, uowtest.Immediate{},
		staff.Config{SessionIdleTimeout: 30 * time.Minute, SessionMaxAge: 12 * time.Hour, TempPasswordTTL: 24 * time.Hour}, ops)
	h := adminapi.NewHandler(svc, 12*time.Hour)
	e, err := httpserver.NewAdmin(httpserver.AdminDeps{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		HTTP:   config.HTTPConfig{BodyLimitBytes: 1 << 20, RequestTimeout: 5 * time.Second, GlobalRateLimitPerSecond: 1000},
		Origin: "https://admin.example.com",
		Routes: []func(*echo.Group){
			func(g *echo.Group) { adminapi.Register(g, h) },
			// 注册了但 admin.yaml 里没有的路由：权限映射查不到，必须拒绝。
			func(g *echo.Group) {
				g.GET("/unmapped", func(c *echo.Context) error { return c.NoContent(http.StatusOK) })
			},
		},
		Auth: []echo.MiddlewareFunc{adminapi.NewGuard(svc, ops)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &testServer{e: e, svc: svc, repo: repo}
}

type response struct {
	code   int
	body   map[string]any
	cookie *http.Cookie
}

func (s *testServer) do(t *testing.T, method, path, cookie, body string) response {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: adminapi.SessionCookie, Value: cookie})
	}
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	r := response{code: rec.Code}
	_ = json.Unmarshal(rec.Body.Bytes(), &r.body)
	for _, c := range rec.Result().Cookies() {
		if c.Name == adminapi.SessionCookie {
			r.cookie = c
		}
	}
	return r
}

func (s *testServer) login(t *testing.T, username, password string) string {
	t.Helper()
	r := s.do(t, http.MethodPost, "/admin/v1/auth/login", "", `{"username":"`+username+`","password":"`+password+`"}`)
	if r.code != http.StatusOK || r.cookie == nil {
		t.Fatalf("login %s = %d %v", username, r.code, r.body)
	}
	return r.cookie.Value
}

// createActive 创建员工并改掉临时密码，返回会话 cookie 值。
func (s *testServer) createActive(t *testing.T, username string, superAdmin bool) (string, string) {
	t.Helper()
	a, temp, err := s.svc.CreateAccount(t.Context(), staff.CreateAccountInput{Username: username, SuperAdmin: superAdmin})
	if err != nil {
		t.Fatal(err)
	}
	cookie := s.login(t, username, temp)
	if r := s.do(t, http.MethodPut, "/admin/v1/me/password", cookie, `{"current_password":"`+temp+`","new_password":"a-long-new-password"}`); r.code != http.StatusNoContent {
		t.Fatalf("change password = %d %v", r.code, r.body)
	}
	return a.ID, cookie
}

func code(r response) string {
	c, _ := r.body["code"].(string)
	return c
}

func TestGuard_SessionAndRouting(t *testing.T) {
	s := newServer(t)
	if r := s.do(t, http.MethodGet, "/admin/v1/me", "", ""); r.code != http.StatusUnauthorized || code(r) != "session_invalid" {
		t.Errorf("未登录 = %d %v, want 401 session_invalid", r.code, r.body)
	}
	if r := s.do(t, http.MethodGet, "/admin/v1/me", "forged", ""); r.code != http.StatusUnauthorized {
		t.Errorf("伪造的 cookie = %d, want 401", r.code)
	}
	if r := s.do(t, http.MethodGet, "/admin/v1/nosuch", "", ""); r.code != http.StatusNotFound {
		t.Errorf("不存在的路径 = %d %v, want 404", r.code, r.body)
	}
	_, cookie := s.createActive(t, "root", true)
	if r := s.do(t, http.MethodGet, "/admin/v1/unmapped", cookie, ""); r.code != http.StatusForbidden || code(r) != "permission_denied" {
		t.Errorf("admin.yaml 里没有映射的路由 = %d %v, want 403（查不到就拒绝，超级管理员也一样）", r.code, r.body)
	}
}

func TestLogin_SetsHardenedCookieAndLogsOnce(t *testing.T) {
	s := newServer(t)
	_, temp, _ := s.svc.CreateAccount(t.Context(), staff.CreateAccountInput{Username: "root", SuperAdmin: true})
	r := s.do(t, http.MethodPost, "/admin/v1/auth/login", "", `{"username":"root","password":"`+temp+`"}`)
	if r.code != http.StatusOK || r.body["must_change_password"] != true {
		t.Fatalf("login = %d %v", r.code, r.body)
	}
	c := r.cookie
	if c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
		t.Errorf("会话 cookie = %+v, want HttpOnly、Secure、SameSite=Strict、Path=/、无 Domain", c)
	}
	if n := len(s.repo.Audit()); n != 1 {
		t.Errorf("一次登录记 %d 行日志, want 1（public 操作不经中间件再记一次）", n)
	}
	if r := s.do(t, http.MethodPost, "/admin/v1/auth/login", "", `{"username":"root","password":"wrong"}`); r.code != http.StatusUnauthorized || code(r) != "invalid_credentials" {
		t.Errorf("密码错误 = %d %v", r.code, r.body)
	}
}

func TestLogin_ThrottledReturns429WithRetryAfter(t *testing.T) {
	s := newServer(t)
	for range 5 {
		if r := s.do(t, http.MethodPost, "/admin/v1/auth/login", "", `{"username":"nobody","password":"wrong"}`); code(r) != "invalid_credentials" {
			t.Fatalf("前 5 次 = %d %v, want invalid_credentials", r.code, r.body)
		}
	}
	r := s.do(t, http.MethodPost, "/admin/v1/auth/login", "", `{"username":"nobody","password":"wrong"}`)
	if r.code != http.StatusTooManyRequests || code(r) != "login_throttled" || r.body["retry_after"] != float64(30) {
		t.Errorf("连续失败 5 次后 = %d %v, want 429 login_throttled retry_after=30", r.code, r.body)
	}
}

func TestMustChangePassword_BlocksEverythingElse(t *testing.T) {
	s := newServer(t)
	_, temp, _ := s.svc.CreateAccount(t.Context(), staff.CreateAccountInput{Username: "root", SuperAdmin: true})
	cookie := s.login(t, "root", temp)

	if r := s.do(t, http.MethodGet, "/admin/v1/accounts", cookie, ""); r.code != http.StatusForbidden || code(r) != "password_change_required" {
		t.Errorf("改密前访问员工列表 = %d %v, want 403 password_change_required", r.code, r.body)
	}
	if r := s.do(t, http.MethodGet, "/admin/v1/me", cookie, ""); r.code != http.StatusOK {
		t.Errorf("改密前查看自己 = %d", r.code)
	}
	if r := s.do(t, http.MethodPut, "/admin/v1/me/password", cookie, `{"current_password":"`+temp+`","new_password":"a-long-new-password"}`); r.code != http.StatusNoContent {
		t.Fatalf("改密 = %d %v", r.code, r.body)
	}
	if r := s.do(t, http.MethodGet, "/admin/v1/accounts", cookie, ""); r.code != http.StatusOK {
		t.Errorf("改密后访问员工列表 = %d %v", r.code, r.body)
	}
}

func TestSuperAdminOnlyOperations(t *testing.T) {
	s := newServer(t)
	_, rootCookie := s.createActive(t, "root", true)
	_, aliceCookie := s.createActive(t, "alice", false)

	if r := s.do(t, http.MethodGet, "/admin/v1/accounts", aliceCookie, ""); r.code != http.StatusForbidden || code(r) != "permission_denied" {
		t.Errorf("非超级管理员调员工接口 = %d %v, want 403", r.code, r.body)
	}
	if r := s.do(t, http.MethodPost, "/admin/v1/roles", aliceCookie, `{"name":"x"}`); r.code != http.StatusForbidden {
		t.Errorf("非超级管理员调角色接口 = %d, want 403", r.code)
	}
	if r := s.do(t, http.MethodGet, "/admin/v1/me/permissions", aliceCookie, ""); r.code != http.StatusOK {
		t.Errorf("authenticated 操作 = %d", r.code)
	}
	r1 := s.do(t, http.MethodGet, "/admin/v1/accounts?limit=1", rootCookie, "")
	if r1.code != http.StatusOK || r1.body["active_super_admins"] != float64(1) {
		t.Errorf("第一页的 active_super_admins = %v, want 1（不受分页影响）", r1.body["active_super_admins"])
	}
	if next, _ := r1.body["next_cursor"].(string); next == "" {
		t.Error("应该有下一页")
	} else if r2 := s.do(t, http.MethodGet, "/admin/v1/accounts?limit=1&cursor="+next, rootCookie, ""); r2.code != http.StatusOK || r2.body["active_super_admins"] != nil {
		t.Errorf("翻页时不返回 active_super_admins，got %v", r2.body["active_super_admins"])
	}

	r := s.do(t, http.MethodPost, "/admin/v1/accounts", rootCookie, `{"username":"bob"}`)
	if r.code != http.StatusCreated || r.body["temporary_password"] == "" {
		t.Fatalf("创建员工 = %d %v", r.code, r.body)
	}
	bobID := r.body["account"].(map[string]any)["id"].(string)

	var writes []staff.AuditEntry
	for _, e := range s.repo.Audit() {
		if strings.HasPrefix(e.Action, "POST /admin/v1/accounts") || strings.HasPrefix(e.Action, "GET ") {
			writes = append(writes, e)
		}
	}
	if len(writes) != 1 || writes[0].Action != "POST /admin/v1/accounts" || writes[0].Result != "201" {
		t.Errorf("写操作日志 = %+v, want 一行 POST /admin/v1/accounts → 201，GET 不记", writes)
	}

	if r := s.do(t, http.MethodPost, "/admin/v1/accounts/"+bobID+"/disable", rootCookie, ""); r.code != http.StatusNoContent {
		t.Errorf("停用 = %d %v", r.code, r.body)
	}
	last := s.repo.Audit()[len(s.repo.Audit())-1]
	if last.Action != "POST /admin/v1/accounts/:id/disable" || last.Target != bobID || last.Result != "204" {
		t.Errorf("停用的日志 = %+v, want 路由模板 + 目标 ID + 204", last)
	}
	// 失败的写操作也记，结果是错误的状态码。
	if r := s.do(t, http.MethodPost, "/admin/v1/accounts", rootCookie, `{"username":"bob"}`); r.code != http.StatusConflict {
		t.Errorf("重复用户名 = %d", r.code)
	}
	if last := s.repo.Audit()[len(s.repo.Audit())-1]; last.Result != "409" {
		t.Errorf("失败的写操作日志 result = %q, want 409", last.Result)
	}
}

func TestDisableRevokesSessionsImmediately(t *testing.T) {
	s := newServer(t)
	_, rootCookie := s.createActive(t, "root", true)
	aliceID, aliceCookie := s.createActive(t, "alice", false)
	if r := s.do(t, http.MethodPost, "/admin/v1/accounts/"+aliceID+"/disable", rootCookie, ""); r.code != http.StatusNoContent {
		t.Fatal(r.code)
	}
	if r := s.do(t, http.MethodGet, "/admin/v1/me", aliceCookie, ""); r.code != http.StatusUnauthorized {
		t.Errorf("停用后旧会话 = %d, want 401", r.code)
	}
}

func TestLogoutAndSessions(t *testing.T) {
	s := newServer(t)
	_, c1 := s.createActive(t, "root", true)
	r := s.do(t, http.MethodGet, "/admin/v1/me/sessions", c1, "")
	items, _ := r.body["items"].([]any)
	if r.code != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["current"] != true ||
		items[0].(map[string]any)["status"] != "active" {
		t.Errorf("我的会话 = %d %v", r.code, r.body)
	}
	r = s.do(t, http.MethodPost, "/admin/v1/auth/logout", c1, "")
	if r.code != http.StatusNoContent || r.cookie == nil || r.cookie.MaxAge >= 0 {
		t.Errorf("登出 = %d, cookie = %+v, want 204 并清除 cookie", r.code, r.cookie)
	}
	if r := s.do(t, http.MethodGet, "/admin/v1/me", c1, ""); r.code != http.StatusUnauthorized {
		t.Errorf("登出后 = %d, want 401", r.code)
	}
}

func TestGuard_WrongMethodIs405(t *testing.T) {
	s := newServer(t)
	for _, path := range []string{"/admin/v1/accounts", "/admin/v1/roles/01936f3c-2b3c-7d4e-8f5a-6b7c8d9e0f1a"} {
		if r := s.do(t, http.MethodPatch, path, "", ""); r.code != http.StatusMethodNotAllowed {
			t.Errorf("PATCH %s = %d %v, want 405", path, r.code, r.body)
		}
	}
	if r := s.do(t, http.MethodGet, "/admin/v1/roles/x/y/z", "", ""); r.code != http.StatusNotFound {
		t.Errorf("不存在的路径 = %d, want 404", r.code)
	}
}

// 被拒绝的写操作也要留痕：越权尝试和必须改密时的写请求都记日志，结果是拒绝的状态码。
func TestGuard_AuditsDeniedWrites(t *testing.T) {
	s := newServer(t)
	aliceID, aliceCookie := s.createActive(t, "alice", false)
	if r := s.do(t, http.MethodPost, "/admin/v1/accounts/"+aliceID+"/disable", aliceCookie, ""); r.code != http.StatusForbidden {
		t.Fatalf("越权写 = %d", r.code)
	}
	last := s.repo.Audit()[len(s.repo.Audit())-1]
	if last.StaffID != aliceID || last.Action != "POST /admin/v1/accounts/:id/disable" || last.Result != "403" {
		t.Errorf("越权尝试的日志 = %+v, want alice、路由模板、403", last)
	}

	_, temp, _ := s.svc.CreateAccount(t.Context(), staff.CreateAccountInput{Username: "bob"})
	bobCookie := s.login(t, "bob", temp)
	before := len(s.repo.Audit())
	if r := s.do(t, http.MethodPost, "/admin/v1/roles", bobCookie, `{"name":"x"}`); r.code != http.StatusForbidden {
		t.Fatalf("改密前的写请求 = %d", r.code)
	}
	if got := s.repo.Audit(); len(got) != before+1 || got[len(got)-1].Result != "403" {
		t.Errorf("改密前被拦下的写请求应记一行 403")
	}
	// 未登录的写请求不知道是谁，不记。
	before = len(s.repo.Audit())
	s.do(t, http.MethodPost, "/admin/v1/roles", "", `{"name":"x"}`)
	if len(s.repo.Audit()) != before {
		t.Error("未登录的写请求不应记日志")
	}
}

// 员工列表带每个员工的角色 ID，页面据此提示"未分配角色"；没有角色时是空数组。
func TestListAccounts_IncludesRoleIDs(t *testing.T) {
	s := newServer(t)
	_, root := s.createActive(t, "root", true)
	r := s.do(t, http.MethodPost, "/admin/v1/roles", root, `{"name":"运营"}`)
	roleID, _ := r.body["id"].(string)
	aliceID, _ := s.createActive(t, "alice", false)
	if r := s.do(t, http.MethodPut, "/admin/v1/accounts/"+aliceID+"/roles", root, `{"role_ids":["`+roleID+`"]}`); r.code != http.StatusNoContent {
		t.Fatalf("分配角色 = %d %v", r.code, r.body)
	}
	r = s.do(t, http.MethodGet, "/admin/v1/accounts", root, "")
	got := map[string]any{}
	for _, it := range r.body["items"].([]any) {
		m := it.(map[string]any)
		got[m["username"].(string)] = m["role_ids"]
	}
	if ids, _ := got["alice"].([]any); len(ids) != 1 || ids[0] != roleID {
		t.Errorf("alice 的 role_ids = %v, want [%s]", got["alice"], roleID)
	}
	if ids, ok := got["root"].([]any); !ok || len(ids) != 0 {
		t.Errorf("root 的 role_ids = %#v, want 空数组（不是 null）", got["root"])
	}
}
