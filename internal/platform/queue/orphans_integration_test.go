//go:build integration

package queue_test

import (
	"testing"

	"app.local/internal/platform/postgres/pgtest"
	"app.local/internal/platform/queue"
)

func TestCancelOrphans(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := t.Context()
	for _, item := range []struct {
		kind  string
		state string
	}{
		{"orphan", "available"}, {"orphan", "retryable"}, {"orphan", "running"}, {"orphan", "scheduled"}, {"orphan", "pending"},
		{"known", "available"}, {"known", "running"},
		{"done", "completed"}, {"done", "cancelled"}, {"done", "discarded"},
	} {
		finalized := "NULL"
		if item.kind == "done" {
			finalized = "now()"
		}
		if _, err := pool.Exec(ctx, `INSERT INTO river_job (kind, state, args, max_attempts, finalized_at) VALUES ($1, $2, '{}', 3, `+finalized+`)`, item.kind, item.state); err != nil {
			t.Fatal(err)
		}
	}
	got, err := queue.CancelOrphans(ctx, pool, []string{"known", "done"})
	if err != nil {
		t.Fatal(err)
	}
	if got["orphan"] != 5 || len(got) != 1 {
		t.Fatalf("cancelled = %v, want orphan:5", got)
	}
	var running int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind = 'orphan' AND state = 'running'`).Scan(&running); err != nil {
		t.Fatal(err)
	}
	if running != 0 {
		t.Errorf("running = %d, want 0", running)
	}
	var knownState string
	if err := pool.QueryRow(ctx, `SELECT state FROM river_job WHERE kind = 'known' AND state = 'available'`).Scan(&knownState); err != nil {
		t.Fatal(err)
	}
}
