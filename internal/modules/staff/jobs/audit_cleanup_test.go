package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"app.local/internal/modules/staff/jobs"
)

type purger struct{ retention time.Duration }

func (p *purger) PurgeAudit(_ context.Context, r time.Duration) (int64, error) {
	p.retention = r
	return 3, nil
}

func TestAuditCleanupWorker_PassesRetention(t *testing.T) {
	p := &purger{}
	w := jobs.NewAuditCleanupWorker(p, 180*24*time.Hour)
	if err := w.Work(t.Context(), &river.Job[jobs.AuditCleanupArgs]{}); err != nil {
		t.Fatal(err)
	}
	if p.retention != 180*24*time.Hour {
		t.Errorf("retention = %v", p.retention)
	}
}
