package logger_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"app.local/internal/kit/logctx"
	"app.local/internal/platform/logger"
	"app.local/internal/platform/process"
	"app.local/internal/platform/tracing"
)

// lines 把 JSON 日志逐行解析；同一条记录里出现重复键时报错（json 解码会静默保留最后一个）。
func lines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		if _, err := dec.Token(); err != nil {
			t.Fatalf("不是 JSON：%q", line)
		}
		seen := map[string]bool{}
		for dec.More() {
			tok, err := dec.Token()
			if err != nil {
				t.Fatal(err)
			}
			key := tok.(string)
			if seen[key] {
				t.Errorf("重复的键 %q：%s", key, line)
			}
			seen[key] = true
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				t.Fatal(err)
			}
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func TestNew_AddsProcessAndContextAttrs(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New("info", &buf, process.New("api", "host-a").LogAttrs()...)
	ctx, span := tracing.Disabled().Tracer("test").Start(t.Context(), "x")
	defer span.End()
	ctx = logctx.With(ctx, slog.String("user_id", "u1"))
	traceID, spanID := span.SpanContext().TraceID().String(), span.SpanContext().SpanID().String()

	log.InfoContext(ctx, "x: happened", slog.String("order_id", "o1"))

	got := lines(t, &buf)[0]
	for key, want := range map[string]any{
		"service_name": "api", "service_instance_id": "host-a", "trace_id": traceID, "span_id": spanID,
		"user_id": "u1", "order_id": "o1",
	} {
		if got[key] != want {
			t.Errorf("%s = %v, want %v（记录：%v）", key, got[key], want, got)
		}
	}
	if v, _ := got["service_version"].(string); v == "" {
		t.Errorf("缺少 service_version：%v", got)
	}
}

func TestNew_DerivedLoggersKeepContextAttrs(t *testing.T) {
	var buf bytes.Buffer
	root := logger.New("info", &buf)
	ctx := logctx.WithUserID(t.Context(), "u1")

	// 经 With 派生、经 slog.New(Handler()) 重建的 logger（第三方库常见写法）都要带上关联字段。
	root.With(slog.String("component", "c")).InfoContext(ctx, "a")
	slog.New(root.Handler()).InfoContext(ctx, "b")

	for _, rec := range lines(t, &buf) {
		if rec["user_id"] != "u1" {
			t.Errorf("派生 logger 丢了关联字段：%v", rec)
		}
	}
}

func TestNew_ExplicitAttrWinsWithoutDuplicateKey(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New("info", &buf)
	ctx := logctx.With(t.Context(), slog.String("user_id", "from-ctx"))

	log.InfoContext(ctx, "explicit", slog.String("user_id", "explicit"))
	log.With(slog.String("user_id", "bound")).InfoContext(ctx, "bound")

	recs := lines(t, &buf)
	if recs[0]["user_id"] != "explicit" || recs[1]["user_id"] != "bound" {
		t.Errorf("显式字段应优先：%v", recs)
	}
}

func TestNew_NoContextAttrsWithoutCorrelation(t *testing.T) {
	var buf bytes.Buffer
	logger.New("info", &buf).InfoContext(t.Context(), "plain")
	if rec := lines(t, &buf)[0]; rec["trace_id"] != nil || rec["span_id"] != nil {
		t.Errorf("ctx 里没有 span 的记录（启动、关停）不应出现 trace_id：%v", rec)
	}
}

func TestNew_UnknownLevelFallsBackToInfo(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New("verbose", &buf)
	log.DebugContext(t.Context(), "hidden")
	log.InfoContext(t.Context(), "shown")
	if recs := lines(t, &buf); len(recs) != 1 || recs[0]["msg"] != "shown" {
		t.Errorf("records = %v", recs)
	}
}

func TestEnsure_WrapsForeignLoggerOnce(t *testing.T) {
	var buf bytes.Buffer
	foreign := slog.New(slog.NewJSONHandler(&buf, nil))
	wrapped := logger.Ensure(foreign)
	if logger.Ensure(wrapped) != wrapped {
		t.Error("已包装的 logger 不应再包一层")
	}
	root := logger.New("info", &buf)
	if logger.Ensure(root) != root {
		t.Error("New 构造的 logger 应原样返回")
	}

	wrapped.InfoContext(logctx.WithUserID(t.Context(), "u1"), "x")
	if rec := lines(t, &buf)[0]; rec["user_id"] != "u1" {
		t.Errorf("包装后应带上关联字段：%v", rec)
	}
	if logger.Ensure(nil) != nil {
		t.Error("Ensure(nil) 应返回 nil")
	}
}
