package errs_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"app.local/internal/kit/errs"
)

var errSample = errs.New(errs.KindNotFound, "sample_not_found", "sample not found")

func TestError_IsAndAsSurviveWrapping(t *testing.T) {
	wrapped := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", errSample))

	if !errors.Is(wrapped, errSample) {
		t.Error("errors.Is 应该穿透 %w 包装命中 sentinel")
	}
	got, ok := errs.As(wrapped)
	if !ok {
		t.Fatal("errs.As 应该取出业务错误")
	}
	if got.Code != "sample_not_found" || got.Message != "sample not found" || got.Kind != errs.KindNotFound {
		t.Errorf("errs.As = %+v", got)
	}
}

func TestAs_PlainError(t *testing.T) {
	if _, ok := errs.As(errors.New("plain")); ok {
		t.Error("普通错误不应该被识别为业务错误")
	}
}

func TestWithRetryAfter_KeepsSentinel(t *testing.T) {
	err := fmt.Errorf("outer: %w", errSample.WithRetryAfter(90*time.Second))
	if !errors.Is(err, errSample) {
		t.Error("带等待时间的副本也要命中原来的 sentinel")
	}
	got, _ := errs.As(err)
	if got.RetryAfter != 90*time.Second || got.Code != "sample_not_found" {
		t.Errorf("errs.As = %+v", got)
	}
	if errSample.RetryAfter != 0 {
		t.Error("WithRetryAfter 不能改动 sentinel 本身")
	}
}

func TestKind_String_NamesEveryKind(t *testing.T) {
	want := map[errs.Kind]string{
		errs.KindInternal:        "internal",
		errs.KindInvalid:         "invalid",
		errs.KindUnauthorized:    "unauthorized",
		errs.KindForbidden:       "forbidden",
		errs.KindNotFound:        "not_found",
		errs.KindConflict:        "conflict",
		errs.KindTooManyRequests: "too_many_requests",
		errs.KindUnavailable:     "unavailable",
	}
	for k := errs.KindInternal; k <= errs.KindUnavailable; k++ {
		if got := k.String(); got != want[k] {
			t.Errorf("Kind(%d).String() = %q, want %q", k, got, want[k])
		}
	}
	if got := errs.Kind(255).String(); got != "unknown" {
		t.Errorf("未知 Kind 的 String() = %q, want unknown", got)
	}
}
