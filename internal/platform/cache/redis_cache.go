// Package cache 提供模块 domain 里 Cache 窄接口（Get/Set/Delete）的两个泛型实现：
// RedisCache 和 NoopCache。用哪个由模块的 module.go 按是否配置 Redis 决定。
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCache 的 key 必须是 cache:<domain>:<id>：限流也用同一个 Redis 实例
// （ratelimit:<规则>:<标识>），不隔离命名空间会撞 key。
type RedisCache[T any] struct {
	client *redis.Client
	domain string
	logger *slog.Logger
}

func NewRedisCache[T any](client *redis.Client, domain string, logger *slog.Logger) *RedisCache[T] {
	return &RedisCache[T]{client: client, domain: domain, logger: logger}
}

func (c *RedisCache[T]) key(id string) string {
	return fmt.Sprintf("cache:%s:%s", c.domain, id)
}

// Get 在未命中和 Redis 故障时都返回 (nil, false, nil)：fail open，缓存故障不能
// 让请求变成 500。故障日志在这里统一记录。
func (c *RedisCache[T]) Get(ctx context.Context, id string) (*T, bool, error) {
	data, err := c.client.Get(ctx, c.key(id)).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			c.logger.WarnContext(ctx, "redis cache get failed, treating as miss",
				"domain", c.domain, "id", id, "error", err)
		}
		return nil, false, nil
	}

	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		// 结构体字段变更导致旧值解不出来时也当未命中，TTL 会让它自愈，不需要版本号。
		c.logger.WarnContext(ctx, "redis cache unmarshal failed, treating as miss",
			"domain", c.domain, "id", id, "error", err)
		return nil, false, nil
	}
	return &v, true, nil
}

// Set 失败时只记日志，不冒泡成调用方的错误——缓存是性能优化，不是关键
// 路径依赖。
func (c *RedisCache[T]) Set(ctx context.Context, id string, v *T, ttl time.Duration) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("cache: marshal: %w", err)
	}
	if err := c.client.Set(ctx, c.key(id), data, ttl).Err(); err != nil {
		c.logger.WarnContext(ctx, "redis cache set failed", "domain", c.domain, "id", id, "error", err)
	}
	return nil
}

// Delete 同样 fail open。"先写 DB 再删缓存"的顺序由调用方保证。
func (c *RedisCache[T]) Delete(ctx context.Context, id string) error {
	if err := c.client.Del(ctx, c.key(id)).Err(); err != nil {
		c.logger.WarnContext(ctx, "redis cache delete failed", "domain", c.domain, "id", id, "error", err)
	}
	return nil
}
