// Package logctx 在 context 里携带 *slog.Logger 和 request_id。这是唯一
// 允许的 context.Value 用法：横切的可观测性信息，不是业务依赖。
package logctx

import (
	"context"
	"log/slog"
)

type loggerKey struct{}

type requestIDKey struct{}

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

// WithRequestID 单独存原始 request_id，供跨进程传播（写进任务参数）时读取；
// 已绑定字段的 logger 无法反查出这个值。
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID 没有绑定时返回空字符串：不是所有调用都来自一次 HTTP 请求。
func RequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	return ""
}

// WithRequest 同时绑定 request_id 和带 request_id 字段的 logger。
func WithRequest(ctx context.Context, base *slog.Logger, requestID string) context.Context {
	ctx = WithRequestID(ctx, requestID)
	return WithLogger(ctx, base.With("request_id", requestID))
}
