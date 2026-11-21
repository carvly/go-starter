package user

import (
	"context"

	"app.local/internal/modules/user/events"
	"app.local/internal/platform/queue"
)

// publisher 用 queue.Inserter 实现 domain.Publisher。发布必须发生在 UnitOfWork.Run 的
// 闭包里（与注销同一事务）。
type publisher struct {
	q *queue.Inserter
}

func (p publisher) PublishUserDeleted(ctx context.Context, evt events.UserDeleted) error {
	return p.q.InsertTx(ctx, evt)
}
