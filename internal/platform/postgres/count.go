package postgres

import (
	"context"
	"fmt"
	"strings"

	"app.local/internal/kit/page"
	"app.local/internal/platform/dbtx"
)

// CountCapped 数 query（不带 ORDER BY / LIMIT 的 SELECT）返回的行数，最多数到
// page.MaxTotal：多数一行判断是否超过，大表上不做完整的 count(*)。
func CountCapped(ctx context.Context, q dbtx.Querier, query string, args ...any) (total int, capped bool, err error) {
	var n int
	sql := fmt.Sprintf("SELECT count(*) FROM (%s LIMIT %d) AS t", query, page.MaxTotal+1)
	if err := q.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		return 0, false, fmt.Errorf("postgres: count: %w", err)
	}
	if n > page.MaxTotal {
		return page.MaxTotal, true, nil
	}
	return n, false, nil
}

// ContainsPattern 把用户输入的关键字转成 ILIKE 的"包含"模式，转义 % _ \，
// 搜索"50%"不会匹配所有含"50"的行。
func ContainsPattern(keyword string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(keyword) + "%"
}
