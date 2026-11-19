package postgres

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"app.local/internal/kit/page"
)

func TestWhere_OnlyConditionsWithValues(t *testing.T) {
	var w Where
	if w.sql() != "" {
		t.Fatalf("空条件 = %q", w.sql())
	}
	day := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	w.Eq("enabled", nil)
	w.Eq("enabled", true)
	w.In("status", nil)
	w.In("status", []string{"a", "b"})
	w.Range("created_at", &day, nil)
	w.Contains("")
	w.Contains("50%", "name", "code")
	want := " WHERE enabled = $1 AND status = ANY($2) AND created_at >= $3 AND (name ILIKE $4 OR code ILIKE $4)"
	if got := w.sql(); got != want {
		t.Errorf("sql = %q\nwant %q", got, want)
	}
	if fmt.Sprint(w.args) != fmt.Sprint([]any{true, []string{"a", "b"}, day, `%50\%%`}) {
		t.Errorf("args = %v", w.args)
	}
}

func TestOrderBy_AlwaysEndsWithID(t *testing.T) {
	if got := orderBy(page.Sort{Field: "sort_order"}); got != ` ORDER BY "sort_order", id` {
		t.Errorf("正序 = %q", got)
	}
	if got := orderBy(page.Sort{Field: "created_at", Desc: true}); got != ` ORDER BY "created_at" DESC, id DESC` {
		t.Errorf("倒序 = %q", got)
	}
}

func TestUniqueViolation(t *testing.T) {
	err := fmt.Errorf("wrap: %w", &pgconn.PgError{Code: "23505", ConstraintName: "uk_categories_name"})
	if got := UniqueViolation(err); got != "uk_categories_name" {
		t.Errorf("UniqueViolation = %q", got)
	}
	if got := UniqueViolation(&pgconn.PgError{Code: "23502"}); got != "" {
		t.Errorf("非唯一约束 = %q", got)
	}
	if got := UniqueViolation(errors.New("x")); got != "" {
		t.Errorf("普通错误 = %q", got)
	}
}
