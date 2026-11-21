// Package app 是组合根：唯一知道"有哪些模块、它们怎么连起来"的地方。
// infra.go 构造共享基础设施，modules.go 构造模块，api.go / worker.go 组装两个进程。
package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	goredis "github.com/redis/go-redis/v9"

	"app.local/internal/config"
	"app.local/internal/platform/dbtx"
	"app.local/internal/platform/httpserver"
	"app.local/internal/platform/logger"
	"app.local/internal/platform/metrics"
	"app.local/internal/platform/postgres"
	"app.local/internal/platform/process"
	"app.local/internal/platform/queue"
	"app.local/internal/platform/redis"
)

// Infra 持有连接类资源，按配置决定是否创建（没配 REDIS_ADDR 就不连 Redis）。
// 模块构造只是结构体赋值，昂贵的东西都在这里，由 Close 统一释放。
type Infra struct {
	Config *config.Config
	// Identity 是进程身份：Logger 的静态字段来自它，指标的 Registry 也由它构造。
	Identity process.Identity
	Logger   *slog.Logger
	// Metrics 是进程的 Registry（运行时指标和业务自定义指标），由一个端口输出：api 的前台端口、
	// worker 的内部端口。后台端口另有自己的 Registry，不输出它，否则两个端口被抓取时会算重。
	Metrics *prometheus.Registry

	Pool *pgxpool.Pool
	DBTx *dbtx.Handle
	// Redis 为 nil 表示没有配置 Redis。
	Redis *goredis.Client
	// Queue 只入队、不消费；worker 进程另建消费用的 client。
	Queue *queue.Inserter

	// Pingers 是 /readyz 检查的依赖。
	Pingers []httpserver.Pinger
}

// newProcess 构造进程身份和根 logger，并设为 slog.Default()：没有绑定 request 的代码路径（比如 river
// 执行任务时）经 logctx.From 拿到的是它，格式和级别才一致。logger 的静态字段只能来自 id，两者不会对不上。
func newProcess(cfg *config.Config, name string, w io.Writer) (process.Identity, *slog.Logger) {
	id := process.New(name, cfg.InstanceID)
	log := logger.New(cfg.LogLevel, w, id.LogAttrs()...)
	slog.SetDefault(log)
	return id, log
}

func NewInfra(ctx context.Context, cfg *config.Config, id process.Identity, log *slog.Logger) (*Infra, error) {
	pool, err := postgres.NewPool(ctx, postgres.PoolConfig{
		DSN:               cfg.DB.DSN,
		MinConns:          cfg.DB.MinConns,
		MaxConns:          cfg.DB.MaxConns,
		MaxConnLifetime:   cfg.DB.MaxConnLifetime,
		HealthCheckPeriod: cfg.DB.HealthCheckPeriod,
	})
	if err != nil {
		return nil, fmt.Errorf("app: build infra: %w", err)
	}
	handle := dbtx.NewHandle(pool)

	insertClient, err := queue.NewClient(pool, nil, nil, log)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("app: build infra: %w", err)
	}

	in := &Infra{
		Config:   cfg,
		Identity: id,
		Metrics:  metrics.NewRuntimeRegistry(id),
		Logger:   log,
		Pool:     pool,
		DBTx:     handle,
		Queue:    queue.NewInserter(insertClient, handle),
		Pingers:  []httpserver.Pinger{handle},
	}
	if cfg.Redis.Addr != "" {
		in.Redis = redis.NewClient(cfg.Redis.Addr, cfg.Redis.Password)
		in.Pingers = append(in.Pingers, redis.NewPinger(in.Redis))
	} else {
		log.WarnContext(ctx, "REDIS_ADDR 未配置：缓存退回 Noop，限流按进程内存计数，多实例部署时实际额度 = 配置值 × 实例数")
	}
	return in, nil
}

// Close 逆序释放资源。
func (in *Infra) Close(ctx context.Context) {
	if in.Redis != nil {
		if err := in.Redis.Close(); err != nil {
			in.Logger.ErrorContext(ctx, "close redis client", slog.Any("error", err))
		}
	}
	if in.Pool != nil {
		in.Pool.Close()
	}
}
