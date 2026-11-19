// Package jobs 是 staff 的 river 适配层：操作日志按保留期清理。
package jobs

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"app.local/internal/kit/logctx"
)

// AuditCleanupArgs 是定时任务的参数，每次入队都相同；截止时间在 Work 里按当前时间计算。
type AuditCleanupArgs struct{}

func (AuditCleanupArgs) Kind() string { return "staff_audit_cleanup" }

type AuditPurger interface {
	PurgeAudit(ctx context.Context, retention time.Duration) (int64, error)
}

type AuditCleanupWorker struct {
	river.WorkerDefaults[AuditCleanupArgs]
	purger    AuditPurger
	retention time.Duration
}

func NewAuditCleanupWorker(p AuditPurger, retention time.Duration) *AuditCleanupWorker {
	return &AuditCleanupWorker{purger: p, retention: retention}
}

func (w *AuditCleanupWorker) Work(ctx context.Context, _ *river.Job[AuditCleanupArgs]) error {
	n, err := w.purger.PurgeAudit(ctx, w.retention)
	if err != nil {
		return err
	}
	logctx.From(ctx).InfoContext(ctx, "staff: purged audit logs", "deleted", n, "retention", w.retention)
	return nil
}
