//go:build integration

package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"app.local/internal/platform/cli"
	"app.local/internal/platform/dbtx"
	"app.local/internal/platform/httpserver"
	"app.local/internal/platform/postgres/pgtest"
)

// 后台员工体系的验收流程，跑在真实 Postgres 和真实的组合根上：CLI 建出的超级管理员能
// 登录并被强制改密；非超级管理员调员工接口 403；停用后旧会话立即失效；CLI 能恢复密码；
// 每个写操作都有日志，登录只记一行。
func TestStaff_AcceptanceFlow(t *testing.T) {
	in := newZeroInfra()
	pool := pgtest.Pool(t, migrationSources()...)
	in.Pool, in.DBTx = pool, dbtx.NewHandle(pool)
	in.Config.HTTP.AdminOrigin = "https://admin.example.com"
	configs := testModuleConfigs(t)
	configs.Staff.SessionIdleTimeout = 30 * time.Minute
	configs.Staff.SessionMaxAge = 12 * time.Hour
	configs.Staff.TempPasswordTTL = 24 * time.Hour
	configs.Staff.AuditRetention = 180 * 24 * time.Hour
	mods := buildModules(in, configs)
	e, err := httpserver.NewAdmin(adminServerDeps(in, mods))
	if err != nil {
		t.Fatal(err)
	}

	runCLI := func(args ...string) string {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := cli.Run(t.Context(), "api", commands(mods), args, &out, &errOut); code != cli.ExitOK {
			t.Fatalf("api %v = %d: %s", args, code, errOut.String())
		}
		s := out.String()
		return strings.TrimSpace(s[strings.LastIndex(s, "：")+len("："):])
	}
	call := func(method, path, cookie, body string) (int, map[string]any, string) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-admin_session", Value: cookie})
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		session := ""
		for _, c := range rec.Result().Cookies() {
			if c.Name == "__Host-admin_session" {
				session = c.Value
			}
		}
		return rec.Code, m, session
	}
	login := func(user, pw string) string {
		t.Helper()
		code, body, session := call(http.MethodPost, "/admin/v1/auth/login", "", `{"username":"`+user+`","password":"`+pw+`"}`)
		if code != http.StatusOK || session == "" {
			t.Fatalf("login %s = %d %v", user, code, body)
		}
		return session
	}
	changePassword := func(cookie, current, next string) {
		t.Helper()
		if code, body, _ := call(http.MethodPut, "/admin/v1/me/password", cookie, `{"current_password":"`+current+`","new_password":"`+next+`"}`); code != http.StatusNoContent {
			t.Fatalf("change password = %d %v", code, body)
		}
	}

	temp := runCLI("staff", "create", "-username", "root")
	root := login("root", temp)
	if code, body, _ := call(http.MethodGet, "/admin/v1/accounts", root, ""); code != http.StatusForbidden || body["code"] != "password_change_required" {
		t.Fatalf("改密前 = %d %v", code, body)
	}
	changePassword(root, temp, "root-long-password")

	code, body, _ := call(http.MethodPost, "/admin/v1/accounts", root, `{"username":"alice","display_name":"Alice"}`)
	if code != http.StatusCreated {
		t.Fatalf("create alice = %d %v", code, body)
	}
	aliceID := body["account"].(map[string]any)["id"].(string)
	alice := login("alice", body["temporary_password"].(string))
	changePassword(alice, body["temporary_password"].(string), "alice-long-password")

	if code, body, _ := call(http.MethodGet, "/admin/v1/accounts", alice, ""); code != http.StatusForbidden || body["code"] != "permission_denied" {
		t.Errorf("非超级管理员调员工接口 = %d %v, want 403", code, body)
	}
	if code, _, _ := call(http.MethodGet, "/admin/v1/nosuch", alice, ""); code != http.StatusNotFound {
		t.Errorf("不存在的路径 = %d, want 404", code)
	}
	if code, body, _ := call(http.MethodPost, "/admin/v1/accounts/"+aliceID+"/disable", root, ""); code != http.StatusNoContent {
		t.Fatalf("disable = %d %v", code, body)
	}
	if code, _, _ := call(http.MethodGet, "/admin/v1/me", alice, ""); code != http.StatusUnauthorized {
		t.Errorf("停用后旧会话 = %d, want 401", code)
	}

	newTemp := runCLI("staff", "reset-password", "-username", "root")
	if code, _, _ := call(http.MethodGet, "/admin/v1/me", root, ""); code != http.StatusUnauthorized {
		t.Errorf("CLI 重设密码后旧会话 = %d, want 401", code)
	}
	root = login("root", newTemp)
	changePassword(root, newTemp, "root-another-password")

	code, body, _ = call(http.MethodGet, "/admin/v1/audit-logs?limit=100", root, "")
	if code != http.StatusOK {
		t.Fatalf("audit logs = %d %v", code, body)
	}
	counts := map[string]int{}
	for _, it := range body["items"].([]any) {
		counts[it.(map[string]any)["action"].(string)]++
	}
	for action, want := range map[string]int{
		"cli:staff create":                    1,
		"cli:staff reset-password":            1,
		"login_succeeded":                     3,
		"POST /admin/v1/accounts":             1,
		"POST /admin/v1/accounts/:id/disable": 1,
		"PUT /admin/v1/me/password":           3,
		"POST /admin/v1/auth/login":           0,
		"GET /admin/v1/accounts":              0,
	} {
		if counts[action] != want {
			t.Errorf("日志 %q 有 %d 条, want %d（全部：%v）", action, counts[action], want, counts)
		}
	}
}
