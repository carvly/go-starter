package queue

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"runtime/debug"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"app.local/internal/kit/logctx"
)

// metadataRequestID 是任务 metadata 里保存发起请求的 request_id 的键。
const metadataRequestID = "request_id"

// insertMiddleware 入队时把当前请求的 request_id 写进任务 metadata，任务执行时由
// workMiddleware 恢复。所有经 river 入队的任务自动带上，事件不需要自己声明这个字段。
type insertMiddleware struct{ river.MiddlewareDefaults }

func (*insertMiddleware) InsertMany(
	ctx context.Context,
	params []*rivertype.JobInsertParams,
	doInner func(context.Context) ([]*rivertype.JobInsertResult, error),
) ([]*rivertype.JobInsertResult, error) {
	if id := logctx.RequestID(ctx); id != "" {
		for _, p := range params {
			p.Metadata = withRequestID(p.Metadata, id)
		}
	}
	return doInner(ctx)
}

// withRequestID 合并进已有 metadata；已有同名键或 metadata 不是 JSON 对象时原样返回，
// 日志关联字段不能让入队失败。
func withRequestID(metadata []byte, id string) []byte {
	m := map[string]json.RawMessage{}
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &m); err != nil || m == nil {
			return metadata
		}
	}
	if _, ok := m[metadataRequestID]; ok {
		return metadata
	}
	m[metadataRequestID], _ = json.Marshal(id)
	out, err := json.Marshal(m)
	if err != nil {
		return metadata
	}
	return out
}

// workMiddleware 在任务执行的 ctx 里写入关联字段：job_kind、job_id、attempt，以及发起请求的
// request_id。这是任务日志关联字段的唯一入口，worker 不手写。
//
// 任务失败和 panic 也在这里用任务 ctx 记录：river 自己的 "Job errored"/"Job panicked" 日志用的是
// 它外层的 ctx，带不上 request_id 和 attempt。
type workMiddleware struct {
	river.MiddlewareDefaults
	log *slog.Logger
}

func (m *workMiddleware) Work(ctx context.Context, job *rivertype.JobRow, doInner func(context.Context) error) error {
	ctx = logctx.With(ctx,
		slog.String(logctx.KeyJobKind, job.Kind),
		slog.Int64(logctx.KeyJobID, job.ID),
		slog.Int(logctx.KeyAttempt, job.Attempt),
	)
	if id := requestIDOf(job); id != "" {
		ctx = logctx.WithRequestID(ctx, id)
	}
	defer func() {
		if r := recover(); r != nil {
			m.log.ErrorContext(ctx, "queue: job panicked", "panic", r, "stack", string(debug.Stack()))
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
		m.log.WarnContext(ctx, "queue: job cancelled", "error", err)
	case job.Attempt >= job.MaxAttempts:
		m.log.ErrorContext(ctx, "queue: job failed, no retries left", "error", err)
	default:
		m.log.WarnContext(ctx, "queue: job failed, will retry", "error", err)
	}
}

func requestIDOf(job *rivertype.JobRow) string {
	var v struct {
		RequestID string `json:"request_id"`
	}
	if len(job.Metadata) > 0 && json.Unmarshal(job.Metadata, &v) == nil {
		return v.RequestID
	}
	return ""
}
