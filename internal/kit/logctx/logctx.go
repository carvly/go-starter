// Package logctx 在 context 里携带 *slog.Logger 和日志关联字段（request_id、user_id、
// 任务 job_kind 等）。这是唯一允许的 context.Value 用法：横切的可观测性信息，不是业务依赖。
//
// 关联字段只存值，由 internal/platform/logger 的根 Handler 在输出每条记录时从 ctx 取出写入，
// 所以无论用哪个 logger（logctx.From、slog.Default、第三方库拿到的 logger），只要调用带
// ctx 的方法，字段都在。字段由入口（HTTP 中间件、鉴权、任务中间件）写入，业务代码不手写。
package logctx

import (
	"context"
	"log/slog"
)

// 关联字段的键名，全项目只用这一套。
const (
	KeyRequestID = "request_id"
	KeyUserID    = "user_id"
	KeyStaffID   = "staff_id"
	KeyMethod    = "method"
	KeyRoute     = "route"
	// 与 river 自己日志里的键一致，按任务类型检索时两边都能查到。
	KeyJobKind = "job_kind"
	KeyJobID   = "job_id"
	KeyAttempt = "attempt"
)

type loggerKey struct{}

type requestIDKey struct{}

type attrsKey struct{}

func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

// From 没有绑定 logger 时返回 slog.Default()，调用方不需要判空。
func From(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// With 返回追加了关联字段的 ctx；同名字段以后写入的为准。不修改 ctx 里已有的切片，
// 并发派生的子 ctx 互不影响。
func With(ctx context.Context, attrs ...slog.Attr) context.Context {
	if len(attrs) == 0 {
		return ctx
	}
	old := Attrs(ctx)
	merged := make([]slog.Attr, 0, len(old)+len(attrs))
	for _, a := range old {
		if !hasKey(attrs, a.Key) {
			merged = append(merged, a)
		}
	}
	for i, a := range attrs {
		if !hasKey(attrs[i+1:], a.Key) {
			merged = append(merged, a)
		}
	}
	return context.WithValue(ctx, attrsKey{}, merged)
}

// Attrs 返回 ctx 里的关联字段，调用方不能修改返回的切片。
func Attrs(ctx context.Context) []slog.Attr {
	attrs, _ := ctx.Value(attrsKey{}).([]slog.Attr)
	return attrs
}

func hasKey(attrs []slog.Attr, key string) bool {
	for _, a := range attrs {
		if a.Key == key {
			return true
		}
	}
	return false
}

// WithRequestID 记录 request_id：既作为关联字段输出，也单独保存原始值，供跨进程传播
// （入队时写进任务 metadata）读取。
func WithRequestID(ctx context.Context, id string) context.Context {
	ctx = context.WithValue(ctx, requestIDKey{}, id)
	return With(ctx, slog.String(KeyRequestID, id))
}

// RequestID 没有绑定时返回空字符串：不是所有调用都来自一次 HTTP 请求。
func RequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	return ""
}

// WithRequest 同时绑定 request_id 和这次请求使用的 logger。
func WithRequest(ctx context.Context, base *slog.Logger, requestID string) context.Context {
	return WithLogger(WithRequestID(ctx, requestID), base)
}
