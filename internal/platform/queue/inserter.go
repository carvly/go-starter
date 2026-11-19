// Package queue 是基于 riverqueue/river 的任务队列：构造 client，并提供与业务
// 无关的事务内入队能力。具体事件类型属于各业务模块（modules/<m>/events）。
package queue

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"app.local/internal/platform/dbtx"
)

// Inserter 把任务和业务写入放进同一个事务提交，语义上替代手写 outbox 表。
type Inserter struct {
	client *river.Client[pgx.Tx]
	handle *dbtx.Handle
}

func NewInserter(client *river.Client[pgx.Tx], handle *dbtx.Handle) *Inserter {
	return &Inserter{client: client, handle: handle}
}

// InsertTx 必须在 UnitOfWork.Run 的闭包内调用，否则返回 dbtx.ErrNoTransaction。
func (i *Inserter) InsertTx(ctx context.Context, args river.JobArgs) error {
	tx, err := i.handle.ResolveTx(ctx)
	if err != nil {
		return fmt.Errorf("queue: insert %s: %w", args.Kind(), err)
	}
	if _, err := i.client.InsertTx(ctx, tx, args, nil); err != nil {
		return fmt.Errorf("queue: insert %s: %w", args.Kind(), err)
	}
	return nil
}
