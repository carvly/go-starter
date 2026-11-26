package app

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"time"

	"github.com/labstack/echo/v5"

	"app.local/internal/config"
	"app.local/internal/kit/lifecycle"
	"app.local/internal/platform/httpserver"
	"app.local/internal/platform/metrics"
	"app.local/internal/platform/ratelimit"
	"app.local/internal/platform/systemd"
)

// shutdownTimeout 是排空之后的关停上限：要大于 HTTP 优雅关闭的 15s 和队列的强制停止，给它们留出完成的时间。
// worker 没有网关转来的流量，不排空，直接用它；api 是排空期 + 它（apiShutdownTimeout）。进程最后还有 tracing
// 的关停（最多 3s，在组件停下之后，见 Infra.Close），systemd 的 TimeoutStopSec 要大于三者之和。
const shutdownTimeout = 20 * time.Second

func apiShutdownTimeout(cfg *config.Config) time.Duration {
	return cfg.HTTP.ShutdownDrain + shutdownTimeout
}

// RunAPI 运行 HTTP API 进程，阻塞到 ctx 取消或组件出错。
// 日志写 stdout。socket activation（HTTP_SOCKET_ACTIVATION）时先取得 systemd 传来的监听 socket，组件构造完、
// 依赖检查通过后才向 systemd 发 READY=1：两个实例共享端口，外部探测分不清实例，READY 是唯一的就绪信号。
func RunAPI(ctx context.Context, cfg *config.Config) error {
	id, log := newProcess(cfg, "api", os.Stdout)
	configs, err := loadModuleConfigs(cfg.Source)
	if err != nil {
		return err
	}
	log.InfoContext(ctx, "starting api", slog.Any("config", configs.Redacted(cfg)))
	lns := &apiListeners{}
	if cfg.HTTP.SocketActivation {
		if lns, err = inheritListeners(cfg.HTTP); err != nil {
			return err
		}
	}
	defer lns.close()
	in, err := NewInfra(ctx, cfg, id, log)
	if err != nil {
		return err
	}
	defer in.Close(ctx)

	mods := buildModules(in, configs)
	if !cfg.HTTP.SocketActivation {
		if err := bindListeners(lns, cfg.HTTP, adminEnabled(in, mods.adminRegistrars()), internalEnabled(in, mods.internalRegistrars())); err != nil {
			return err
		}
	}
	components, err := apiComponents(in, mods, lns)
	if err != nil {
		return err
	}
	if cfg.HTTP.SocketActivation {
		if err := waitDependencies(ctx, in.Pingers, log); err != nil {
			return err
		}
		if err := systemd.Notify(systemd.Ready); err != nil {
			return fmt.Errorf("app: %w", err)
		}
		log.InfoContext(ctx, "api ready: notified systemd")
	}
	return lifecycle.Run(ctx, apiShutdownTimeout(cfg), components...)
}

// bindListeners 是自己绑定端口的部署（非 socket activation）：前台总是绑定；后台、内部端口只在启用时绑定。
func bindListeners(lns *apiListeners, cfg config.HTTPConfig, adminOn, internalOn bool) error {
	var err error
	if lns.front, err = httpserver.Listen(cfg.Addr); err != nil {
		return fmt.Errorf("app: %w", err)
	}
	if adminOn {
		if lns.admin, err = httpserver.Listen(cfg.AdminAddr); err != nil {
			return fmt.Errorf("app: %w", err)
		}
	}
	if internalOn {
		if lns.internal, err = httpserver.Listen(cfg.APIInternalAddr); err != nil {
			return fmt.Errorf("app: %w", err)
		}
	}
	return nil
}

