package app

import (
	"slices"
	"testing"

	"app.local/internal/kit/lifecycle"
	_ "app.local/internal/modules/staff"
)

func componentNames(cs []lifecycle.Component) []string {
	var names []string
	for _, c := range cs {
		names = append(names, c.Name())
	}
	return names
}

func TestAPIComponents_AdminOnlyWhenConfigured(t *testing.T) {
	in := newZeroInfra()
	cs, err := apiComponents(in, buildModules(in, testModuleConfigs(t)))
	if err != nil {
		t.Fatal(err)
	}
	if got := componentNames(cs); !slices.Equal(got, []string{"api"}) {
		t.Errorf("未配置 HTTP_ADMIN_ADDR 时 components = %v, want [api]（不监听后台端口）", got)
	}

	in = newZeroInfra()
	in.Config.HTTP.AdminAddr = "127.0.0.1:0"
	in.Config.HTTP.AdminOrigin = "https://admin.example.com"
	cs, err = apiComponents(in, buildModules(in, testModuleConfigs(t)))
	if err != nil {
		t.Fatal(err)
	}
	if got := componentNames(cs); !slices.Equal(got, []string{"api", "admin"}) {
		t.Errorf("配置了 HTTP_ADMIN_ADDR 时 components = %v, want [api admin]", got)
	}

	if adminEnabled(in, nil) {
		t.Error("没有后台接入模块时仍启用了后台监听")
	}
}
