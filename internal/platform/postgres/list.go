package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"app.local/internal/kit/page"
	"app.local/internal/platform/dbtx"
)

// Where 收集后台列表的筛选条件。只放有值的条件：拼进 WHERE 的每个条件都会影响查询计划，
// 没有值的条件放进去（如 col = $1 OR $1 IS NULL）会让索引用不上。列名来自调用方的代码，
// 不能来自用户输入。
type Where struct {
	conds []string
	args  []any
}

func (w *Where) arg(v any) string {
	w.args = append(w.args, v)
	return fmt.Sprintf("$%d", len(w.args))
}

// Eq 加一个 col = v 的条件；v 为 nil 时不加。
func (w *Where) Eq(col string, v any) {
	if v == nil {
		return
	}
	w.conds = append(w.conds, col+" = "+w.arg(v))
}

// In 加一个 col 取 values 之一的条件（枚举多选）；values 为空时不加。
func (w *Where) In(col string, values []string) {
	if len(values) == 0 {
		return
	}
	w.conds = append(w.conds, col+" = ANY("+w.arg(values)+")")
}

// Range 加半开区间 [from, to) 的条件，两端各自可以为空。
func (w *Where) Range(col string, from, to *time.Time) {
	if from != nil {
		w.conds = append(w.conds, col+" >= "+w.arg(*from))
	}
	if to != nil {
		w.conds = append(w.conds, col+" < "+w.arg(*to))
	}
}

// Contains 加关键字搜索：任一列包含 keyword（不区分大小写，% _ 按字面匹配）；keyword 为空时不加。
func (w *Where) Contains(keyword string, cols ...string) {
	if keyword == "" || len(cols) == 0 {
		return
	}
	p := w.arg(ContainsPattern(keyword))
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = c + " ILIKE " + p
	}
	w.conds = append(w.conds, "("+strings.Join(parts, " OR ")+")")
}

func (w *Where) sql() string {
	if len(w.conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(w.conds, " AND ")
}

// orderBy 把 page.Sort 转成 ORDER BY，最后按 id 排一次：排序字段不唯一时，没有它翻页会重复或漏行。
func orderBy(s page.Sort) string {
	dir := ""
	if s.Desc {
		dir = " DESC"
	}
	return " ORDER BY " + pgx.Identifier{s.Field}.Sanitize() + dir + ", id" + dir
}

// ListPage 查后台列表的一页：SELECT columns FROM table WHERE … ORDER BY …, id LIMIT … OFFSET …，
// 总数用 CountCapped（最多数到 page.MaxTotal）。s.Field 必须来自 page.ParseSort 的白名单。
func ListPage[T any](ctx context.Context, q dbtx.Querier, table, columns string, w Where, s page.Sort, p page.Request, scan pgx.RowToFunc[T]) (page.Result[T], error) {
	from := " FROM " + table + w.sql()
	total, capped, err := CountCapped(ctx, q, "SELECT 1"+from, w.args...)
	if err != nil {
		return page.Result[T]{}, fmt.Errorf("postgres: list %s: %w", table, err)
	}
	n := len(w.args)
	query := fmt.Sprintf("SELECT %s%s%s LIMIT $%d OFFSET $%d", columns, from, orderBy(s), n+1, n+2)
	rows, err := q.Query(ctx, query, append(w.args, p.Size(), p.Offset())...)
	if err != nil {
		return page.Result[T]{}, fmt.Errorf("postgres: list %s: %w", table, err)
	}
	items, err := pgx.CollectRows(rows, scan)
	if err != nil {
		return page.Result[T]{}, fmt.Errorf("postgres: list %s: %w", table, err)
	}
	return page.Result[T]{Items: items, Total: total, TotalCapped: capped}, nil
}

// UniqueViolation 返回违反的唯一约束（或唯一索引）的名字，不是唯一约束冲突时返回空串。
// Repository 按名字把冲突换成领域错误，如 uk_categories_name → category_name_taken。
func UniqueViolation(err error) string {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
		return pgErr.ConstraintName
	}
	return ""
}
