package logctx_test

import (
	"context"
	"log/slog"
	"testing"

	"app.local/internal/kit/logctx"
)

func attrMap(ctx context.Context) map[string]string {
	out := map[string]string{}
	for _, a := range logctx.Attrs(ctx) {
		out[a.Key] = a.Value.String()
	}
	return out
}

func TestWith_AppendsAndLaterWriteWins(t *testing.T) {
	ctx := logctx.With(t.Context(), slog.String("a", "1"), slog.String("b", "1"))
	ctx = logctx.With(ctx, slog.String("b", "2"), slog.String("c", "1"))

	got := attrMap(ctx)
	if len(logctx.Attrs(ctx)) != 3 || got["a"] != "1" || got["b"] != "2" || got["c"] != "1" {
		t.Errorf("Attrs = %v, want a=1 b=2 c=1 且没有重复键", logctx.Attrs(ctx))
	}
}

func TestWith_DuplicateKeysInOneCallKeepLast(t *testing.T) {
	ctx := logctx.With(t.Context(), slog.String("a", "1"), slog.String("a", "2"))
	if attrs := logctx.Attrs(ctx); len(attrs) != 1 || attrs[0].Value.String() != "2" {
		t.Errorf("Attrs = %v, want 只有 a=2", attrs)
	}
}

func TestWith_DerivedContextsDoNotShareState(t *testing.T) {
	parent := logctx.With(t.Context(), slog.String("a", "1"))
	left := logctx.With(parent, slog.String("side", "left"))
	right := logctx.With(parent, slog.String("side", "right"))

	if attrMap(left)["side"] != "left" || attrMap(right)["side"] != "right" {
		t.Errorf("left = %v, right = %v", logctx.Attrs(left), logctx.Attrs(right))
	}
	if _, ok := attrMap(parent)["side"]; ok {
		t.Errorf("派生 ctx 不能改动父 ctx：%v", logctx.Attrs(parent))
	}
}

func TestWithRequestID_IsBothAttrAndRawValue(t *testing.T) {
	ctx := logctx.WithRequestID(t.Context(), "rid-1")
	if logctx.RequestID(ctx) != "rid-1" || attrMap(ctx)["request_id"] != "rid-1" {
		t.Errorf("RequestID = %q, Attrs = %v", logctx.RequestID(ctx), logctx.Attrs(ctx))
	}
}

func TestWithRequest_BindsLogger(t *testing.T) {
	base := slog.New(slog.DiscardHandler)
	ctx := logctx.WithRequest(t.Context(), base, "rid-1")
	if logctx.From(ctx) != base || logctx.RequestID(ctx) != "rid-1" {
		t.Error("WithRequest 应同时绑定 logger 和 request_id")
	}
}

func TestFrom_DefaultsWhenUnbound(t *testing.T) {
	if logctx.From(context.Background()) != slog.Default() {
		t.Error("未绑定 logger 时应返回 slog.Default()")
	}
}

func TestWithUserIDAndStaffID_AreCorrelationFields(t *testing.T) {
	ctx := logctx.WithStaffID(logctx.WithUserID(t.Context(), "u1"), "s1")
	if got := attrMap(ctx); got["user_id"] != "u1" || got["staff_id"] != "s1" {
		t.Errorf("Attrs = %v", logctx.Attrs(ctx))
	}
}
