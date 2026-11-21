package queue

import (
	"context"
	"fmt"
	"log/slog"

	"app.local/internal/kit/logctx"
	"github.com/jackc/pgx/v5/pgxpool"
)

func orphanKinds(ctx context.Context, pool *pgxpool.Pool, known []string) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT DISTINCT kind FROM river_job WHERE finalized_at IS NULL ORDER BY kind`)
	if err != nil {
		return nil, fmt.Errorf("queue: list orphan kinds: %w", err)
	}
	defer rows.Close()
	knownSet := make(map[string]bool, len(known))
	for _, kind := range known {
		knownSet[kind] = true
	}
	var out []string
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			return nil, err
		}
		if !knownSet[kind] {
			out = append(out, kind)
		}
	}
	return out, rows.Err()
}

// CancelOrphans 取消当前二进制没有 worker 的全部非终态任务，返回每种 kind 的数量。
func CancelOrphans(ctx context.Context, pool *pgxpool.Pool, known []string) (map[string]int, error) {
	orphans, err := orphanKinds(ctx, pool, known)
	if err != nil || len(orphans) == 0 {
		return map[string]int{}, err
	}
	rows, err := pool.Query(ctx, `
		UPDATE river_job SET state = 'cancelled', finalized_at = now()
		WHERE finalized_at IS NULL AND kind = ANY($1) RETURNING kind`, orphans)
	if err != nil {
		return nil, fmt.Errorf("queue: cancel orphan jobs: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			return nil, err
		}
		out[kind]++
	}
	return out, rows.Err()
}

func WarnOrphans(ctx context.Context, pool *pgxpool.Pool, known []string, logger *slog.Logger) {
	orphans, err := orphanKinds(ctx, pool, known)
	if err != nil {
		logger.WarnContext(ctx, "queue: failed to check orphan jobs", "error", err)
		return
	}
	for _, kind := range orphans {
		logger.WarnContext(ctx, "queue: jobs without a registered worker; run api queue cancel-orphans after all instances are upgraded", logctx.KeyJobKind, kind)
	}
}
