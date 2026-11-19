package lifecycle_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"app.local/internal/kit/lifecycle"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// fakeComponent 的 Start 默认阻塞到 ctx 取消；exitWith 非 nil 时立即返回它，
// exitEarly 为 true 时立即返回 nil，ignoreCancel 为 true 时永不返回直到 release。
type fakeComponent struct {
	name         string
	exitWith     error
	exitEarly    bool
	ignoreCancel bool
	release      chan struct{}

	mu      sync.Mutex
	stopped bool
	order   *[]string
}

func (f *fakeComponent) Name() string { return f.name }

func (f *fakeComponent) Start(ctx context.Context) error {
	switch {
	case f.exitWith != nil:
		return f.exitWith
	case f.exitEarly:
		return nil
	case f.ignoreCancel:
		<-f.release
		return nil
	}
	<-ctx.Done()
	return nil
}

func (f *fakeComponent) Stop(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
	if f.order != nil {
		*f.order = append(*f.order, f.name)
	}
	return nil
}

func TestRun_ContextCanceled_StopsAllInReverseOrderWithoutError(t *testing.T) {
	var order []string
	a := &fakeComponent{name: "a", order: &order}
	b := &fakeComponent{name: "b", order: &order}

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	if err := lifecycle.Run(ctx, time.Second, a, b); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if len(order) != 2 || order[0] != "b" || order[1] != "a" {
		t.Errorf("stop order = %v, want [b a]", order)
	}
}

func TestRun_ComponentFails_ReturnsErrorAndStopsOthers(t *testing.T) {
	boom := errors.New("boom")
	failing := &fakeComponent{name: "failing", exitWith: boom}
	other := &fakeComponent{name: "other"}

	err := lifecycle.Run(t.Context(), time.Second, other, failing)
	if !errors.Is(err, boom) {
		t.Fatalf("Run() error = %v, want wrapping %v", err, boom)
	}
	if !other.stopped {
		t.Error("出错后其它组件也必须被 Stop")
	}
}

func TestRun_ComponentExitsEarlyWithoutError_ReturnsUnexpectedExit(t *testing.T) {
	early := &fakeComponent{name: "early", exitEarly: true}
	other := &fakeComponent{name: "other"}

	err := lifecycle.Run(t.Context(), time.Second, other, early)
	if !errors.Is(err, lifecycle.ErrUnexpectedExit) {
		t.Fatalf("Run() error = %v, want ErrUnexpectedExit", err)
	}
}

func TestRun_TwoComponentsFailTogether_DoesNotHang(t *testing.T) {
	a := &fakeComponent{name: "a", exitWith: errors.New("a failed")}
	b := &fakeComponent{name: "b", exitWith: errors.New("b failed")}

	done := make(chan error, 1)
	go func() { done <- lifecycle.Run(t.Context(), time.Second, a, b) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run() error = nil, want non-nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("两个组件同时出错时 Run 卡住了")
	}
}

func TestRun_ComponentIgnoresStop_ReturnsStopTimeout(t *testing.T) {
	stuck := &fakeComponent{name: "stuck", ignoreCancel: true, release: make(chan struct{})}
	t.Cleanup(func() { close(stuck.release) })

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := lifecycle.Run(ctx, 20*time.Millisecond, stuck)
	if !errors.Is(err, lifecycle.ErrStopTimeout) {
		t.Fatalf("Run() error = %v, want ErrStopTimeout", err)
	}
}
