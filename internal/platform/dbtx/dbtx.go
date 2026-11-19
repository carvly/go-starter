// Package dbtx 实现 uow.UnitOfWork，构建在原生 pgxpool.Pool/pgx.Tx 之上。
package dbtx

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"app.local/internal/kit/uow"
)

// Querier 是 *pgxpool.Pool 和 pgx.Tx 共同满足的最小接口子集——
// Repository 实现只依赖这几个方法，不关心背后到底是连接池还是事务。
type Querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type ctxKey struct{}

// Handle 把连接池包成私有字段：Repository 只能经 Resolve(ctx) 取连接，
// 语法上无法绕过外层事务拿到裸连接。
type Handle struct {
	pool *pgxpool.Pool
}

func NewHandle(pool *pgxpool.Pool) *Handle {
	return &Handle{pool: pool}
}

// Resolve 返回当前 ctx 上下文里正确的 Querier：如果处在一个
// UnitOfWork.Run 的事务闭包内，返回那个 pgx.Tx；否则返回连接池本身。
func (h *Handle) Resolve(ctx context.Context) Querier {
	if tx, ok := ctx.Value(ctxKey{}).(pgx.Tx); ok {
		return tx
	}
	return h.pool
}

// Ping 供 /readyz 探活。
func (h *Handle) Ping(ctx context.Context) error {
	return h.pool.Ping(ctx)
}

// ErrNoTransaction 由 ResolveTx 在 ctx 上下文里没有事务时返回。
var ErrNoTransaction = errors.New("dbtx: no transaction in context (must be called within UnitOfWork.Run)")

// ResolveTx 只在 UnitOfWork.Run 的闭包内才能拿到具体的 pgx.Tx，供
// river.Client.InsertTx 这类要求具体 pgx.Tx 类型的调用方使用。
func (h *Handle) ResolveTx(ctx context.Context) (pgx.Tx, error) {
	tx, ok := ctx.Value(ctxKey{}).(pgx.Tx)
	if !ok {
		return nil, ErrNoTransaction
	}
	return tx, nil
}

// Run 把 fn 包进事务，事务经 ctx 传给 fn 内调用的 Repository。ctx 上已有
// 事务时用 pgx.Tx.Begin 开 SAVEPOINT，不会静默开出第二个独立事务。
func (h *Handle) Run(ctx context.Context, fn func(ctx context.Context) error) error {
	if outerTx, ok := ctx.Value(ctxKey{}).(pgx.Tx); ok {
		nestedTx, err := outerTx.Begin(ctx)
		if err != nil {
			return fmt.Errorf("dbtx: begin nested transaction (savepoint): %w", err)
		}
		return runInTx(ctx, nestedTx, fn)
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("dbtx: begin transaction: %w", err)
	}
	return runInTx(ctx, tx, fn)
}

func runInTx(ctx context.Context, tx pgx.Tx, fn func(context.Context) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback(ctx)
			// panic 交给调用方那一层兜底（HTTP Recover 中间件、river worker）。
			panic(r)
		}
	}()

	txCtx, hooks := uow.WithHooks(context.WithValue(ctx, ctxKey{}, tx))
	if fnErr := fn(txCtx); fnErr != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return fmt.Errorf("dbtx: rollback failed: %v (original error: %w)", rbErr, fnErr)
		}
		return fnErr
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("dbtx: commit transaction: %w", err)
	}
	hooks.Committed(ctx)
	return nil
}

var _ uow.UnitOfWork = (*Handle)(nil)
