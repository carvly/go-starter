// Package uowtest 提供 uow.UnitOfWork 的测试替身。
package uowtest

import "context"

// Immediate 直接执行 fn，不开事务；fn 的错误原样返回。
type Immediate struct{}

func (Immediate) Run(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }
