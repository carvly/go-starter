package queue

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"app.local/internal/platform/migrate"
)

// Migrations 是 river 自己的表结构，由 river 的迁移器（rivermigrate）执行：版本记在 river_migration 表，每个版本在
// 自己的事务里执行。不导出成 SQL 文件交给 golang-migrate：river 的迁移依赖"上一个版本已提交"（004 给枚举加 'pending'、
// 006 的函数用它），合并进一个事务后只有 Postgres 17 起才能执行；升级 river 时新增的版本也随库自动带上。
var Migrations = migrate.Source{Name: "river", Migrator: riverMigrator{}}

type riverMigrator struct{}

func (riverMigrator) Up(ctx context.Context, dsn string) error {
	return withRiverMigrator(ctx, dsn, func(m *rivermigrate.Migrator[pgx.Tx]) error {
		_, err := m.Migrate(ctx, rivermigrate.DirectionUp, nil)
		return err
	})
}

func (riverMigrator) Down(ctx context.Context, dsn string) error {
	return withRiverMigrator(ctx, dsn, func(m *rivermigrate.Migrator[pgx.Tx]) error {
		_, err := m.Migrate(ctx, rivermigrate.DirectionDown, &rivermigrate.MigrateOpts{MaxSteps: 1})
		return err
	})
}

func (riverMigrator) Version(ctx context.Context, dsn string) (uint, error) {
	var version uint
	err := withRiverMigrator(ctx, dsn, func(m *rivermigrate.Migrator[pgx.Tx]) error {
		existing, err := m.ExistingVersions(ctx)
		if err != nil {
			return err
		}
		for _, v := range existing {
			version = max(version, uint(v.Version))
		}
		return nil
	})
	return version, err
}

func withRiverMigrator(ctx context.Context, dsn string, fn func(*rivermigrate.Migrator[pgx.Tx]) error) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("river migrate: connect: %w", err)
	}
	defer pool.Close()
	m, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		return fmt.Errorf("river migrate: %w", err)
	}
	return fn(m)
}
