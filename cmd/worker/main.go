// Command worker 是后台任务入口（消费队列、跑定时任务）。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"app.local/internal/app"
	"app.local/internal/config"
	"app.local/internal/platform/healthcheck"
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := app.RunWorker(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "worker:", err)
		os.Exit(1)
	}
}
