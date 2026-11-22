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
)

// shutdownTimeout 是排空之后的关停上限：要大于 HTTP 优雅关闭的 15s 和队列的强制停止，给它们留出完成的时间。
// worker 没有网关转来的流量，不排空，直接用它；api 是排空期 + 它（apiShutdownTimeout）。进程最后还有 tracing
// 的关停（最多 3s，在组件停下之后，见 Infra.Close），systemd 的 TimeoutStopSec 要大于三者之和。
const shutdownTimeout = 20 * time.Second

func apiShutdownTimeout(cfg *config.Config) time.Duration {
	return cfg.HTTP.ShutdownDrain + shutdownTimeout
}

// RunAPI 运行 HTTP API 进程，阻塞到 ctx 取消或组件出错。
// 日志写 stdout。
func RunAPI(ctx context.Context, cfg *config.Config) error {
	id, log := newProcess(cfg, "api", os.Stdout)
	configs, err := loadModuleConfigs(cfg.Source)
	if err != nil {
		return err
	}
	log.InfoContext(ctx, "starting api", slog.Any("config", configs.Redacted(cfg)))
	in, err := NewInfra(ctx, cfg, id, log)
	if err != nil {
		return err
	}
	defer in.Close(ctx)

	components, err := apiComponents(in, buildModules(in, configs))
	if err != nil {
		return err
	}
	return lifecycle.Run(ctx, apiShutdownTimeout(cfg), components...)
}

// apiComponents 是 api 进程的 HTTP server：前台总是启动；后台只在配置了地址时启动，
// 未配置时不监听后台端口。
func apiComponents(in *Infra, mods *Modules) ([]lifecycle.Component, error) {
	e, err := httpserver.New(apiServerDeps(in, mods))
	if err != nil {
		return nil, fmt.Errorf("app: build http server: %w", err)
	}
	drain := in.Config.HTTP.ShutdownDrain
	components := []lifecycle.Component{httpserver.Component("api", e, in.Config.HTTP.Addr, drain)}

	if adminEnabled(in, mods.adminRegistrars()) {
		admin, err := httpserver.NewAdmin(adminServerDeps(in, mods))
		if err != nil {
			return nil, fmt.Errorf("app: build admin server: %w", err)
		}
		components = append(components, httpserver.Component("admin", admin, in.Config.HTTP.AdminAddr, drain))
	}
	return components, nil
}

func adminEnabled(in *Infra, routes []func(*echo.Group)) bool {
	return in.Config.HTTP.AdminAddr != "" && len(routes) > 0
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
