//go:build integration

package queue_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"app.local/internal/kit/logctx"
	"app.local/internal/platform/dbtx"
	"app.local/internal/platform/postgres/pgtest"
	"app.local/internal/platform/queue"
)

type testArgs struct {
	Value string `json:"value"`
}

func (testArgs) Kind() string { return "queue_integration_test" }

func newInserter(t *testing.T) (*queue.Inserter, *dbtx.Handle, func() int) {
	t.Helper()
	pool := pgtest.Pool(t)
	client, err := queue.NewClient(pool, nil, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	h := dbtx.NewHandle(pool)
	count := func() int {
		var n int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM river_job WHERE kind = 'queue_integration_test'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	return queue.NewInserter(client, h), h, count
}

// 任务和业务写入同事务：事务回滚时任务也不能留下。
func TestInsertTx_FollowsTransaction(t *testing.T) {
	ctx := t.Context()
	ins, h, count := newInserter(t)

	if err := h.Run(ctx, func(ctx context.Context) error { return ins.InsertTx(ctx, testArgs{"committed"}) }); err != nil {
		t.Fatal(err)
	}
	_ = h.Run(ctx, func(ctx context.Context) error {
		if err := ins.InsertTx(ctx, testArgs{"rolled back"}); err != nil {
			return err
		}
		return errors.New("rollback")
	})
	if n := count(); n != 1 {
		t.Errorf("river_job = %d, want 1", n)
	}
}

func TestInsertTx_RequiresTransaction(t *testing.T) {
	ins, _, _ := newInserter(t)
	if err := ins.InsertTx(t.Context(), testArgs{}); !errors.Is(err, dbtx.ErrNoTransaction) {
		t.Errorf("error = %v, want ErrNoTransaction", err)
	}
}

// 请求里入队的任务在 metadata 里带上 request_id，worker 执行时据此恢复日志关联字段。
func TestInsertTx_CarriesRequestIDInMetadata(t *testing.T) {
	pool := pgtest.Pool(t)
	client, err := queue.NewClient(pool, nil, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	h := dbtx.NewHandle(pool)
	ins := queue.NewInserter(client, h)

	ctx := logctx.WithRequestID(t.Context(), "rid-integration")
	if err := h.Run(ctx, func(ctx context.Context) error { return ins.InsertTx(ctx, testArgs{"x"}) }); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := pool.QueryRow(t.Context(),
		`SELECT metadata->>'request_id' FROM river_job WHERE kind = 'queue_integration_test'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "rid-integration" {
		t.Errorf("metadata.request_id = %q, want rid-integration", got)
	}
}
