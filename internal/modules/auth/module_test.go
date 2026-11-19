package auth

import (
	"context"
	"slices"
	"testing"
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

func TestModule_AddUserDataCleaner(t *testing.T) {
	m := &Module{}
	called := false
	m.AddUserDataCleaner(cleanerFunc(func() { called = true }))
	if len(m.userDataCleaners) != 1 {
		t.Fatalf("cleaners = %d, want 1", len(m.userDataCleaners))
	}
	if err := m.userDataCleaners[0].DeleteUserData(t.Context(), "u1"); err != nil || !called {
		t.Fatalf("DeleteUserData called = %v, err = %v", called, err)
	}
}
