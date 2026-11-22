package queue

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"github.com/riverqueue/rivercontrib/otelriver"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"

	"app.local/internal/platform/logger"
)

// NewClient 在 workers 为 nil 时构造只入队、不消费的 client（periodic 被
// 忽略）；worker 进程传入注册好的 workers 并调用 Start。定时任务用 river 自带的
// PeriodicJob，不另接 cron 库。tp 必填：不导出时也由它生成任务的 trace_id。
func NewClient(pool *pgxpool.Pool, workers *Workers, periodic []Periodic, log *slog.Logger, tp trace.TracerProvider) (*river.Client[pgx.Tx], error) {
	if tp == nil {
		return nil, errors.New("queue: TracerProvider is required")
	}
	client, err := river.NewClient(riverpgxv5.New(pool), newConfig(workers, periodic, log, tp))
	if err != nil {
		return nil, fmt.Errorf("queue: create river client: %w", err)
	}
	return client, nil
}

func newConfig(workers *Workers, periodic []Periodic, log *slog.Logger, tp trace.TracerProvider) *river.Config {
	if log == nil {
		log = slog.Default()
	}
	// 不设置时 river 用自己的文本格式 logger 写 stdout，和应用的 JSON 日志混在一起。
	log = logger.Ensure(log)
	cfg := &river.Config{
		Logger: log,
		// otelriver 在前（外层）：入队时把 traceparent 写进 metadata，执行时建 work span 并 link 回入队的请求；
		// workMiddleware 在它之内，日志拿得到 work span。指标走 Prometheus：MeterProvider 不传时它退回 otel
		// 全局 provider，有人设置全局 provider 后会多出一套重复的队列指标，所以显式传 noop。
		Middleware: []rivertype.Middleware{
			otelriver.NewMiddleware(&otelriver.MiddlewareConfig{
				TracerProvider:              tp,
				MeterProvider:               metricnoop.NewMeterProvider(),
				EnableTracePropagation:      true,
				EnableWorkSpanJobKindSuffix: true,
			}),
			&workMiddleware{log: log},
		},
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
