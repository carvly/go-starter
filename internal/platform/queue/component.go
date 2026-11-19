package queue

import (
	"context"
	"errors"
	"sync"
	"time"

	"app.local/internal/kit/lifecycle"
)

// hardStopTimeout 是优雅停止超时后，强制取消正在执行的任务再等待它们退出的时间。
const hardStopTimeout = 5 * time.Second

type startStopper interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	StopAndCancel(ctx context.Context) error
}

// Component 把消费用的 river client 包成 lifecycle.Component。
func Component(client startStopper) lifecycle.Component {
	return &workerComponent{client: client}
}

type workerComponent struct {
	client startStopper

	mu      sync.Mutex
	stopped bool
}

func (w *workerComponent) Name() string { return "river worker" }

// Start 用不会被取消的 ctx 启动 client：取消 Start 的 ctx 在 river 里是硬停止，
// 会中断正在执行的任务；这里改由 Stop 先优雅停止、超时再强制取消。
//
// 启动和 Stop 用同一把锁串行：river 对还没启动的 client 调 Stop 什么也不做，
// 如果 Stop 抢在启动之前执行，之后启动的 client 就再也不会被停掉。
func (w *workerComponent) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.stopped || ctx.Err() != nil {
		w.mu.Unlock()
		return nil
	}
	err := w.client.Start(context.WithoutCancel(ctx))
	w.mu.Unlock()
	if err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

// Stop 先优雅停止（等正在执行的任务完成）；ctx 到期还没停完就取消任务的 ctx，
// 否则一个卡住的任务会让进程一直等到被强杀，任务停在 running 要很久才会被重试。
func (w *workerComponent) Stop(ctx context.Context) error {
	w.mu.Lock()
	w.stopped = true
	w.mu.Unlock()

	err := w.client.Stop(ctx)
	if err == nil || !errors.Is(err, ctx.Err()) {
		return err
	}
	hardCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hardStopTimeout)
	defer cancel()
	return errors.Join(err, w.client.StopAndCancel(hardCtx))
}
