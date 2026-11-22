// Package redis 构造 go-redis 客户端，并把它适配成 /readyz 的依赖检查。
package redis

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/extra/redisotel/v9"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace"
)

// NewClient 打开 ContextTimeoutEnabled：go-redis 默认不按 ctx 的截止时间设置读写
// 超时，调用方给的超时（比如限流查询的 100ms）会不起作用。每条命令记一个 span，不记命令参数
// （键里可能有 ID，值可能是业务数据）。tp 必填。
func NewClient(addr, password string, tp trace.TracerProvider) (*goredis.Client, error) {
	if tp == nil {
		return nil, errors.New("redis: TracerProvider is required")
	}
	client := goredis.NewClient(&goredis.Options{Addr: addr, Password: password, ContextTimeoutEnabled: true})
	if err := redisotel.InstrumentTracing(client, redisotel.WithTracerProvider(tp), redisotel.WithDBStatement(false)); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: instrument tracing: %w", err)
	}
	return client, nil
}

// Pinger 让 /readyz 检查 Redis 连通性。
type Pinger struct {
	client *goredis.Client
}

func NewPinger(client *goredis.Client) *Pinger {
	return &Pinger{client: client}
}

func (p *Pinger) Ping(ctx context.Context) error {
	return p.client.Ping(ctx).Err()
}
