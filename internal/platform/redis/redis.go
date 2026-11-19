// Package redis 构造 go-redis 客户端，并把它适配成 /readyz 的依赖检查。
package redis

import (
	"context"

	goredis "github.com/redis/go-redis/v9"
)

// NewClient 打开 ContextTimeoutEnabled：go-redis 默认不按 ctx 的截止时间设置读写
// 超时，调用方给的超时（比如限流查询的 100ms）会不起作用。
func NewClient(addr, password string) *goredis.Client {
	return goredis.NewClient(&goredis.Options{Addr: addr, Password: password, ContextTimeoutEnabled: true})
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
