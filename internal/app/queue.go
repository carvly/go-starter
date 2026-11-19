package app

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"

	"app.local/internal/config"
	"app.local/internal/platform/postgres"
	"app.local/internal/platform/queue"
)

// RunQueueCommand 只使用数据库配置和纯 worker kind 声明，不构造业务模块。
func RunQueueCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) != 2 || args[1] != "cancel-orphans" {
		return fmt.Errorf("usage: api queue cancel-orphans")
	}
	cfg, err := config.LoadDB()
	if err != nil {
		return err
	}
	pool, err := postgres.NewPool(ctx, postgres.PoolConfig{
		DSN: cfg.DSN, MinConns: cfg.MinConns, MaxConns: cfg.MaxConns,
		MaxConnLifetime: cfg.MaxConnLifetime, HealthCheckPeriod: cfg.HealthCheckPeriod,
	})
	if err != nil {
		return err
	}
	defer pool.Close()
	cancelled, err := queue.CancelOrphans(ctx, pool, workerKinds())
	if err != nil {
		return err
	}
	for _, kind := range slices.Sorted(maps.Keys(cancelled)) {
		count := cancelled[kind]
		_, _ = fmt.Fprintf(stdout, "%s\t%d\n", kind, count)
	}
	return nil
}
