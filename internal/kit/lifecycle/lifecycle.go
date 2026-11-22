// Package lifecycle 统一进程内多个长期运行组件的启停：任一组件出错或提前
// 退出都会让整组停下，并以非 nil 错误返回，进程据此以非零退出码结束。
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

type Component interface {
	Name() string
	// Start 阻塞到 ctx 取消或出错。ctx 取消后返回 nil 表示正常退出。
	Start(ctx context.Context) error
	// Stop 在 Start 的 ctx 被取消后调用，用于释放 Start 之外持有的资源；
	// 没有额外资源的组件实现为空操作即可。
	Stop(ctx context.Context) error
}

// ErrUnexpectedExit 表示某个组件在没有收到停止信号时自行返回了 nil。
var ErrUnexpectedExit = errors.New("lifecycle: component exited unexpectedly")

// ErrStopTimeout 表示 timeout 内仍有组件没有退出。
var ErrStopTimeout = errors.New("lifecycle: components did not stop within timeout")

// Failed 报告 Start 收到的 ctx 是否因为同组组件出错或提前退出而取消，而不是收到停止信号（信号和父 ctx 取消的
// cause 都是 context.Canceled）。组件据此跳过只对正常下线有意义的等待，例如 HTTP 排空。
func Failed(ctx context.Context) bool {
	return ctx.Err() != nil && !errors.Is(context.Cause(ctx), context.Canceled)
}

// Run 并发启动全部组件；任一组件返回错误、提前退出或 ctx 被取消时，按注册
// 逆序在 timeout 内 Stop 全部组件，返回第一个错误（ctx 取消本身不算错误）。
func Run(ctx context.Context, timeout time.Duration, cs ...Component) error {
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	var wg sync.WaitGroup
	for _, c := range cs {
		wg.Go(func() {
			err := c.Start(runCtx)
			switch {
			case err != nil && !errors.Is(err, context.Canceled):
				cancel(fmt.Errorf("%s: %w", c.Name(), err))
			case runCtx.Err() == nil:
				cancel(fmt.Errorf("%s: %w", c.Name(), ErrUnexpectedExit))
			}
		})
	}

	<-runCtx.Done()
	cause := context.Cause(runCtx)
	if errors.Is(cause, context.Canceled) {
		cause = nil
	}

	stopCtx, stopCancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer stopCancel()

	var errs []error
	for _, c := range slices.Backward(cs) {
		if err := c.Stop(stopCtx); err != nil {
			errs = append(errs, fmt.Errorf("%s: stop: %w", c.Name(), err))
		}
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-stopCtx.Done():
		errs = append(errs, ErrStopTimeout)
	}

	return errors.Join(append([]error{cause}, errs...)...)
}
