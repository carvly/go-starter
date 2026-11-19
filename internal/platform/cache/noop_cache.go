package cache

import (
	"context"
	"time"
)

// NoopCache 用于没有配置 Redis 的部署：Get 恒定未命中，Set/Delete 是空操作。
type NoopCache[T any] struct{}

func NewNoopCache[T any]() *NoopCache[T] {
	return &NoopCache[T]{}
}

func (NoopCache[T]) Get(context.Context, string) (*T, bool, error) {
	return nil, false, nil
}

func (NoopCache[T]) Set(context.Context, string, *T, time.Duration) error {
	return nil
}

func (NoopCache[T]) Delete(context.Context, string) error {
	return nil
}
