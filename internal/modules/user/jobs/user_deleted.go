// Package jobs 是 user 的 river 适配层：过期行清理（oauth_states、过期/已吊销
// 超过保留期的 user_sessions）和 user_deleted 的编排 worker。
package jobs

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"

	"app.local/internal/kit/logctx"
	"app.local/internal/modules/user/events"
)

// UserDeletedWorker 消费 user 自己发布的 user_deleted，但它是编排角色：一个事件
// 牵动多个模块各自的清理，所以 cleaners 由组合根注入，而不是只清 user 自己的表
// （user 自己的数据在注销事务里已经删完）。
type UserDeletedWorker struct {
	river.WorkerDefaults[events.UserDeleted]
	cleaners []func(context.Context, string) error
}

func NewUserDeletedWorker[T interface {
	DeleteUserData(context.Context, string) error
}](cleaners []T) *UserDeletedWorker {
	w := &UserDeletedWorker{cleaners: make([]func(context.Context, string) error, 0, len(cleaners))}
	for _, c := range cleaners {
		w.cleaners = append(w.cleaners, c.DeleteUserData)
	}
	return w
}

func (w *UserDeletedWorker) Work(ctx context.Context, job *river.Job[events.UserDeleted]) error {
	ctx = logctx.WithUserID(ctx, job.Args.UserID)
	for _, c := range w.cleaners {
		if err := c(ctx, job.Args.UserID); err != nil {
			return err
		}
	}
	logctx.From(ctx).InfoContext(ctx, "user: user_deleted processed", slog.Int("cleaners", len(w.cleaners)))
	return nil
}
