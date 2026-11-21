// Command worker 是后台任务入口（消费队列、跑定时任务）。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"app.local/internal/app"
	"app.local/internal/config"
	"app.local/internal/platform/healthcheck"
	"app.local/internal/platform/logger"
)

func main() {
	// 本地开发用 .env；生产环境没有这个文件，找不到就忽略。
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "worker: load config:", err)
		os.Exit(1)
	}

	// distroless 镜像里没有 curl，HEALTHCHECK 用这个子命令复用同一个二进制探活。
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck.Run(cfg.HTTP.InternalAddr))
	}

	log := logger.New(cfg.LogLevel, os.Stdout, logger.Process("worker", cfg.InstanceID)...)
	// 没有绑定 request 的代码路径（比如 river 执行任务时）经 logctx.From 拿到的是
	// slog.Default()，设成同一个 logger，格式和级别才一致。
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := app.RunWorker(ctx, cfg, log); err != nil {
		fmt.Fprintln(os.Stderr, "worker:", err)
		os.Exit(1)
	}
}
