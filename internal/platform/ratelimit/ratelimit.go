// Package ratelimit 提供 Echo RateLimiter 中间件的计数存储。配置了 Redis 时用
// GCRA（go-redis/redis_rate），所有实例共享额度；没配时退回进程内存，多实例下
// 实际额度是"配置值 × 实例数"。
package ratelimit

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/go-redis/redis_rate/v10"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	goredis "github.com/redis/go-redis/v9"
)

// Limit 表示每 Period 允许 Rate 次请求，突发上限 Burst。
type Limit struct {
	Rate   int
	Period time.Duration
	Burst  int
}

// PerSecond 把"每秒 rate 次"（可以是小数，比如 0.5）换成整数的 Limit，突发上限为
// 两秒的额度、至少 1。直接 int(rate) 会把 0.5 截成 0，变成全部拒绝。
func PerSecond(rate float64) Limit {
	const scale = 1000
	return Limit{
		Rate:   max(1, int(math.Round(rate*scale))),
		Period: scale * time.Second,
		Burst:  max(1, int(math.Ceil(rate*2))),
	}
}

// redisTimeout 是单次限流查询的上限。限流中间件排在请求超时之前，不设上限的话
// Redis 网络不通时每个请求都要等 go-redis 的拨号/读超时加重试，可能是好几秒。
const redisTimeout = 100 * time.Millisecond

// NewStore 在 client 为 nil 时返回内存 store。name 区分不同限流规则的 key 空间。
func NewStore(client *goredis.Client, name string, limit Limit, logger *slog.Logger) middleware.RateLimiterStore {
	if client == nil {
		return middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
			Rate:      float64(limit.Rate) / limit.Period.Seconds(),
			Burst:     limit.Burst,
			ExpiresIn: max(5*time.Minute, 2*limit.Period),
		})
	}
	return &RedisStore{
		limiter: redis_rate.NewLimiter(client),
		prefix:  "ratelimit:" + name + ":",
		limit:   redis_rate.Limit{Rate: limit.Rate, Period: limit.Period, Burst: limit.Burst},
		logger:  logger,
		timeout: redisTimeout,
	}
}

// RedisStore 在 Redis 故障时放行请求并记日志（fail open）：限流是保护措施，
// 不能因为它的存储不可用就让整个 API 不可用。
type RedisStore struct {
	limiter *redis_rate.Limiter
	prefix  string
	limit   redis_rate.Limit
	logger  *slog.Logger
	timeout time.Duration
}

func (s *RedisStore) Allow(identifier string) (bool, error) {
	return s.allow(context.Background(), identifier)
}

func (s *RedisStore) AllowContext(c *echo.Context, identifier string) (bool, error) {
	return s.allow(c.Request().Context(), identifier)
}

func (s *RedisStore) allow(ctx context.Context, identifier string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	res, err := s.limiter.Allow(ctx, s.prefix+identifier, s.limit)
	if err != nil {
		s.logger.WarnContext(ctx, "redis rate limiter failed, allowing request", slog.String("key", s.prefix+identifier), slog.Any("error", err))
		return true, nil
	}
	return res.Allowed > 0, nil
}

var (
	_ middleware.RateLimiterStore        = (*RedisStore)(nil)
	_ middleware.RateLimiterStoreContext = (*RedisStore)(nil)
)
