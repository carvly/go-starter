package user

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/labstack/echo/v5"

	"app.local/internal/kit/errs"
	"app.local/internal/modules/user/domain"
)

func TestWeChatRegistry_OnlyEnabledEntries(t *testing.T) {
	cred := WeChatAppCredentials{AppID: "appid", Secret: "secret"}
	r := wechatRegistry(WeChatConfig{MP: cred, Mini: cred})

	var names []string
	for name := range r {
		names = append(names, name)
	}
	slices.Sort(names)
	if want := []string{"wechat-miniprogram", "wechat-mp"}; !slices.Equal(names, want) {
		t.Fatalf("registry entries = %v, want %v", names, want)
	}
	if _, ok := r.Browser("wechat-web"); ok {
		t.Fatal("Browser(wechat-web) ok = true, want false（未配置的入口不能登记）")
	}
	if _, ok := r.NativeEntry("wechat-app"); ok {
		t.Fatal("NativeEntry(wechat-app) ok = true, want false（未配置的入口不能登记）")
	}
}

type cleanerFunc func()

func (f cleanerFunc) DeleteUserData(context.Context, string) error {
	f()
	return nil
}

func TestModule_AddDataCleaner(t *testing.T) {
	m := &Module{}
	called := false
	m.AddDataCleaner(cleanerFunc(func() { called = true }))
	if len(m.dataCleaners) != 1 {
		t.Fatalf("cleaners = %d, want 1", len(m.dataCleaners))
	}
	if err := m.dataCleaners[0].DeleteUserData(t.Context(), "u1"); err != nil || !called {
		t.Fatalf("DeleteUserData called = %v, err = %v", called, err)
	}
}

// 配置了 USER_TRUSTED_IDENTITY_HEADER 时，前台鉴权信任网关透传的 header，不再要求 bearer token。
func TestModule_MiddlewareFollowsTrustedIdentityConfig(t *testing.T) {
	ok := func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) }
	call := func(mw echo.MiddlewareFunc, header string) int {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			req.Header.Set("X-Gateway-User", header)
		}
		rec := httptest.NewRecorder()
		err := mw(ok)(e.NewContext(req, rec))
		var he *echo.HTTPError
		if errors.As(err, &he) {
			return he.Code
		}
		if be, ok := errs.As(err); ok && be.Kind == errs.KindUnauthorized {
			return http.StatusUnauthorized
		}
		return rec.Code
	}
	trusted := &Module{cfg: Config{TrustedIdentityHeader: "X-Gateway-User"}}
	if got := call(trusted.Middleware(), "u-1"); got != http.StatusNoContent {
		t.Errorf("带网关 header = %d, want 204", got)
	}
	if got := call(trusted.Middleware(), ""); got != http.StatusUnauthorized {
		t.Errorf("缺网关 header = %d, want 401", got)
	}
	session := &Module{cfg: Config{JWTKeyID: "kid", JWTSigningKey: "secret"}}
	if got := call(session.Middleware(), "u-1"); got != http.StatusUnauthorized {
		t.Errorf("未配置时只认 bearer token，带网关 header = %d, want 401", got)
	}
}

// 每个原生入口都要登记会话属于哪个客户端，否则登录时一律当作未知入口。
func TestNativeClients_CoverEveryNativeEntry(t *testing.T) {
	cred := WeChatAppCredentials{AppID: "appid", Secret: "secret"}
	r := wechatRegistry(WeChatConfig{Web: cred, MP: cred, Mini: cred, App: cred})
	for name, entry := range r {
		if entry.Native == nil {
			continue
		}
		if _, ok := nativeClients[name]; !ok {
			t.Errorf("原生入口 %s 没有登记在 nativeClients 里", name)
		}
	}
	for name := range nativeClients {
		if _, ok := r.NativeEntry(name); !ok {
			t.Errorf("nativeClients 登记了不存在的原生入口 %s", name)
		}
	}
}

// 配置启用了内置策略时，再登记自定义策略必须在启动时失败，不能悄悄覆盖。
func TestSetSessionPolicy_ConflictsWithConfiguredPolicy(t *testing.T) {
	m := New(Deps{Config: Config{JWTSigningKey: "k", JWTKeyID: "kid", SessionSlotLimit: 1}})
	defer func() {
		if recover() == nil {
			t.Error("配置启用内置策略后 SetSessionPolicy 应 panic")
		}
	}()
	m.SetSessionPolicy(domain.NewSlotLimit(2))
}
