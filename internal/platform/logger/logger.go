// Package logger 构造结构化日志（log/slog JSON）。在 context 里传递 logger
// 和 request_id 见 internal/kit/logctx。
package logger

import (
	"io"
	"log/slog"
	"os"
)

// New 在 level 不识别时回落到 info：日志级别配错不应该让服务启动失败。
func New(level string, w io.Writer) *slog.Logger {
	if w == nil {
		w = os.Stdout
	}
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: parseLevel(level)})
	return slog.New(handler)
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
