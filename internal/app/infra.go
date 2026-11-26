// Package app 是组合根：唯一知道"有哪些模块、它们怎么连起来"的地方。
// infra.go 构造共享基础设施，modules.go 构造模块，api.go / worker.go 组装两个进程。
package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	goredis "github.com/redis/go-redis/v9"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"

	"app.local/internal/config"
	"app.local/internal/platform/crypto"
	"app.local/internal/platform/dbtx"
	"app.local/internal/platform/httpserver"
	"app.local/internal/platform/logger"
	"app.local/internal/platform/metrics"
	"app.local/internal/platform/postgres"
	"app.local/internal/platform/process"
	"app.local/internal/platform/queue"
	"app.local/internal/platform/redis"
	"app.local/internal/platform/tracing"
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
	// Tracer 是进程的 TracerProvider（tracing.New）：HTTP、SQL、Redis、队列的 span 都从它建。不设为 otel 的全局
	// provider：不经组合根构造的出站客户端（如微信登录）从 ctx 里的 span 取 provider。
	Tracer trace.TracerProvider
	// shutdownTracing 导出剩余的 span，由 Close 最后调用；只构造了部分字段的 Infra（测试）为 nil。
	shutdownTracing func(context.Context) error

	Pool *pgxpool.Pool
	DBTx *dbtx.Handle
	// Redis 为 nil 表示没有配置 Redis。
	Redis *goredis.Client
	// Queue 只入队、不消费；worker 进程另建消费用的 client。
	Queue *queue.Inserter

	// Pingers 是 /readyz 检查的依赖。
	Pingers []httpserver.Pinger

	// AssertionVerifier 验证网关转来的身份断言（GATEWAY_ASSERTION_PUBLIC_KEYS），没配时为 nil。
	AssertionVerifier *crypto.AssertionVerifier
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
	tp, err := tracing.New(ctx, id, cfg.Tracing.OTLPEndpoint)
	if err != nil {
		return nil, fmt.Errorf("app: build infra: %w", err)
	}
	in := &Infra{
		Config: cfg, Identity: id, Metrics: metrics.NewRuntimeRegistry(id), Logger: log, Tracer: tp,
		shutdownTracing: func(ctx context.Context) error { return tracing.Shutdown(ctx, tp) },
	}
	if in.AssertionVerifier, err = newAssertionVerifier(cfg.Gateway, in.Metrics); err != nil {
		in.Close(ctx)
		return nil, fmt.Errorf("app: build infra: %w", err)
	}
	if err := in.connect(ctx); err != nil {
		in.Close(ctx)
		return nil, fmt.Errorf("app: build infra: %w", err)
	}
	return in, nil
}

// connect 构造连接类资源；出错时由调用方 Close 释放已经建好的部分。
func (in *Infra) connect(ctx context.Context) error {
	cfg := in.Config
	pool, err := postgres.NewPool(ctx, postgres.PoolConfig{
		DSN:               cfg.DB.DSN,
		MinConns:          cfg.DB.MinConns,
		MaxConns:          cfg.DB.MaxConns,
		MaxConnLifetime:   cfg.DB.MaxConnLifetime,
		HealthCheckPeriod: cfg.DB.HealthCheckPeriod,
		Tracer:            newPgxTracer(in.Tracer),
	})
	if err != nil {
		return err
	}
	in.Pool = pool
	in.DBTx = dbtx.NewHandle(pool)
	in.Pingers = []httpserver.Pinger{in.DBTx}

	insertClient, err := queue.NewClient(pool, nil, nil, in.Logger, in.Tracer)
	if err != nil {
		return err
	}
	in.Queue = queue.NewInserter(insertClient, in.DBTx)

	if cfg.Redis.Addr == "" {
		in.Logger.WarnContext(ctx, "REDIS_ADDR 未配置：缓存退回 Noop，限流按进程内存计数，多实例部署时实际额度 = 配置值 × 实例数")
		return nil
	}
	if in.Redis, err = redis.NewClient(cfg.Redis.Addr, cfg.Redis.Password, in.Tracer); err != nil {
		return err
	}
	in.Pingers = append(in.Pingers, redis.NewPinger(in.Redis))
	return nil
}

// Close 逆序释放资源。在进程的组件（HTTP server、队列）全部停下之后调用，tracing 最后关停：
// 排空和优雅关闭期间的 span 也要导出，最多等 3 秒，到期丢弃。
func (in *Infra) Close(ctx context.Context) {
	if in.Redis != nil {
		if err := in.Redis.Close(); err != nil {
			in.Logger.ErrorContext(ctx, "close redis client", slog.Any("error", err))
		}
	}
	if in.Pool != nil {
		in.Pool.Close()
	}
	if in.shutdownTracing != nil {
		if err := in.shutdownTracing(ctx); err != nil {
			in.Logger.WarnContext(ctx, "tracing: shutdown did not finish, remaining spans dropped", slog.Any("error", err))
		}
	}
}

// newPgxTracer 记录每条 SQL 的 span：默认 span 名只取 SQL 的第一个词（SELECT、INSERT），完整语句在属性里，
// 不记参数值。指标走 Prometheus：MeterProvider 不传时 otelpgx 退回 otel 全局 provider，所以显式传 noop。
func newPgxTracer(tp trace.TracerProvider) *otelpgx.Tracer {
	return otelpgx.NewTracer(otelpgx.WithTracerProvider(tp), otelpgx.WithMeterProvider(metricnoop.NewMeterProvider()))
}

// newAssertionVerifier 按 GATEWAY_ASSERTION_PUBLIC_KEYS 构造断言验证器，没配时返回 nil。每把已加载的公钥输出一条
// gateway_assertion_key_info{kid}：轮换断言密钥时，切换签发方之前用它确认所有实例都已加载新公钥。
func newAssertionVerifier(cfg config.GatewayConfig, reg prometheus.Registerer) (*crypto.AssertionVerifier, error) {
	if len(cfg.AssertionPublicKeys) == 0 {
		return nil, nil
	}
	v, err := crypto.NewAssertionVerifier(cfg.AssertionPublicKeys)
	if err != nil {
		return nil, fmt.Errorf("GATEWAY_ASSERTION_PUBLIC_KEYS: %w", err)
	}
	info := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gateway_assertion_key_info",
		Help: "Identity assertion public keys loaded by this process, one series per kid.",
	}, []string{"kid"})
	for _, kid := range v.KIDs() {
		info.WithLabelValues(kid).Set(1)
	}
	if err := reg.Register(info); err != nil {
		return nil, err
	}
	return v, nil
}
