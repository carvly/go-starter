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

	"app.local/internal/kit/logctx"
	"app.local/internal/platform/logger"
)

func TestInsertMiddleware_WritesRequestIDIntoMetadata(t *testing.T) {
	params := []*rivertype.JobInsertParams{
		{Kind: "a"},
		{Kind: "b", Metadata: []byte(`{"other":1}`)},
		{Kind: "c", Metadata: []byte(`{"request_id":"keep"}`)},
		{Kind: "d", Metadata: []byte(`not json`)},
	}
	ctx := logctx.WithRequestID(t.Context(), "rid-1")
	called := false
	_, err := (&insertMiddleware{}).InsertMany(ctx, params, func(context.Context) ([]*rivertype.JobInsertResult, error) {
		called = true
		return nil, nil
	})
	if err != nil || !called {
		t.Fatalf("err = %v, called = %v", err, called)
	}

	want := []string{"rid-1", "rid-1", "keep"}
	for i, w := range want {
		var m map[string]any
		if err := json.Unmarshal(params[i].Metadata, &m); err != nil {
			t.Fatalf("%s metadata = %s", params[i].Kind, params[i].Metadata)
		}
		if m["request_id"] != w {
			t.Errorf("%s request_id = %v, want %s", params[i].Kind, m["request_id"], w)
		}
	}
	if string(params[1].Metadata) == `{"request_id":"rid-1"}` {
		t.Error("应保留已有的 metadata 键")
	}
	if string(params[3].Metadata) != "not json" {
		t.Errorf("非 JSON 的 metadata 应原样保留：%s", params[3].Metadata)
	}
}

func TestInsertMiddleware_NoRequestIDLeavesMetadataUntouched(t *testing.T) {
	params := []*rivertype.JobInsertParams{{Kind: "a"}}
	_, _ = (&insertMiddleware{}).InsertMany(t.Context(), params, func(context.Context) ([]*rivertype.JobInsertResult, error) {
		return nil, nil
	})
	if params[0].Metadata != nil {
		t.Errorf("没有 request_id 时不应写 metadata：%s", params[0].Metadata)
	}
}

func TestWorkMiddleware_AddsJobFieldsAndRequestID(t *testing.T) {
	cases := []struct {
		name string
		job  rivertype.JobRow
		want string
	}{
		{"metadata 优先", rivertype.JobRow{Metadata: []byte(`{"request_id":"from-meta"}`), EncodedArgs: []byte(`{"request_id":"from-args"}`)}, "from-meta"},
		{"回退到任务参数", rivertype.JobRow{Metadata: []byte(`{}`), EncodedArgs: []byte(`{"request_id":"from-args"}`)}, "from-args"},
		{"都没有", rivertype.JobRow{Metadata: []byte(`{}`), EncodedArgs: []byte(`{"user_id":"u"}`)}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := logger.New("info", &buf)
			job := tc.job
			job.ID, job.Kind, job.Attempt = 7, "user_deleted", 2

			err := (&workMiddleware{log: log}).Work(t.Context(), &job, func(ctx context.Context) error {
				log.InfoContext(ctx, "inside job")
				if logctx.RequestID(ctx) != tc.want {
					t.Errorf("RequestID = %q, want %q", logctx.RequestID(ctx), tc.want)
				}
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
			if got, _ := rec["request_id"].(string); got != tc.want {
				t.Errorf("request_id = %q, want %q", got, tc.want)
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
				Metadata: []byte(`{"request_id":"rid-1"}`),
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
			if rec["request_id"] != "rid-1" || rec["job_kind"] != "user_deleted" ||
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
	job := rivertype.JobRow{ID: 7, Kind: "user_deleted", Attempt: 1, MaxAttempts: 3, Metadata: []byte(`{"request_id":"rid-1"}`)}
	defer func() {
		if r := recover(); r != "boom" {
			t.Fatalf("panic 应继续抛给 river，由它按失败处理：recover = %v", r)
		}
		var rec map[string]any
		if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
			t.Fatalf("%v: %s", err, buf.String())
		}
		if rec["level"] != "ERROR" || rec["msg"] != "queue: job panicked" || rec["panic"] != "boom" ||
			rec["request_id"] != "rid-1" || rec["job_kind"] != "user_deleted" {
			t.Errorf("panic 日志：%v", rec)
		}
	}()
	_ = (&workMiddleware{log: logger.New("info", &buf)}).Work(t.Context(), &job, func(context.Context) error {
		panic("boom")
	})
}

func TestNewConfig_UsesAppLoggerAndMiddleware(t *testing.T) {
	var buf bytes.Buffer
	cfg := newConfig(nil, nil, slog.New(slog.NewJSONHandler(&buf, nil)))
	if cfg.Logger == nil {
		t.Fatal("必须设置 Logger，否则 river 用自己的文本格式 logger")
	}
	cfg.Logger.InfoContext(logctx.WithRequestID(t.Context(), "rid-1"), "river says")
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil || rec["request_id"] != "rid-1" {
		t.Errorf("river 的日志应是带关联字段的 JSON：%s", buf.String())
	}
	var hasInsert, hasWork bool
	for _, m := range cfg.Middleware {
		_, isInsert := m.(rivertype.JobInsertMiddleware)
		_, isWork := m.(rivertype.WorkerMiddleware)
		hasInsert, hasWork = hasInsert || isInsert, hasWork || isWork
	}
	if !hasInsert || !hasWork {
		t.Errorf("Middleware 缺少入队或执行中间件：%v", cfg.Middleware)
	}
}
