package app

import (
	"slices"
	"testing"

	"app.local/internal/kit/lifecycle"
	_ "app.local/internal/modules/staff"
	"app.local/internal/platform/metrics"
	"app.local/internal/platform/process"
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

// Infra.Metrics 属于进程，恰好由一个端口输出：前台输出它，后台用自己的 Registry。两个端口都输出会让按服务求和算重，
// 都不输出则看不到运行时和业务指标（见 observability.md 的"两种 Registry 的归属不同"）。
func TestServerDeps_ProcessRegistryOnExactlyOnePort(t *testing.T) {
	in := newZeroInfra()
	in.Metrics = metrics.NewRuntimeRegistry(process.New("api", "test"))
	mods := buildModules(in, testModuleConfigs(t))
	if apiServerDeps(in, mods).Metrics != in.Metrics {
		t.Error("前台端口应输出进程的 Registry（Infra.Metrics）")
	}
	if adminMetrics := adminServerDeps(in, mods).Metrics; adminMetrics == nil || adminMetrics == in.Metrics {
		t.Error("后台端口应使用自己的 Registry，不能再输出 Infra.Metrics")
	}
}
