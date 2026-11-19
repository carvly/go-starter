package queue

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeClient struct {
	mu             sync.Mutex
	started        bool
	stopped        bool
	hardStopped    bool
	stopBlocksTill chan struct{} // 非 nil 时 Stop 一直阻塞到 ctx 到期，模拟卡住的任务
}

func (f *fakeClient) Start(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = true
	return nil
}

func (f *fakeClient) Stop(ctx context.Context) error {
	f.mu.Lock()
	f.stopped = true
	block := f.stopBlocksTill
	f.mu.Unlock()
	if block != nil {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (f *fakeClient) StopAndCancel(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hardStopped = true
	return nil
}

func TestWorkerComponent_StopBeforeStart_NeverStartsClient(t *testing.T) {
	f := &fakeClient{}
	c := Component(f)
	ctx, cancel := context.WithCancel(t.Context())

	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if f.started {
		t.Error("Stop 之后不应该再启动 client，否则它再也不会被停掉")
	}
}

func TestWorkerComponent_StopTimeout_EscalatesToHardStop(t *testing.T) {
	f := &fakeClient{stopBlocksTill: make(chan struct{})}
	c := Component(f)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	err := c.Stop(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want DeadlineExceeded", err)
	}
	if !f.hardStopped {
		t.Error("优雅停止超时后应该调用 StopAndCancel 取消正在执行的任务")
	}
}

func TestWorkerComponent_GracefulStop_DoesNotHardStop(t *testing.T) {
	f := &fakeClient{}
	c := Component(f)
	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.hardStopped {
		t.Error("优雅停止成功时不应该强制取消任务")
	}
}