// apiComponents 是 api 进程的 HTTP server：前台总是启动；后台、内部端口只在配置了地址且有对应路由时启动。lns 是
// 已就绪的监听 socket：socket activation 时来自 systemd，否则由 bindListeners 绑定。
func apiComponents(in *Infra, mods *Modules, lns *apiListeners) ([]lifecycle.Component, error) {
	socket := in.Config.HTTP.SocketActivation
	adminOn := adminEnabled(in, mods.adminRegistrars())
	internalOn := internalEnabled(in, mods.internalRegistrars())
	if err := checkOptionalSockets(
		optionalSocket{name: "admin", env: "HTTP_ADMIN_ADDR", enabled: adminOn, ln: lns.admin},
		optionalSocket{name: "internal", env: "HTTP_API_INTERNAL_ADDR", enabled: internalOn, ln: lns.internal},
	); err != nil {
		return nil, err
	}
	e, err := httpserver.New(apiServerDeps(in, mods))
	if err != nil {
		return nil, fmt.Errorf("app: build http server: %w", err)
	}
	var adminServer *echo.Echo
	adminDeps := adminServerDeps(in, mods)
	if adminOn {
		if adminServer, err = httpserver.NewAdmin(adminDeps); err != nil {
			return nil, fmt.Errorf("app: build admin server: %w", err)
		}
	}

	// socket activation 时 socket 由轮流上线的实例共享：收到停止信号立即不再 accept，新连接由接班的实例接走。
	stop := httpserver.Stop{Drain: in.Config.HTTP.ShutdownDrain, SharedListener: socket}
	components := []lifecycle.Component{httpserver.Component("api", e, lns.front, stop)}
	if adminOn {
		components = append(components, httpserver.Component("admin", adminServer, lns.admin, stop))
	}
	if internalOn {
		internalServer, err := httpserver.NewInternalAPI(httpserver.InternalDeps{
			Logger:         in.Logger,
			HTTP:           in.Config.HTTP,
			RateLimitStore: ratelimit.NewStore(in.Redis, "internal", globalRate(in), in.Logger),
			Routes:         mods.internalRegistrars(),
			Metrics:        in.Metrics,
			Tracer:         in.Tracer,
		})
		if err != nil {
			return nil, fmt.Errorf("app: build internal server: %w", err)
		}
		components = append(components, httpserver.Component("internal", internalServer, lns.internal, stop))
	}
	return components, nil
}

func adminEnabled(in *Infra, routes []func(*echo.Group)) bool {
	return in.Config.HTTP.AdminAddr != "" && len(routes) > 0
}

// internalEnabled：内部端口只在配置了 HTTP_API_INTERNAL_ADDR 且有模块注册内部接口时启动。
func internalEnabled(in *Infra, routes []func(*echo.Group)) bool {
	return in.Config.HTTP.APIInternalAddr != "" && len(routes) > 0
}

func apiServerDeps(in *Infra, mods *Modules) httpserver.Deps {
	return httpserver.Deps{
		Logger:         in.Logger,
		HTTP:           in.Config.HTTP,
		CORS:           in.Config.CORS,
		Pingers:        in.Pingers,
		RateLimitStore: ratelimit.NewStore(in.Redis, "global", globalRate(in), in.Logger),
		Routes:         mods.httpRegistrars(),
		Metrics:        in.Metrics,
		Tracer:         in.Tracer,
	}
}

func adminServerDeps(in *Infra, mods *Modules) httpserver.AdminDeps {
	return httpserver.AdminDeps{
		Logger:         in.Logger,
		HTTP:           in.Config.HTTP,
		Origin:         in.Config.HTTP.AdminOrigin,
		RateLimitStore: ratelimit.NewStore(in.Redis, "admin", globalRate(in), in.Logger),
		Routes:         mods.adminRegistrars(),
		Auth:           mods.adminAuth(),
		Static:         adminStatic,
		Metrics:        metrics.NewRegistry(in.Identity),
		Tracer:         in.Tracer,
		Pingers:        in.Pingers,
	}
}

// adminStatic 是后台前端的构建产物，由 admin_ui.go 设置；没有那个文件时为 nil，不托管前端。
var adminStatic fs.FS

// globalRate 是按 IP 的全局限流额度，前台和后台各自计数、额度相同；取值由 config 校验为正数。
func globalRate(in *Infra) ratelimit.Limit {
	return ratelimit.PerSecond(in.Config.HTTP.GlobalRateLimitPerSecond)
}
