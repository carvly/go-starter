// Package logger 构造结构化日志（log/slog JSON）。关联字段（request_id、user_id、任务 kind 等）
// 存在 ctx 里（internal/kit/logctx），由这里的根 Handler 在输出时统一写入。
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"

	"app.local/internal/kit/logctx"
	"app.local/internal/platform/buildinfo"
)

// New 构造进程的根 logger：JSON 输出，attrs 是每条记录都带的静态字段（见 Process），
// 并从 ctx 补上关联字段。进程里其它 logger（slog.Default、Echo、river）都应由它派生，
// 否则拿不到关联字段。level 不识别时回落到 info：日志级别配错不应该让服务启动失败。
func New(level string, w io.Writer, attrs ...slog.Attr) *slog.Logger {
	if w == nil {
		w = os.Stdout
	}
	inner := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: parseLevel(level)})
	return slog.New(&contextHandler{inner: inner}).With(attrsToArgs(attrs)...)
}

// Ensure 保证 l 输出时带上 ctx 里的关联字段：New 构造的 logger（及其派生）原样返回，
// 其它 logger 包一层。接收外部 logger 的平台组件（HTTP server、river）都经它取用，
// 调用方传错 logger 也不会丢字段。
func Ensure(l *slog.Logger) *slog.Logger {
	if l == nil {
		return nil
	}
	if _, ok := l.Handler().(*contextHandler); ok {
		return l
	}
	return slog.New(&contextHandler{inner: l.Handler()})
}

// Process 是标识进程的静态字段：哪个服务、哪个版本、哪个实例。
func Process(service, instance string) []slog.Attr {
	return []slog.Attr{
		slog.String("service", service),
		slog.String("version", buildinfo.Version()),
		slog.String("instance", instance),
	}
}

func attrsToArgs(attrs []slog.Attr) []any {
	args := make([]any, len(attrs))
	for i, a := range attrs {
		args[i] = a
	}
	return args
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// contextHandler 在每条记录上追加 ctx 里的关联字段。调用方已经显式写了同名字段时以调用方为准，
// 不输出重复的键。不支持 WithGroup 之后的关联字段放在顶层：项目不用 slog 分组。
type contextHandler struct {
	inner slog.Handler
	// bound 是经 Logger.With 绑定过的键，这些键不再从 ctx 补。
	bound map[string]bool
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	attrs := logctx.Attrs(ctx)
	if len(attrs) == 0 {
		return h.inner.Handle(ctx, r)
	}
	present := make(map[string]bool, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		present[a.Key] = true
		return true
	})
	r = r.Clone()
	for _, a := range attrs {
		if !present[a.Key] && !h.bound[a.Key] {
			r.AddAttrs(a)
		}
	}
	return h.inner.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	bound := make(map[string]bool, len(h.bound)+len(attrs))
	for k := range h.bound {
		bound[k] = true
	}
	for _, a := range attrs {
		bound[a.Key] = true
	}
	return &contextHandler{inner: h.inner.WithAttrs(attrs), bound: bound}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{inner: h.inner.WithGroup(name), bound: h.bound}
}
