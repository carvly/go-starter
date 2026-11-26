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

func TestWithLogger_Binds(t *testing.T) {
	base := slog.New(slog.DiscardHandler)
	if logctx.From(logctx.WithLogger(t.Context(), base)) != base {
		t.Error("WithLogger 应绑定 logger")
	}
}

func TestWithLinkTraceID_IsCorrelationField(t *testing.T) {
	ctx := logctx.WithLinkTraceID(t.Context(), "4bf92f3577b34da6a3ce929d0e0e4736")
	if attrMap(ctx)["link_trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("Attrs = %v", logctx.Attrs(ctx))
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

func TestWithClient_SkipsEmptyValues(t *testing.T) {
	ctx := logctx.WithClient(t.Context(), "android", "", "1.4.2+1042", "")
	got := attrMap(ctx)
	if len(got) != 2 || got["client_platform"] != "android" || got["client_version"] != "1.4.2+1042" {
		t.Errorf("Attrs = %v, want 只有 client_platform 和 client_version", logctx.Attrs(ctx))
	}
	if attrs := logctx.Attrs(logctx.WithClient(t.Context(), "", "", "", "")); len(attrs) != 0 {
		t.Errorf("全空时 Attrs = %v, want 空", attrs)
	}
}
