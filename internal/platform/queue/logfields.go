package queue

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"runtime/debug"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"app.local/internal/kit/logctx"
)

// workMiddleware 在任务执行的 ctx 里写入关联字段：job_kind、job_id、attempt，以及入队那次请求的
// link_trace_id。这是任务日志关联字段的唯一入口，worker 不手写。它排在 otelriver 之内，ctx 里已是这次执行的
// work span，日志的 trace_id、span_id 由根 Handler 从 span 取。
//
// 任务失败和 panic 也在这里用任务 ctx 记录：river 自己的 "Job errored"/"Job panicked" 日志用的是
// 它外层的 ctx，带不上 trace_id 和 attempt。
type workMiddleware struct {
	river.MiddlewareDefaults
	log *slog.Logger
}

func (m *workMiddleware) Work(ctx context.Context, job *rivertype.JobRow, doInner func(context.Context) error) error {
	ctx = logctx.WithJob(ctx, job.Kind, job.ID, job.Attempt)
	if id := linkTraceID(job); id != "" {
		ctx = logctx.WithLinkTraceID(ctx, id)
	}
	defer func() {
		if r := recover(); r != nil {
			m.log.ErrorContext(ctx, "queue: job panicked", slog.Any("panic", r), slog.String("stack", string(debug.Stack())))
			// 继续抛给 river：由它记录堆栈、按失败处理和重试。
			panic(r)
		}
	}()
	err := doInner(ctx)
	m.logFailure(ctx, job, err)
	return err
}

func (m *workMiddleware) logFailure(ctx context.Context, job *rivertype.JobRow, err error) {
	if err == nil {
		return
	}
	if _, ok := errors.AsType[*rivertype.JobSnoozeError](err); ok {
		return
	}
	switch _, cancelled := errors.AsType[*rivertype.JobCancelError](err); {
	case cancelled:
		m.log.WarnContext(ctx, "queue: job cancelled", slog.Any("error", err))
	case job.Attempt >= job.MaxAttempts:
		m.log.ErrorContext(ctx, "queue: job failed, no retries left", slog.Any("error", err))
	default:
		m.log.WarnContext(ctx, "queue: job failed, will retry", slog.Any("error", err))
	}
}

// linkTraceID 取入队时所在 trace 的 trace_id：otelriver 入队时开一个 insert_many span（当前 ctx 的子 span，没有
// 父 span 时是新的根），把它的 traceparent 写进任务 metadata，执行时以 span link 指向它。执行是一条新的 trace（任务
// 可能几小时后才执行，父子关系会让 trace 横跨很久），trace 又可能被采样丢掉，只有日志里的这个字段能把任务和入队方
// 连起来：请求里入队的是请求的 trace_id；worker 里入队的是上一个任务这次执行的 trace_id，顺着它的 link_trace_id
// 能追到最初的请求；定时任务和请求之外入队的，指向只有 insert_many span 的 trace，查不到其它日志。
//
// 自己解析 metadata 而不从 work span 取 link：不导出时 span 不记录（NeverSample），读不到 link；otelriver 的解析函数
// 不导出。键名和格式与 otelriver 一致由 inserter_integration_test.go 的端到端测试保证。
func linkTraceID(job *rivertype.JobRow) string {
	var v struct {
		Traceparent string `json:"traceparent"`
	}
	if len(job.Metadata) == 0 || json.Unmarshal(job.Metadata, &v) != nil || v.Traceparent == "" {
		return ""
	}
	ctx := propagation.TraceContext{}.Extract(context.Background(), propagation.MapCarrier{"traceparent": v.Traceparent})
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		return sc.TraceID().String()
	}
	return ""
}
