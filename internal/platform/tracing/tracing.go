// Package tracing 构造进程的 TracerProvider。始终是 SDK provider：未配置导出地址时 NeverSample、不挂
// exporter，不建立任何连接，但 SDK 先生成 ID 再做采样决定，每个请求和任务照样有 trace_id，日志靠它关联。
package tracing

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"net/url"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"app.local/internal/platform/process"
)

// shutdownTimeout 是关停时等待导出剩余 span 的上限，到期丢弃，不阻塞进程退出。
const shutdownTimeout = 3 * time.Second

const tracesPath = "v1/traces"

// New 的 endpoint 为空时不导出；非空时全量导出（AlwaysSample），导出量由 Collector 的尾部采样控制。
// endpoint 按 OTEL_EXPORTER_OTLP_ENDPOINT 的语义解释（base URL，拼上 /v1/traces），是导出地址的唯一来源；
// headers、超时等其余 OTEL_EXPORTER_OTLP_* 由 exporter 自己读环境变量。resource 只来自 id，与日志、指标的
// 进程身份同一来源，不读 OTEL_SERVICE_NAME / OTEL_RESOURCE_ATTRIBUTES。
func New(ctx context.Context, id process.Identity, endpoint string) (*sdktrace.TracerProvider, error) {
	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(resource.NewSchemaless(
			attribute.String("service.name", id.Name),
			attribute.String("service.version", id.Version),
			attribute.String("service.instance.id", id.InstanceID),
		)),
		sdktrace.WithIDGenerator(idGenerator{}),
	}
	if endpoint == "" {
		return sdktrace.NewTracerProvider(append(opts, sdktrace.WithSampler(sdktrace.NeverSample()))...), nil
	}
	target, err := url.JoinPath(endpoint, tracesPath)
	if err != nil {
		return nil, fmt.Errorf("tracing: parse OTLP endpoint: %w", err)
	}
	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(target))
	if err != nil {
		return nil, fmt.Errorf("tracing: create OTLP exporter: %w", err)
	}
	// 不看上游的采样标志：网关不做采样决定，全部导出交给 Collector 尾部采样。
	// BatchSpanProcessor 队列满时丢弃 span，不阻塞请求。
	return sdktrace.NewTracerProvider(append(opts, sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(errorTextRemover{sdktrace.NewBatchSpanProcessor(exporter)}))...), nil
}

// Disabled 是不导出的 provider，给没有注入 provider 的组件和测试用：trace_id 照样生成。
func Disabled() *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(sdktrace.WithIDGenerator(idGenerator{}), sdktrace.WithSampler(sdktrace.NeverSample()))
}

// NewWithProcessor 全量采样并交给 sp 处理，ID 生成规则与 New 相同。给测试用（如 tracetest.SpanRecorder），
// 断言实际产生的 span。
func NewWithProcessor(sp sdktrace.SpanProcessor) *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(sdktrace.WithIDGenerator(idGenerator{}), sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(errorTextRemover{sp}))
}

// Shutdown 导出剩余的 span，最多等 shutdownTimeout。在 HTTP server 和队列都停下之后调用。
func Shutdown(ctx context.Context, tp *sdktrace.TracerProvider) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	return tp.Shutdown(ctx)
}

type remoteTraceIDKey struct{}

// WithRemoteTraceID 让 ctx 里接下来新建的根 span 使用 id 作为 trace_id：网关给的 ID 就是这次请求的 trace_id，
// 根 span 仍是真正的根（没有伪造的父 span）。
func WithRemoteTraceID(ctx context.Context, id trace.TraceID) context.Context {
	return context.WithValue(ctx, remoteTraceIDKey{}, id)
}

// ParseTraceID 只接受 32 位小写或大写十六进制且不全为零的值，其余（包括客户端乱填的）一律忽略。
func ParseTraceID(s string) (trace.TraceID, bool) {
	var id trace.TraceID
	if len(s) != 2*len(id) {
		return id, false
	}
	if _, err := hex.Decode(id[:], []byte(s)); err != nil {
		return trace.TraceID{}, false
	}
	return id, id.IsValid()
}

// idGenerator 在 ctx 带有网关给的 trace_id 时用它，否则随机生成。SDK 只在新建根 span 时调用 NewIDs，
// 有父 span 时沿用父 span 的 trace_id。
type idGenerator struct{}

func (idGenerator) NewIDs(ctx context.Context) (trace.TraceID, trace.SpanID) {
	tid, ok := ctx.Value(remoteTraceIDKey{}).(trace.TraceID)
	if !ok || !tid.IsValid() {
		tid = randomTraceID()
	}
	return tid, randomSpanID()
}

func (idGenerator) NewSpanID(context.Context, trace.TraceID) trace.SpanID {
	return randomSpanID()
}

func randomTraceID() trace.TraceID {
	var id trace.TraceID
	for !id.IsValid() {
		binary.BigEndian.PutUint64(id[:8], rand.Uint64())
		binary.BigEndian.PutUint64(id[8:], rand.Uint64())
	}
	return id
}

func randomSpanID() trace.SpanID {
	var id trace.SpanID
	for !id.IsValid() {
		binary.BigEndian.PutUint64(id[:], rand.Uint64())
	}
	return id
}
