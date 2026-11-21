// Package errs 定义对外可见的业务错误：Kind 决定 HTTP 状态码，Code/
// Message 原样返回给客户端。传输层只认识 Kind，不认识任何具体模块的错误，
// 新增模块、新增错误都不需要改传输层。
package errs

import (
	"errors"
	"time"
)

type Kind uint8

const (
	KindInternal Kind = iota
	KindInvalid
	KindUnauthorized
	KindForbidden
	KindNotFound
	KindConflict
	// KindTooManyRequests 是需要等一段时间再试（如登录连续失败后的等待），用
	// WithRetryAfter 带上剩余时间。
	KindTooManyRequests
)

// String 是写进日志 error_kind 字段的名字，按类别检索和统计错误用。
func (k Kind) String() string {
	switch k {
	case KindInternal:
		return "internal"
	case KindInvalid:
		return "invalid"
	case KindUnauthorized:
		return "unauthorized"
	case KindForbidden:
		return "forbidden"
	case KindNotFound:
		return "not_found"
	case KindConflict:
		return "conflict"
	case KindTooManyRequests:
		return "too_many_requests"
	default:
		return "unknown"
	}
}

// Error 的 Code 给机器读，Message 给人读，二者都必须可以安全地返回给客户端，
// 不能带内部调用链或实现细节。
type Error struct {
	Kind    Kind
	Code    string
	Message string
	// RetryAfter 大于 0 时表示多久之后可以再试，传输层写进 Retry-After 响应头和响应体。
	RetryAfter time.Duration

	sentinel *Error
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Is 让 WithRetryAfter 返回的副本仍然命中原来的 sentinel。
func (e *Error) Is(target error) bool { return e.sentinel != nil && target == e.sentinel }

// WithRetryAfter 返回带剩余等待时间的副本，errors.Is(副本, e) 仍然成立。
func (e *Error) WithRetryAfter(d time.Duration) *Error {
	c := *e
	c.RetryAfter = d
	c.sentinel = e
	if e.sentinel != nil {
		c.sentinel = e.sentinel
	}
	return &c
}

// New 返回的指针用作 sentinel：errors.Is 按指针相等判断，%w 包装后依然成立。
func New(kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

// As 取出错误链上最内层的业务错误；链上没有业务错误时 ok 为 false。
func As(err error) (*Error, bool) {
	return errors.AsType[*Error](err)
}
