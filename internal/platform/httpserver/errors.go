package httpserver

import (
	"cmp"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"app.local/internal/kit/errs"
)

// ErrorBody 是所有错误响应的 JSON 结构，与 api/openapi.yaml 的 Error schema 一致。
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// RetryAfter 是多少秒之后可以再试，只在需要等待的错误（429）里出现，同时写进 Retry-After 响应头。
	RetryAfter int `json:"retry_after,omitempty"`
}

func statusOf(k errs.Kind) int {
	switch k {
	case errs.KindInvalid:
		return http.StatusBadRequest
	case errs.KindUnauthorized:
		return http.StatusUnauthorized
	case errs.KindForbidden:
		return http.StatusForbidden
	case errs.KindNotFound:
		return http.StatusNotFound
	case errs.KindConflict:
		return http.StatusConflict
	case errs.KindTooManyRequests:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

// describe 返回错误处理器会给 err 渲染的状态码和响应体。
func describe(err error) (int, ErrorBody) {
	status, body := http.StatusInternalServerError, ErrorBody{Code: "internal_error", Message: "Internal Server Error"}
	if he, ok := errors.AsType[*echo.HTTPError](err); ok {
		status, body.Code = he.Code, "http_error"
		// Echo 有的 HTTPError 不带 message（如 Bind 遇到不认识的 Content-Type 返回的 415），用状态码的标准文案。
		body.Message = cmp.Or(he.Message, http.StatusText(he.Code))
	} else if sc, ok := err.(interface{ StatusCode() int }); ok {
		// Echo 内置的 sentinel（如 BodyLimit 的 ErrStatusRequestEntityTooLarge）走这里。
		status, body = sc.StatusCode(), ErrorBody{Code: "http_error", Message: err.Error()}
	} else if be, ok := errs.As(err); ok && be.Kind != errs.KindInternal {
		status, body = statusOf(be.Kind), ErrorBody{Code: be.Code, Message: be.Message}
		if be.RetryAfter > 0 {
			body.RetryAfter = int(math.Ceil(be.RetryAfter.Seconds()))
		}
	}
	return status, body
}

// StatusOf 返回错误处理器会给 err 渲染的 HTTP 状态码，供需要在 handler 返回后记录结果的
// 中间件（如后台操作日志）使用：那时错误还没有被渲染成响应。
func StatusOf(err error) int {
	status, _ := describe(err)
	return status
}

// newHTTPErrorHandler 只把 *errs.Error 的 Code/Message 返回给客户端，绝不返回
// err.Error()：包装链里可能有 SQL、内部路径或调用链前缀。其余错误一律是固定
// 文案的 500，排查信息走带 request_id 的日志。
func newHTTPErrorHandler() echo.HTTPErrorHandler {
	return func(c *echo.Context, err error) {
		if r, uErr := echo.UnwrapResponse(c.Response()); uErr == nil && r.Committed {
			return
		}

		// c.Logger() 是 RequestID 中间件绑好 request_id 的 per-request logger。
		logger := c.Logger()

		status, body := describe(err)

		attrs := []any{"status", status, "code", body.Code}
		if pe, ok := errors.AsType[*middleware.PanicStackError](err); ok {
			attrs = append(attrs, "error", pe.Err, "stack", string(pe.Stack))
		} else {
			attrs = append(attrs, "error", err)
		}
		if be, ok := errs.As(err); ok {
			attrs = append(attrs, "error_kind", be.Kind.String())
		}
		logger.Log(c.Request().Context(), levelOf(status), "http request failed", attrs...)

		if body.RetryAfter > 0 {
			c.Response().Header().Set("Retry-After", strconv.Itoa(body.RetryAfter))
		}
		var writeErr error
		if c.Request().Method == http.MethodHead {
			writeErr = c.NoContent(status)
		} else {
			writeErr = c.JSON(status, body)
		}
		if writeErr != nil {
			logger.ErrorContext(c.Request().Context(), "failed to write error response", "error", writeErr)
		}
	}
}

// levelOf 让 Error 级别只留给服务端故障：4xx 是客户端的问题，其中鉴权失败、越权和触发限流
// 可能是攻击迹象，记 Warn；其余记 Info。
func levelOf(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status == http.StatusUnauthorized, status == http.StatusForbidden, status == http.StatusTooManyRequests:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}
