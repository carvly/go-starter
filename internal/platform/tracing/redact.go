package tracing

import (
	"slices"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// exceptionEventName 是 span.RecordError 写入的事件名（OTel 语义约定）。
const exceptionEventName = "exception"

// errorTextRemover 包在 span processor 外面：错误的完整信息只写日志，span 只标状态。插桩库（otelpgx、redisotel、
// otelriver）把 err.Error() 写进状态描述和 exception 事件，里面可能有 SQL、内部路径或用户数据，且都不能配置，
// 所以在交给 processor 之前统一去掉。
type errorTextRemover struct {
	sdktrace.SpanProcessor
}

func (p errorTextRemover) OnEnd(s sdktrace.ReadOnlySpan) {
	p.SpanProcessor.OnEnd(withoutErrorText{s})
}

type withoutErrorText struct {
	sdktrace.ReadOnlySpan
}

func (s withoutErrorText) Status() sdktrace.Status {
	st := s.ReadOnlySpan.Status()
	st.Description = ""
	return st
}

func (s withoutErrorText) Events() []sdktrace.Event {
	return slices.DeleteFunc(slices.Clone(s.ReadOnlySpan.Events()), func(e sdktrace.Event) bool {
		return e.Name == exceptionEventName
	})
}
