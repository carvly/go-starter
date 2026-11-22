package tracing_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"app.local/internal/platform/process"
	"app.local/internal/platform/tracing"
)

const gatewayID = "4bf92f3577b34da6a3ce929d0e0e4736"

func TestParseTraceID(t *testing.T) {
	for in, ok := range map[string]bool{
		gatewayID:                          true,
		"4BF92F3577B34DA6A3CE929D0E0E4736": true,
		"00000000000000000000000000000000": false,
		"4bf92f3577b34da6":                 false,
		"zzf92f3577b34da6a3ce929d0e0e4736": false,
		"":                                 false,
	} {
		if _, got := tracing.ParseTraceID(in); got != ok {
			t.Errorf("ParseTraceID(%q) ok = %v, want %v", in, got, ok)
		}
	}
}

// 未配置导出地址也有 trace_id；网关给的 ID 成为根 span 的 trace_id，根 span 没有父 span。
func TestDisabledProvider_UsesRemoteTraceIDAndStillGeneratesIDs(t *testing.T) {
	tp := tracing.Disabled()
	tr := tp.Tracer("test")

	_, span := tr.Start(t.Context(), "random")
	if sc := span.SpanContext(); !sc.TraceID().IsValid() || !sc.SpanID().IsValid() || sc.IsSampled() {
		t.Errorf("未导出时应有 ID 且不采样：%+v", sc)
	}
	span.End()

	id, _ := tracing.ParseTraceID(gatewayID)
	ctx, root := tr.Start(tracing.WithRemoteTraceID(t.Context(), id), "root")
	if got := root.SpanContext().TraceID().String(); got != gatewayID {
		t.Errorf("trace_id = %s, want %s", got, gatewayID)
	}
	if trace.SpanContextFromContext(context.WithoutCancel(ctx)).TraceID() != id {
		t.Error("子 ctx 应带同一个 trace_id")
	}
	_, child := tr.Start(ctx, "child")
	if child.SpanContext().TraceID() != id {
		t.Error("子 span 应沿用父 span 的 trace_id")
	}
	child.End()
	root.End()
}

// 未配置导出地址时不建立任何连接；配置后才导出（这里只验证构造不连接、关停能完成）。
func TestNew_WithoutEndpointDoesNotDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan struct{}, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- struct{}{}
			_ = c.Close()
		}
	}()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://"+ln.Addr().String())

	tp, err := tracing.New(t.Context(), process.New("api", "test"), "")
	if err != nil {
		t.Fatal(err)
	}
	_, span := tp.Tracer("test").Start(t.Context(), "x")
	span.End()
	if err := tracing.Shutdown(t.Context(), tp); err != nil {
		t.Fatal(err)
	}
	select {
	case <-accepted:
		t.Error("未配置导出地址时不应建立连接")
	default:
	}
}

// 导出地址只来自配置（YAML 或 OTEL_EXPORTER_OTLP_ENDPOINT 读进来的值），按 OTLP 规范拼上 /v1/traces。
func TestNew_ExportsToConfiguredEndpoint(t *testing.T) {
	paths := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
	}))
	defer srv.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")

	tp, err := tracing.New(t.Context(), process.New("api", "test"), srv.URL+"/prefix")
	if err != nil {
		t.Fatal(err)
	}
	_, span := tp.Tracer("test").Start(t.Context(), "x")
	span.End()
	if err := tracing.Shutdown(t.Context(), tp); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-paths:
		if got != "/prefix/v1/traces" {
			t.Errorf("导出路径 = %s, want /prefix/v1/traces", got)
		}
	default:
		t.Fatal("配置的导出地址没有收到 span")
	}
}

// 错误的完整信息只写日志：插桩库（otelpgx、redisotel、otelriver）写进 span 的错误文本在导出前去掉，只留状态。
func TestProvider_DropsErrorTextFromSpans(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := tracing.NewWithProcessor(rec)
	_, span := tp.Tracer("test").Start(t.Context(), "x")
	span.AddEvent("kept")
	span.RecordError(errors.New("password=secret"))
	span.SetStatus(codes.Error, "password=secret")
	span.End()

	ended := rec.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d", len(ended))
	}
	s := ended[0]
	if s.Status().Code != codes.Error || s.Status().Description != "" {
		t.Errorf("status = %+v, want Error 且没有描述", s.Status())
	}
	for _, e := range s.Events() {
		if e.Name == "exception" {
			t.Errorf("exception 事件应去掉：%+v", e)
		}
	}
	if len(s.Events()) != 1 {
		t.Errorf("events = %+v, want 只剩 kept", s.Events())
	}
}
