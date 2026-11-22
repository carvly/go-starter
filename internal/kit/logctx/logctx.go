// Package logctx 在 context 里携带 *slog.Logger 和日志关联字段（user_id、任务 job_kind 等）。
// 这是唯一允许的 context.Value 用法：横切的可观测性信息，不是业务依赖。trace_id、span_id 不在这里：
// 它们取自 ctx 里的 span，由 internal/platform/logger 的根 Handler 写入。
//
// 关联字段只存值，由 internal/platform/logger 的根 Handler 在输出每条记录时从 ctx 取出写入，
// 所以无论用哪个 logger（logctx.From、slog.Default、第三方库拿到的 logger），只要调用带
// ctx 的方法，字段都在。字段由入口（HTTP 中间件、鉴权、任务中间件）写入，业务代码不手写。
package logctx

import (
	"context"
	"log/slog"
)

// 关联字段的键名，全项目只用这一套。不导出：其它包只能经下面的 With* 写入，引用不到键名，
// 手写同名字段只能用字面量，由 sloglint 的 forbidden-keys 拦下。
const (
	keyUserID  = "user_id"
	keyStaffID = "staff_id"
	keyMethod  = "method"
	keyRoute   = "route"
	// 与 river 自己日志里的键一致，按任务类型检索时两边都能查到。
	keyJobKind = "job_kind"
	keyJobID   = "job_id"
	keyAttempt = "attempt"
	// 任务的 trace_id 是本次执行的；入队那次请求的 trace_id 经 span link 关联，记在这里。
	keyLinkTraceID = "link_trace_id"
)

type loggerKey struct{}

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

// WithUserID 记录这条日志涉及的前台用户：登录态请求里是当前用户（由鉴权写入），没有登录态的
// 路径（刷新令牌、注销任务）是被处理的那个用户。
func WithUserID(ctx context.Context, id string) context.Context {
	return With(ctx, slog.String(keyUserID, id))
}

// WithStaffID 记录当前操作的后台员工。
func WithStaffID(ctx context.Context, id string) context.Context {
	return With(ctx, slog.String(keyStaffID, id))
}

// WithRoute 记录请求的方法和路由模板。route 为空（没匹配上路由）时不写：原始路径不进日志字段。
func WithRoute(ctx context.Context, method, route string) context.Context {
	ctx = With(ctx, slog.String(keyMethod, method))
	if route != "" {
		ctx = With(ctx, slog.String(keyRoute, route))
	}
	return ctx
}

// WithJobKind 记录日志涉及的任务类型。
func WithJobKind(ctx context.Context, kind string) context.Context {
	return With(ctx, slog.String(keyJobKind, kind))
}

// WithJob 记录正在执行的任务：类型、ID、第几次执行。
func WithJob(ctx context.Context, kind string, id int64, attempt int) context.Context {
	return With(WithJobKind(ctx, kind), slog.Int64(keyJobID, id), slog.Int(keyAttempt, attempt))
}

// WithLinkTraceID 记录任务入队那次请求的 trace_id（span link 指向的 trace）。trace 会被采样丢掉，日志不会：
// 只有这个字段能把任务和发起它的请求连起来。
func WithLinkTraceID(ctx context.Context, id string) context.Context {
	return With(ctx, slog.String(keyLinkTraceID, id))
}
