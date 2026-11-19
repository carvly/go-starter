package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/riverqueue/river"

	"app.local/internal/config"
	"app.local/internal/kit/lifecycle"
	"app.local/internal/platform/httpserver"
	"app.local/internal/platform/queue"
)

// RunWorker 运行后台任务进程：river 消费 client + 只有 /healthz、/metrics 的
// 内部 server，任一组件异常退出都会让整个进程以非零退出码结束。
func RunWorker(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	configs, err := loadModuleConfigs(cfg.Source)
	if err != nil {
		return err
	}
	logger.Info("starting worker", "config", configs.Redacted(cfg))
	in, err := NewInfra(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer in.Close()

	mods := buildModules(in, configs)
	queue.WarnOrphans(ctx, in.Pool, workerKinds(), logger)
	workClient, err := queue.NewClient(in.Pool, newWorkers(mods), periodicJobs(mods))
	if err != nil {
		return fmt.Errorf("app: build queue worker client: %w", err)
	}

	return lifecycle.Run(ctx, shutdownTimeout,
		queue.Component(workClient),
		httpserver.Component("worker internal", httpserver.NewInternal(logger), cfg.HTTP.InternalAddr),
	)
}

func newWorkers(mods *Modules) *river.Workers {
	workers := river.NewWorkers()
	for _, register := range mods.jobRegistrars() {
		register(workers)
	}
	return workers
}

// periodicJobs 汇总各模块的 PeriodicJobs()。这里不写具体任务：任务由模块提供，
// 它的 worker 在同一个模块的 RegisterJobs 里注册。
func periodicJobs(mods *Modules) []queue.Periodic {
	var all []queue.Periodic
	for _, jobs := range mods.periodicJobRegistrars() {
		all = append(all, jobs()...)
	}
	return all
}
