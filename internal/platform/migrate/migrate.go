// Package migrate 按独立来源执行 Postgres 迁移。
package migrate

import (
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

// Source 是一组独立编号的迁移。Name 同时用于日志和版本表名。
type Source struct {
	Name string
	FS   fs.FS
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
		if src.FS == nil {
			return nil, fmt.Errorf("migrate: source %s has nil FS", src.Name)
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
