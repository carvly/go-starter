package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/riverqueue/rivercontrib/otelriver"

	"app.local/internal/kit/logctx"
	"app.local/internal/platform/logger"
	"app.local/internal/platform/tracing"
)

const (
	requestTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	jobTraceparent = `{"traceparent":"00-` + requestTraceID + `-00f067aa0ba902b7-01"}`
)

func TestWorkMiddleware_AddsJobFieldsAndLinkTraceID(t *testing.T) {
	cases := []struct {
		name string
		job  rivertype.JobRow
		want string
	}{
		{"入队时带了 traceparent", rivertype.JobRow{Metadata: []byte(jobTraceparent)}, requestTraceID},
		// 任务参数是业务数据，不是关联字段的来源。
		{"不读任务参数", rivertype.JobRow{Metadata: []byte(`{}`), EncodedArgs: []byte(jobTraceparent)}, ""},
		{"traceparent 不合法", rivertype.JobRow{Metadata: []byte(`{"traceparent":"garbage"}`)}, ""},
		{"定时任务没有 metadata", rivertype.JobRow{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := logger.New("info", &buf)
			job := tc.job
			job.ID, job.Kind, job.Attempt = 7, "user_deleted", 2

			err := (&workMiddleware{log: log}).Work(t.Context(), &job, func(ctx context.Context) error {
				log.InfoContext(ctx, "inside job")
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			var rec map[string]any
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatal(err)
			}
			if rec["job_kind"] != "user_deleted" || rec["job_id"] != float64(7) || rec["attempt"] != float64(2) {
				t.Errorf("任务字段缺失：%v", rec)
			}
			if got, _ := rec["link_trace_id"].(string); got != tc.want {
				t.Errorf("link_trace_id = %q, want %q", got, tc.want)
			}
		})
	}
}

// river 自己的 "Job errored"/"Job panicked" 日志用的是它外层的 ctx，没有关联字段；
// 任务失败的日志由 workMiddleware 用任务 ctx 记录。
func TestWorkMiddleware_LogsFailureWithCorrelationFields(t *testing.T) {
	cases := []struct {
		name      string
		attempt   int
		err       error
		wantLevel string
		wantMsg   string
	}{
		{"还会重试", 1, errors.New("boom"), "WARN", "queue: job failed, will retry"},
		{"最后一次", 3, errors.New("boom"), "ERROR", "queue: job failed, no retries left"},
		{"主动取消", 1, river.JobCancel(errors.New("boom")), "WARN", "queue: job cancelled"},
		{"延后执行不是失败", 1, river.JobSnooze(time.Minute), "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			job := rivertype.JobRow{
				ID: 7, Kind: "user_deleted", Attempt: tc.attempt, MaxAttempts: 3,
				Metadata: []byte(jobTraceparent),
			}
			err := (&workMiddleware{log: logger.New("info", &buf)}).Work(t.Context(), &job, func(context.Context) error {
				return tc.err
			})
			if !errors.Is(err, tc.err) {
				t.Fatalf("应原样返回任务的错误，river 据此决定重试：%v", err)
			}
			if tc.wantMsg == "" {
				if buf.Len() != 0 {
					t.Errorf("不应记录日志：%s", buf.String())
				}
				return
			}
			var rec map[string]any
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("%v: %s", err, buf.String())
			}
			if rec["level"] != tc.wantLevel || rec["msg"] != tc.wantMsg {
				t.Errorf("level/msg = %v/%v, want %s/%s", rec["level"], rec["msg"], tc.wantLevel, tc.wantMsg)
			}
			if rec["link_trace_id"] != requestTraceID || rec["job_kind"] != "user_deleted" ||
				rec["job_id"] != float64(7) || rec["attempt"] != float64(tc.attempt) {
				t.Errorf("失败日志缺关联字段：%v", rec)
			}
			if e, _ := rec["error"].(string); !strings.Contains(e, "boom") {
				t.Errorf("error = %v", rec["error"])
			}
		})
	}
}

func TestWorkMiddleware_LogsPanicAndRepanics(t *testing.T) {
	var buf bytes.Buffer
	job := rivertype.JobRow{ID: 7, Kind: "user_deleted", Attempt: 1, MaxAttempts: 3, Metadata: []byte(jobTraceparent)}
	defer func() {
		if r := recover(); r != "boom" {
			t.Fatalf("panic 应继续抛给 river，由它按失败处理：recover = %v", r)
		}
		var rec map[string]any
		if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
			t.Fatalf("%v: %s", err, buf.String())
		}
		if rec["level"] != "ERROR" || rec["msg"] != "queue: job panicked" || rec["panic"] != "boom" ||
			rec["link_trace_id"] != requestTraceID || rec["job_kind"] != "user_deleted" {
			t.Errorf("panic 日志：%v", rec)
		}
		// 堆栈要能定位到 panic 的位置，即任务函数本身。
		if stack, _ := rec["stack"].(string); !strings.Contains(stack, "TestWorkMiddleware_LogsPanicAndRepanics") {
			t.Errorf("stack 应包含 panic 所在的函数：%q", stack)
		}
	}()
	_ = (&workMiddleware{log: logger.New("info", &buf)}).Work(t.Context(), &job, func(context.Context) error {
		panic("boom")
	})
}

func TestNewConfig_UsesAppLoggerAndMiddleware(t *testing.T) {
	var buf bytes.Buffer
	cfg := newConfig(nil, nil, slog.New(slog.NewJSONHandler(&buf, nil)), tracing.Disabled())
	if cfg.Logger == nil {
		t.Fatal("必须设置 Logger，否则 river 用自己的文本格式 logger")
	}
	cfg.Logger.InfoContext(logctx.WithUserID(t.Context(), "u1"), "river says")
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil || rec["user_id"] != "u1" {
		t.Errorf("river 的日志应是带关联字段的 JSON：%s", buf.String())
	}
	// otelriver 必须在 workMiddleware 外层：后者的日志要拿到 work span 的 trace_id。
	if len(cfg.Middleware) != 2 {
		t.Fatalf("Middleware = %v", cfg.Middleware)
	}
	if _, ok := cfg.Middleware[0].(*otelriver.Middleware); !ok {
		t.Errorf("第一个中间件应是 otelriver：%T", cfg.Middleware[0])
	}
	if _, ok := cfg.Middleware[1].(*workMiddleware); !ok {
		t.Errorf("第二个中间件应是 workMiddleware：%T", cfg.Middleware[1])
	}
}
