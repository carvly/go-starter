package cache_test

import (
	"testing"
	"time"

	"app.local/internal/platform/cache"
)

func TestNoopCache_AlwaysMiss(t *testing.T) {
	c := cache.NewNoopCache[testItem]()
	ctx := t.Context()

	if err := c.Set(ctx, "sku-1", &testItem{ID: "sku-1"}, time.Minute); err != nil {
		t.Fatalf("Set() error = %v, want nil", err)
	}
	_, hit, err := c.Get(ctx, "sku-1")
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}
	if hit {
		t.Error("Get() hit = true, want false（NoopCache 恒定未命中）")
	}
	if err := c.Delete(ctx, "sku-1"); err != nil {
		t.Fatalf("Delete() error = %v, want nil", err)
	}
}
