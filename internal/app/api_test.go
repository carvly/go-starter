package app

import (
	"slices"
	"strings"
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
	cs, err := apiComponents(in, buildModules(in, testModuleConfigs(t)), &apiListeners{front: testListener(t)})
	if err != nil {
		t.Fatal(err)
	}
	if got := componentNames(cs); !slices.Equal(got, []string{"api"}) {
		t.Errorf("未配置 HTTP_ADMIN_ADDR 时 components = %v, want [api]（不监听后台端口）", got)
	}

	in = newZeroInfra()
	in.Config.HTTP.AdminAddr = "127.0.0.1:0"
	in.Config.HTTP.AdminOrigin = "https://admin.example.com"
	cs, err = apiComponents(in, buildModules(in, testModuleConfigs(t)), &apiListeners{front: testListener(t), admin: testListener(t)})
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

// socket activation 时 systemd 传来的后台 socket 有无必须与后台是否启动一致。
func TestAPIComponents_SocketActivation(t *testing.T) {
	newIn := func(admin bool) *Infra {
		in := newZeroInfra()
		in.Config.HTTP.SocketActivation = true
		if admin {
			in.Config.HTTP.AdminAddr = "127.0.0.1:8081"
			in.Config.HTTP.AdminOrigin = "https://admin.example.com"
		}
		return in
	}
	in := newIn(true)
	cs, err := apiComponents(in, buildModules(in, testModuleConfigs(t)), &apiListeners{front: testListener(t), admin: testListener(t)})
	if err != nil {
		t.Fatal(err)
	}
	if got := componentNames(cs); !slices.Equal(got, []string{"api", "admin"}) {
		t.Errorf("components = %v, want [api admin]", got)
	}

	for _, tc := range []struct {
		name    string
		admin   bool
		lns     *apiListeners
		wantErr string
	}{
		{"后台启用但没有后台 socket", true, &apiListeners{front: testListener(t)}, "add ListenStream="},
		{"后台未启用却有后台 socket", false, &apiListeners{front: testListener(t), admin: testListener(t)}, "remove that ListenStream="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := newIn(tc.admin)
			_, err := apiComponents(in, buildModules(in, testModuleConfigs(t)), tc.lns)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want 含 %q", err, tc.wantErr)
			}
		})
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

// 内部端口：配置了 HTTP_API_INTERNAL_ADDR 且有模块注册内部接口时才启动（删除了提供内部接口的模块时不启动）；它的 HTTP
// 指标注册进进程的 Registry（server="internal"），与前台的同名指标共存。socket activation 时 socket 有无与是否启动一致。
func TestAPIComponents_InternalPort(t *testing.T) {
	in := newZeroInfra()
	in.Metrics = metrics.NewRuntimeRegistry(process.New("api", "test"))
	in.Config.HTTP.APIInternalAddr = "127.0.0.1:0"
	mods := buildModules(in, testModuleConfigs(t))
	hasRoutes := len(mods.internalRegistrars()) > 0
	lns := &apiListeners{front: testListener(t)}
	want := []string{"api"}
	if hasRoutes {
		lns.internal = testListener(t)
		want = append(want, "internal")
	}
	cs, err := apiComponents(in, mods, lns)
	if err != nil {
		t.Fatal(err)
	}
	if got := componentNames(cs); !slices.Equal(got, want) {
		t.Errorf("components = %v, want %v", got, want)
	}

	// socket activation：socket 有无与"是否启动"不一致时启动失败。
	in = newZeroInfra()
	in.Config.HTTP.SocketActivation = true
	in.Config.HTTP.APIInternalAddr = "127.0.0.1:8082"
	mismatched := &apiListeners{front: testListener(t)}
	if !hasRoutes {
		mismatched.internal = testListener(t)
	}
	if _, err := apiComponents(in, buildModules(in, testModuleConfigs(t)), mismatched); err == nil ||
		!strings.Contains(err.Error(), "internal") {
		t.Errorf("内部端口的 socket 与是否启动不一致：err = %v", err)
	}
}
