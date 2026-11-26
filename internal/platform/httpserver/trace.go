package httpserver

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"app.local/internal/kit/logctx"
	"app.local/internal/platform/tracing"
)

// HeaderTraceID 是网关传入、响应写回 trace_id 的请求头：网关 access log、应用日志、trace、
// 后台操作日志用同一个值。
const HeaderTraceID = "X-Trace-Id"

const tracerName = "app.local/internal/platform/httpserver"

// newRequestMiddleware 是每个请求的入口，排在中间件链最外层：
//   - 建 server span（健康检查和 /metrics 不建），trace_id 写进响应头 X-Trace-Id；
//   - 在 ctx 里绑定 logger 和关联字段 method、route，日志的 trace_id 由根 Handler 从 span 取；
//   - 客户端自报的 X-Client-*、X-Installation-Id（ParseClientInfo）只进日志字段，不进 span 属性（客户端可任意取值），
//     经 trace_id 与 trace 关联；
//   - 只有 trustProxy 为真（在网关后）才接受上游的 traceparent 和 X-Trace-Id，否则一律新生成：
//     直连时客户端能指定 trace_id，把自己的请求混进别人的日志。
//
// Echo 先匹配路由再执行 Use 链，这里的 c.Path() 已是路由模板，span 名直接用 "{method} {route}"。
func newRequestMiddleware(tp trace.TracerProvider, trustProxy bool, log *slog.Logger) echo.MiddlewareFunc {
	tracer := tp.Tracer(tracerName)
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) (err error) {
			req := c.Request()
			ctx := req.Context()
			if trustProxy {
				ctx = upstreamTrace(ctx, req.Header)
			}
			method, route := methodName(req.Method), c.Path()
			client := ParseClientInfo(req.Header)

			var span trace.Span
			if !healthEndpointSkipper(c) {
				name := method
				if route != "" {
					name += " " + route
				}
				ctx, span = tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindServer),
					trace.WithAttributes(attribute.String("http.request.method", method), attribute.String("http.route", route)))
				c.Response().Header().Set(HeaderTraceID, span.SpanContext().TraceID().String())
			}

			ctx = logctx.WithRoute(logctx.WithLogger(ctx, log), method, route)
			ctx = logctx.WithClient(ctx, client.Platform, client.OS, client.Version, client.InstallationID)
			c.SetRequest(req.WithContext(ctx))
			c.SetLogger(logctx.From(ctx))
			if span == nil {
				return next(c)
			}

			defer func() {
				if r := recover(); r != nil {
					// Recover 原样再抛的 http.ErrAbortHandler：连接被中断，没有响应。
					span.SetStatus(codes.Error, "panic")
					span.End()
					panic(r)
				}
			}()
			err = next(c)
			status := responseStatus(c, err)
			span.SetAttributes(attribute.Int("http.response.status_code", status))
			if status >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(status))
			}
			span.End()
			return err
		}
	}
}

// upstreamTrace 按优先级取上游的 trace：traceparent（以后拆出的内部服务之间）→ X-Trace-Id（网关）。
// 都没有或不合法时 ctx 不变，SDK 随机生成。
func upstreamTrace(ctx context.Context, h http.Header) context.Context {
	parent := propagation.TraceContext{}.Extract(ctx, propagation.HeaderCarrier(h))
	if trace.SpanContextFromContext(parent).IsValid() {
		return parent
	}
	if id, ok := tracing.ParseTraceID(h.Get(HeaderTraceID)); ok {
		return tracing.WithRemoteTraceID(ctx, id)
	}
	return ctx
}

// TraceID 返回 ctx 所属请求的 trace_id，没有时为空字符串。需要持久化这个 ID 的适配器（如后台操作日志）
// 经它取值，模块不直接依赖 OpenTelemetry。
func TraceID(ctx context.Context) string {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		return sc.TraceID().String()
	}
	return ""
}
