package uow_test

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"app.local/internal/kit/uow"
)

func TestAfterCommit_OutsideTransactionRunsImmediately(t *testing.T) {
	ran := false
	uow.AfterCommit(t.Context(), func(context.Context) { ran = true })
	if !ran {
		t.Error("不在事务里时 AfterCommit 应该立即执行")
	}
}

func TestAfterCommit_RunsOnlyAfterCommitted(t *testing.T) {
	var got []string
	txCtx, hooks := uow.WithHooks(t.Context())
	uow.AfterCommit(txCtx, func(context.Context) { got = append(got, "a") })
	uow.AfterCommit(txCtx, func(context.Context) { got = append(got, "b") })
	if len(got) != 0 {
		t.Fatalf("提交前执行了回调：%v", got)
	}
	hooks.Committed(t.Context())
	if !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("got %v, want [a b]（按登记顺序执行）", got)
	}
}

func TestAfterCommit_NestedWaitsForOutermostCommit(t *testing.T) {
	var got []string
	outerCtx, outer := uow.WithHooks(t.Context())

	innerCtx, inner := uow.WithHooks(outerCtx)
	uow.AfterCommit(innerCtx, func(context.Context) { got = append(got, "released") })
	inner.Committed(outerCtx)

	// 回滚的 SAVEPOINT：不调用 Committed，回调被丢弃。
	rolledBackCtx, _ := uow.WithHooks(outerCtx)
	uow.AfterCommit(rolledBackCtx, func(context.Context) { got = append(got, "rolled back") })

	if len(got) != 0 {
		t.Fatalf("外层提交前执行了回调：%v", got)
	}
	outer.Committed(t.Context())
	if !slices.Equal(got, []string{"released"}) {
		t.Errorf("got %v, want [released]", got)
	}
}

func TestAfterCommit_IgnoresCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	txCtx, hooks := uow.WithHooks(ctx)
	var cbErr error
	uow.AfterCommit(txCtx, func(ctx context.Context) { cbErr = ctx.Err() })
	cancel()
	hooks.Committed(ctx)
	if cbErr != nil {
		t.Errorf("回调拿到的 ctx 已取消：%v；数据已提交，副作用不能被跳过", cbErr)
	}
}

func TestAfterCommit_ConcurrentRegistration(t *testing.T) {
	txCtx, hooks := uow.WithHooks(t.Context())
	var ran atomic.Int64
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() { uow.AfterCommit(txCtx, func(context.Context) { ran.Add(1) }) })
	}
	wg.Wait()
	hooks.Committed(t.Context())
	if n := ran.Load(); n != 50 {
		t.Errorf("执行了 %d 个回调, want 50（并发登记时丢了回调）", n)
	}
}

// 回调在提交之后执行，它的 panic 不能冒到调用方（数据已经提交，调用方会误以为失败），
// 也不能让后面的回调被跳过。
func TestAfterCommit_PanicDoesNotEscapeOrSkipOthers(t *testing.T) {
	txCtx, hooks := uow.WithHooks(t.Context())
	uow.AfterCommit(txCtx, func(context.Context) { panic("boom") })
	ran := false
	uow.AfterCommit(txCtx, func(context.Context) { ran = true })

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Committed() panic 冒出来了：%v", r)
			}
		}()
		hooks.Committed(t.Context())
	}()
	if !ran {
		t.Error("前一个回调 panic 后，后面的回调没有执行")
	}
}

func TestInTransaction(t *testing.T) {
	if uow.InTransaction(t.Context()) {
		t.Error("普通 ctx 不应该被判定为在事务里")
	}
	txCtx, _ := uow.WithHooks(t.Context())
	if !uow.InTransaction(txCtx) {
		t.Error("WithHooks 返回的 ctx 应该被判定为在事务里")
	}
}
