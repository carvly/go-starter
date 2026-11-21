package queue

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"app.local/internal/platform/logger"
)

// NewClient 在 workers 为 nil 时构造只入队、不消费的 client（periodic 被
// 忽略）；worker 进程传入注册好的 workers 并调用 Start。定时任务用 river 自带的
// PeriodicJob，不另接 cron 库。
func NewClient(pool *pgxpool.Pool, workers *Workers, periodic []Periodic, log *slog.Logger) (*river.Client[pgx.Tx], error) {
	client, err := river.NewClient(riverpgxv5.New(pool), newConfig(workers, periodic, log))
	if err != nil {
		return nil, fmt.Errorf("queue: create river client: %w", err)
	}
	return client, nil
}

func newConfig(workers *Workers, periodic []Periodic, log *slog.Logger) *river.Config {
	if log == nil {
		log = slog.Default()
	}
	// 不设置时 river 用自己的文本格式 logger 写 stdout，和应用的 JSON 日志混在一起。
	log = logger.Ensure(log)
	cfg := &river.Config{
		Logger:     log,
		Middleware: []rivertype.Middleware{&insertMiddleware{}, &workMiddleware{log: log}},
	}
	if workers != nil {
		cfg.Workers = workers.river
		cfg.Queues = map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 100},
		}
		for _, p := range periodic {
			cfg.PeriodicJobs = append(cfg.PeriodicJobs, p.riverJob())
		}
		// 停止时等正在执行的任务最多这么久，之后取消它们的 ctx；要小于进程的关停超时。
		cfg.SoftStopTimeout = 10 * time.Second
	}
	return cfg
}
