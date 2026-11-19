package httpserver

import "context"

// Pinger 是 /readyz 逐个检查的依赖，由 dbtx.Handle、Redis 客户端等实现。
type Pinger interface {
	Ping(ctx context.Context) error
}
