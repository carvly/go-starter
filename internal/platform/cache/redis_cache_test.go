package cache_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"app.local/internal/platform/cache"
)

type testItem struct {
	ID   string
	Name string
}

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t) // t.Cleanup 里自动关闭，见 miniredis 文档
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRedisCache_SetGetDelete(t *testing.T) {
	client := newTestRedis(t)
	c := cache.NewRedisCache[testItem](client, "item", discardLogger())
	ctx := t.Context()

	item := &testItem{ID: "sku-1", Name: "机械键盘"}
	if err := c.Set(ctx, "sku-1", item, time.Minute); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got, hit, err := c.Get(ctx, "sku-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !hit {
		t.Fatal("Get() hit = false, want true")
	}
	if got.Name != "机械键盘" {
		t.Errorf("Get() = %+v, want Name=机械键盘", got)
	}

	if err := c.Delete(ctx, "sku-1"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	_, hit, err = c.Get(ctx, "sku-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if hit {
		t.Error("Get() hit = true after Delete()，want false")
	}
}

func TestRedisCache_Miss(t *testing.T) {
	client := newTestRedis(t)
	c := cache.NewRedisCache[testItem](client, "item", discardLogger())

	_, hit, err := c.Get(t.Context(), "does-not-exist")
	if err != nil {
		t.Fatalf("Get() error = %v, want nil（未命中不是错误）", err)
	}
	if hit {
		t.Error("Get() hit = true, want false")
	}
}

func TestRedisCache_KeyNamespacing(t *testing.T) {
	client := newTestRedis(t)
	c := cache.NewRedisCache[testItem](client, "item", discardLogger())
	ctx := t.Context()

	if err := c.Set(ctx, "sku-1", &testItem{ID: "sku-1"}, time.Minute); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	// key 必须按 cache:<domain>:<id> 拼接，否则不同模块之间、缓存和限流之间会撞 key。
	raw, err := client.Get(ctx, "cache:item:sku-1").Result()
	if err != nil {
		t.Fatalf("直接用命名空间 key 读取失败: %v，说明 RedisCache 没有按约定拼接 key", err)
	}
	if raw == "" {
		t.Error("命名空间 key 读到空值")
	}
}

func TestRedisCache_FailOpen_WhenClientBroken(t *testing.T) {
	// 连不上的地址模拟 Redis 故障：Get/Set/Delete 都不能把 error 冒泡给调用方。
	brokenClient := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1", // 该端口不会有真实 Redis 监听
		DialTimeout: 200 * time.Millisecond,
		MaxRetries:  -1, // 禁用重试，让测试快速失败而不是等好几秒
	})
	c := cache.NewRedisCache[testItem](brokenClient, "item", discardLogger())
	ctx := t.Context()

	_, hit, err := c.Get(ctx, "sku-1")
	if err != nil {
		t.Fatalf("Get() error = %v, want nil（fail open）", err)
	}
	if hit {
		t.Error("Get() hit = true, want false（Redis 故障应该当未命中）")
	}

	if err := c.Set(ctx, "sku-1", &testItem{ID: "sku-1"}, time.Minute); err != nil {
		t.Fatalf("Set() error = %v, want nil（fail open）", err)
	}
	if err := c.Delete(ctx, "sku-1"); err != nil {
		t.Fatalf("Delete() error = %v, want nil（fail open）", err)
	}
}
