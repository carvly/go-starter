// Package uow 定义事务边界的契约，实现在 internal/platform/dbtx。
package uow

import (
	"context"
	"sync"

	"app.local/internal/kit/logctx"
)

// UnitOfWork 把 fn 内的写操作包进一次事务，事务经 ctx 隐式传给 fn 内调用的
// Repository。嵌套调用由实现方用 SAVEPOINT 处理，不会静默开出第二个独立事务。
// 实现方必须用 WithHooks 支持 AfterCommit。
type UnitOfWork interface {
	Run(ctx context.Context, fn func(ctx context.Context) error) error
}

type hooksKey struct{}

// Hooks 收集一次事务（或一层 SAVEPOINT）里登记的提交后回调。
type Hooks struct {
	mu  sync.Mutex
	fns []func(context.Context)
}

func (h *Hooks) add(fns ...func(context.Context)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fns = append(h.fns, fns...)
}

// AfterCommit 登记 fn，在 ctx 所在的最外层事务提交后执行；事务回滚（含 fn 所在的
// SAVEPOINT 回滚）时不执行；ctx 不在事务里时立即执行。用于删缓存、回填缓存这类必须
// 等数据对其它连接可见（或确定不会回滚）之后才能做的副作用。
//
// 回调必须是尽力而为的：它的 panic 会被记录并吞掉，不影响已经提交的结果和其它回调。
// Commit 返回错误时事务可能其实已经提交，回调不会执行，删缓存这类回调要接受由 TTL 兜底。
func AfterCommit(ctx context.Context, fn func(context.Context)) {
	if h, ok := ctx.Value(hooksKey{}).(*Hooks); ok {
		h.add(fn)
		return
	}
	run(ctx, fn)
}

// InTransaction 报告 ctx 是否处在 UnitOfWork.Run 的事务里。缓存读取用它判断要不要绕过
// 缓存：删缓存推迟到提交后，事务里命中缓存会读不到本事务自己的写入。
func InTransaction(ctx context.Context) bool {
	_, ok := ctx.Value(hooksKey{}).(*Hooks)
	return ok
}

// WithHooks 由 UnitOfWork 实现方在开启事务或 SAVEPOINT 时调用，返回的 ctx 交给 fn。
func WithHooks(ctx context.Context) (context.Context, *Hooks) {
	h := &Hooks{}
	return context.WithValue(ctx, hooksKey{}, h), h
}

// Committed 由实现方在提交（或释放 SAVEPOINT）成功后调用，outer 是传给 Run 的 ctx。
// outer 本身处在事务里时回调并入外层，等外层提交；否则立即执行。回调用不随 outer
// 取消的 ctx 执行：数据已经提交，副作用不能因为调用方超时而被跳过。
func (h *Hooks) Committed(outer context.Context) {
	h.mu.Lock()
	fns := h.fns
	h.fns = nil
	h.mu.Unlock()

	if parent, ok := outer.Value(hooksKey{}).(*Hooks); ok {
		parent.add(fns...)
		return
	}
	ctx := context.WithoutCancel(outer)
	for _, fn := range fns {
		run(ctx, fn)
	}
}

func run(ctx context.Context, fn func(context.Context)) {
	defer func() {
		if r := recover(); r != nil {
			logctx.From(ctx).ErrorContext(ctx, "uow: after-commit hook panicked", "panic", r)
		}
	}()
	fn(ctx)
}
