// Package postgres 提供连接池构造和分页 cursor 编解码。各模块的
// Repository 实现在 internal/modules/<m>/postgres，直接用 pgx/v5 原生接口。
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolConfig 的默认值只是起点，需要按实际并发量和实例规格压测调整。
type PoolConfig struct {
	DSN               string
	MinConns          int32
	MaxConns          int32
	MaxConnLifetime   time.Duration
	HealthCheckPeriod time.Duration
}

// NewPool 构造一个 *pgxpool.Pool。
func NewPool(ctx context.Context, cfg PoolConfig) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse DSN: %w", err)
	}

	if cfg.MinConns > 0 {
		poolCfg.MinConns = cfg.MinConns
	}
	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	if cfg.MaxConnLifetime > 0 {
		poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
		// 加 10% 抖动，防止大批连接同时过期导致重连风暴。
		poolCfg.MaxConnLifetimeJitter = cfg.MaxConnLifetime / 10
	}
	if cfg.HealthCheckPeriod > 0 {
		poolCfg.HealthCheckPeriod = cfg.HealthCheckPeriod
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}
	return pool, nil
}
