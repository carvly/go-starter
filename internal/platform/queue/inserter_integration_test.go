//go:build integration

package queue_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"app.local/internal/platform/dbtx"
	"app.local/internal/platform/logger"
	"app.local/internal/platform/postgres/pgtest"
	"app.local/internal/platform/queue"
	"app.local/internal/platform/tracing"
)

type testArgs struct {
	Value string `json:"value"`
}

func (testArgs) Kind() string { return "queue_integration_test" }

func newInserter(t *testing.T) (*queue.Inserter, *dbtx.Handle, func() int) {
	t.Helper()
	pool := pgtest.Pool(t)
	client, err := queue.NewClient(pool, nil, nil, slog.New(slog.DiscardHandler), tracing.Disabled())
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

type traceWorker struct {
	river.WorkerDefaults[testArgs]
	log  *slog.Logger
	done chan struct{}
}

func (w *traceWorker) Work(ctx context.Context, _ *river.Job[testArgs]) error {
	w.log.InfoContext(ctx, "queue test: job worked")
	close(w.done)
	return nil
}

// 请求里入队的任务：执行日志的 link_trace_id 是入队那次请求的 trace_id，trace_id 是这次执行新的
// （otelriver 用 span link 关联，不是父子关系）。
func TestJob_LinksBackToEnqueuingRequest(t *testing.T) {
	pool := pgtest.Pool(t)
	tp := tracing.Disabled()
	insertClient, err := queue.NewClient(pool, nil, nil, slog.New(slog.DiscardHandler), tp)
	if err != nil {
		t.Fatal(err)
	}
	h := dbtx.NewHandle(pool)
	ins := queue.NewInserter(insertClient, h)

	ctx, span := tp.Tracer("test").Start(t.Context(), "GET /v1/test")
	requestTraceID := span.SpanContext().TraceID().String()
	if err := h.Run(ctx, func(ctx context.Context) error { return ins.InsertTx(ctx, testArgs{"x"}) }); err != nil {
		t.Fatal(err)
	}
	span.End()

	var buf syncBuffer
	log := logger.New("info", &buf)
	w := &traceWorker{log: log, done: make(chan struct{})}
	workers := queue.NewWorkers()
	queue.AddWorker(workers, w)
	workClient, err := queue.NewClient(pool, workers, nil, log, tp)
	if err != nil {
		t.Fatal(err)
	}
	if err := workClient.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = workClient.Stop(context.WithoutCancel(t.Context())) }()
	select {
	case <-w.done:
	case <-time.After(10 * time.Second):
		t.Fatal("任务没有被执行")
	}

	var rec map[string]any
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(line, "queue test: job worked") {
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatal(err)
			}
		}
	}
	if rec == nil {
		t.Fatalf("没有任务日志：%s", buf.String())
	}
	if rec["link_trace_id"] != requestTraceID {
		t.Errorf("link_trace_id = %v, want 请求的 %s", rec["link_trace_id"], requestTraceID)
	}
	if id, _ := rec["trace_id"].(string); id == "" || id == requestTraceID {
		t.Errorf("trace_id = %q, 应是这次执行新的", id)
	}
}

// syncBuffer 让 worker goroutine 写日志、测试 goroutine 读日志不产生数据竞争。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
