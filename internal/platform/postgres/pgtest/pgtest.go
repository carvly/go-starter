//go:build integration

// Package pgtest 为集成测试提供真实 Postgres：每个测试进程启动一个容器，按迁移来源
// 集合建立模板库，之后每个测试克隆一个独立数据库。
package pgtest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"app.local/internal/platform/dbtx"
	"app.local/internal/platform/migrate"
	"app.local/internal/platform/queue"
)

// image 与 deploy/docker-compose.yml 保持一致。
const image = "postgres:18-alpine"

var (
	startContainer = sync.OnceValues(func() (string, error) { return start(context.Background()) })
	dbSeq          atomic.Int64
	templateSeq    atomic.Int64
	templateMu     sync.Mutex
	templateOnce   = map[string]func() (string, error){}
)

// Pool 返回一个连接到全新数据库的连接池。river 的迁移（queue.Migrations）总会先执行，sources 是调用方
// 需要的模块迁移；相同来源集合共享一个模板库。
func Pool(t *testing.T, sources ...migrate.Source) *pgxpool.Pool {
	t.Helper()
	ctx := t.Context()
	adminDSN, err := startContainer()
	if err != nil {
		t.Fatalf("pgtest: start postgres: %v", err)
	}
	sources = withPlatform(sources)
	template, err := templateFor(adminDSN, sources)
	if err != nil {
		t.Fatalf("pgtest: migrate template: %v", err)
	}

	pool, err := pgxpool.New(ctx, createDatabase(t, adminDSN, "TEMPLATE "+template))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// EmptyDSN 返回一个全新空数据库的连接串，不执行任何迁移，与 Pool 共用同一个容器。
// 给自己负责迁移的子进程用（如 go run ./cmd/api migrate up）。
func EmptyDSN(t *testing.T) string {
	t.Helper()
	adminDSN, err := startContainer()
	if err != nil {
		t.Fatalf("pgtest: start postgres: %v", err)
	}
	return createDatabase(t, adminDSN, "")
}

func createDatabase(t *testing.T, adminDSN, options string) string {
	t.Helper()
	admin, err := pgxpool.New(t.Context(), adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("test_%d", dbSeq.Add(1))
	if _, err := admin.Exec(t.Context(), strings.TrimSpace("CREATE DATABASE "+name+" "+options)); err != nil {
		t.Fatalf("pgtest: create database: %v", err)
	}
	return withDatabase(adminDSN, name)
}

func withPlatform(sources []migrate.Source) []migrate.Source {
	out := []migrate.Source{queue.Migrations}
	seen := map[string]bool{queue.Migrations.Name: true}
	for _, src := range sources {
		if !seen[src.Name] {
			seen[src.Name] = true
			out = append(out, src)
		}
	}
	return out
}

func templateFor(adminDSN string, sources []migrate.Source) (string, error) {
	names := make([]string, 0, len(sources))
	for _, src := range sources {
		names = append(names, src.Name)
	}
	slices.Sort(names)
	key := strings.Join(names, ",")
	templateMu.Lock()
	once, ok := templateOnce[key]
	if !ok {
		once = sync.OnceValues(func() (string, error) {
			return createTemplate(adminDSN, sources)
		})
		templateOnce[key] = once
	}
	templateMu.Unlock()
	return once()
}

func createTemplate(adminDSN string, sources []migrate.Source) (string, error) {
	ctx := context.Background()
	name := fmt.Sprintf("app_template_%d", templateSeq.Add(1))
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		return "", err
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		return "", err
	}
	runner, err := migrate.NewRunner(withDatabase(adminDSN, name), sources)
	if err != nil {
		return "", err
	}
	if err := runner.Up(); err != nil {
		return "", err
	}
	return name, nil
}

// New 声明测试需要的迁移来源并返回数据库句柄。
func New(t *testing.T, sources ...migrate.Source) *dbtx.Handle {
	t.Helper()
	return Handle(t, sources...)
}

// Handle 是 Pool 的 dbtx.Handle 版本。
func Handle(t *testing.T, sources ...migrate.Source) *dbtx.Handle {
	t.Helper()
	return dbtx.NewHandle(Pool(t, sources...))
}

func start(ctx context.Context) (string, error) {
	c, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase("postgres"),
		tcpostgres.WithUsername("app"),
		tcpostgres.WithPassword("app"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return "", err
	}
	return c.ConnectionString(ctx, "sslmode=disable")
}

func withDatabase(dsn, name string) string {
	base, params, _ := strings.Cut(dsn, "?")
	prefix, _, _ := strings.CutLast(base, "/")
	base = prefix + "/" + name
	if params != "" {
		return base + "?" + params
	}
	return base
}
