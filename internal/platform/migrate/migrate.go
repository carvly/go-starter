// Package migrate 按独立来源执行 Postgres 迁移。
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"regexp"

	gomigrate "github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Source 是一组独立编号的迁移，FS 与 Migrator 二选一。Name 用于日志和 -module 参数，FS 来源的版本表是
// schema_migrations_<Name>。
type Source struct {
	Name string
	// FS 下的 migrations/ 目录由 golang-migrate 执行：每个文件一个事务。
	FS fs.FS
	// Migrator 是自带迁移器的来源（如 river 的 rivermigrate）：按它自己的版本表和事务边界执行，Runner 只负责调用。
	// 第三方库的表结构交给库自己的迁移器，不导出成 SQL 文件：库的迁移按自己的事务边界设计（river 每个版本一个事务），
	// 合并导出后放进一个事务执行会破坏这个前提，升级库时也要记得手工补导出。
	Migrator Migrator
}

// Migrator 是自带迁移器的来源。
type Migrator interface {
	// Up 执行全部未执行的迁移。
	Up(ctx context.Context, dsn string) error
	// Down 回退一步。
	Down(ctx context.Context, dsn string) error
	// Version 返回当前版本，没有执行过时为 0。
	Version(ctx context.Context, dsn string) (uint, error)
}

// Version 是一个来源的当前迁移状态。
type Version struct {
	Source  string
	Version uint
	Dirty   bool
}

type Runner struct {
	dsn     string
	sources []Source
}

var sourceName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func NewRunner(dsn string, sources []Source) (*Runner, error) {
	seen := map[string]bool{}
	for _, src := range sources {
		if !sourceName.MatchString(src.Name) {
			return nil, fmt.Errorf("migrate: invalid source name %q", src.Name)
		}
		if (src.FS == nil) == (src.Migrator == nil) {
			return nil, fmt.Errorf("migrate: source %s must set exactly one of FS and Migrator", src.Name)
		}
		if seen[src.Name] {
			return nil, fmt.Errorf("migrate: duplicate source %s", src.Name)
		}
		seen[src.Name] = true
	}
	return &Runner{dsn: dsn, sources: sources}, nil
}

func (r *Runner) open(src Source) (*gomigrate.Migrate, *sql.DB, error) {
	db, err := sql.Open("pgx", r.dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("migrate: open %s: %w", src.Name, err)
	}
	source, err := iofs.New(src.FS, "migrations")
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("migrate: open source %s: %w", src.Name, err)
	}
	driver, err := postgres.WithInstance(db, &postgres.Config{MigrationsTable: "schema_migrations_" + src.Name})
	if err != nil {
		_ = source.Close()
		_ = db.Close()
		return nil, nil, fmt.Errorf("migrate: open database %s: %w", src.Name, err)
	}
	m, err := gomigrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		_ = source.Close()
		_ = db.Close()
		return nil, nil, fmt.Errorf("migrate: initialize %s: %w", src.Name, err)
	}
	return m, db, nil
}

func migrationError(src Source, m *gomigrate.Migrate, err error) error {
	version, dirty, versionErr := m.Version()
	if versionErr != nil {
		return fmt.Errorf("migrate %s: %w", src.Name, err)
	}
	return fmt.Errorf("migrate %s at version %d (dirty=%v): %w", src.Name, version, dirty, err)
}

func (r *Runner) Up() error {
	for _, src := range r.sources {
		if src.Migrator != nil {
			if err := src.Migrator.Up(context.Background(), r.dsn); err != nil {
				return fmt.Errorf("migrate %s: %w", src.Name, err)
			}
			continue
		}
		m, db, err := r.open(src)
		if err != nil {
			return err
		}
		err = m.Up()
		if err != nil && !errors.Is(err, gomigrate.ErrNoChange) {
			wrapped := migrationError(src, m, err)
			_, _ = m.Close()
			_ = db.Close()
			return wrapped
		}
		_, _ = m.Close()
		_ = db.Close()
	}
	return nil
}

func (r *Runner) source(name string) (Source, error) {
	for _, src := range r.sources {
		if src.Name == name {
			return src, nil
		}
	}
	return Source{}, fmt.Errorf("migrate: unknown source %q", name)
}

func (r *Runner) Down(module string) error {
	src, err := r.source(module)
	if err != nil {
		return err
	}
	if src.Migrator != nil {
		if err := src.Migrator.Down(context.Background(), r.dsn); err != nil {
			return fmt.Errorf("migrate %s: %w", src.Name, err)
		}
		return nil
	}
	m, db, err := r.open(src)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	defer func() { _, _ = m.Close() }()
	if err := m.Steps(-1); err != nil && !errors.Is(err, gomigrate.ErrNoChange) {
		return migrationError(src, m, err)
	}
	return nil
}

func (r *Runner) Force(module string, version int) error {
	src, err := r.source(module)
	if err != nil {
		return err
	}
	if src.Migrator != nil {
		// 自带迁移器的来源每个版本在一个事务里执行并记录，失败时整体回滚，不会留下 dirty 状态，也就不需要 force。
		return fmt.Errorf("migrate: source %s uses its own migrator, which never leaves a dirty version; force does not apply", src.Name)
	}
	m, db, err := r.open(src)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	defer func() { _, _ = m.Close() }()
	if err := m.Force(version); err != nil {
		return migrationError(src, m, err)
	}
	return nil
}

func (r *Runner) Versions() ([]Version, error) {
	out := make([]Version, 0, len(r.sources))
	for _, src := range r.sources {
		if src.Migrator != nil {
			version, err := src.Migrator.Version(context.Background(), r.dsn)
			if err != nil {
				return nil, fmt.Errorf("migrate: version %s: %w", src.Name, err)
			}
			out = append(out, Version{Source: src.Name, Version: version})
			continue
		}
		m, db, err := r.open(src)
		if err != nil {
			return nil, err
		}
		version, dirty, err := m.Version()
		_, _ = m.Close()
		_ = db.Close()
		if err != nil && !errors.Is(err, gomigrate.ErrNilVersion) {
			return nil, fmt.Errorf("migrate: version %s: %w", src.Name, err)
		}
		out = append(out, Version{Source: src.Name, Version: version, Dirty: dirty})
	}
	return out, nil
}
