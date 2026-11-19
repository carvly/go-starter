//go:build integration

package dbtx_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"app.local/internal/kit/uow"
	"app.local/internal/platform/dbtx"
	"app.local/internal/platform/postgres/pgtest"
)

// newHandle 返回一个带独立测试表的 Handle：platform 不认识业务模块，也不借用模块的表。
func newHandle(t *testing.T) *dbtx.Handle {
	t.Helper()
	h := pgtest.Handle(t)
	if _, err := h.Resolve(t.Context()).Exec(t.Context(), `CREATE TABLE dbtx_test_items (id bigint GENERATED ALWAYS AS IDENTITY)`); err != nil {
		t.Fatal(err)
	}
	return h
}

func countItems(t *testing.T, h *dbtx.Handle) int {
	t.Helper()
	var n int
	if err := h.Resolve(t.Context()).QueryRow(t.Context(), `SELECT count(*) FROM dbtx_test_items`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func insertItem(ctx context.Context, h *dbtx.Handle) error {
	_, err := h.Resolve(ctx).Exec(ctx, `INSERT INTO dbtx_test_items DEFAULT VALUES`)
	return err
}

func TestRun_CommitAndRollback(t *testing.T) {
	ctx := t.Context()
	h := newHandle(t)

	if err := h.Run(ctx, func(ctx context.Context) error { return insertItem(ctx, h) }); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err := h.Run(ctx, func(ctx context.Context) error {
		if err := insertItem(ctx, h); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want boom", err)
	}
	if n := countItems(t, h); n != 1 {
		t.Errorf("rows = %d, want 1（第二个事务应该整体回滚）", n)
	}
}

// 嵌套 Run 走 SAVEPOINT：内层失败只回滚内层，外层仍可提交。
func TestRun_NestedUsesSavepoint(t *testing.T) {
	ctx := t.Context()
	h := newHandle(t)

	err := h.Run(ctx, func(ctx context.Context) error {
		if err := insertItem(ctx, h); err != nil {
			return err
		}
		_ = h.Run(ctx, func(ctx context.Context) error {
			if err := insertItem(ctx, h); err != nil {
				return err
			}
			return errors.New("inner failed")
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := countItems(t, h); n != 1 {
		t.Errorf("rows = %d, want 1（只有外层的写入被提交）", n)
	}
}

func TestResolveTx_OutsideRun(t *testing.T) {
	if _, err := pgtest.Handle(t).ResolveTx(t.Context()); !errors.Is(err, dbtx.ErrNoTransaction) {
		t.Errorf("error = %v, want ErrNoTransaction", err)
	}
}

// 提交后回调执行时，其它连接必须已经能看到提交的数据；回滚的事务和回滚的
// SAVEPOINT 里登记的回调不执行。
func TestRun_AfterCommit(t *testing.T) {
	ctx := t.Context()
	h := newHandle(t)

	var seen []int
	record := func(context.Context) { seen = append(seen, countItems(t, h)) }

	err := h.Run(ctx, func(ctx context.Context) error {
		if err := insertItem(ctx, h); err != nil {
			return err
		}
		uow.AfterCommit(ctx, record)
		_ = h.Run(ctx, func(ctx context.Context) error {
			uow.AfterCommit(ctx, func(context.Context) { t.Error("回滚的 SAVEPOINT 里登记的回调被执行了") })
			return errors.New("inner failed")
		})
		return h.Run(ctx, func(ctx context.Context) error {
			uow.AfterCommit(ctx, record)
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seen, []int{1, 1}) {
		t.Errorf("回调看到的行数 = %v, want [1 1]（两个回调都在外层提交后执行）", seen)
	}

	_ = h.Run(ctx, func(ctx context.Context) error {
		uow.AfterCommit(ctx, func(context.Context) { t.Error("回滚的事务里登记的回调被执行了") })
		return errors.New("boom")
	})
}
