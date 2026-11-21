//go:build unix

package logger_test

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"app.local/internal/platform/logger"
)

func streamID(t *testing.T, f *os.File) string {
	t.Helper()
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino)
}

func logAllLevels(t *testing.T, f *os.File) []string {
	t.Helper()
	log := logger.New("debug", f)
	log.DebugContext(t.Context(), "d")
	log.InfoContext(t.Context(), "i", slog.String("level", "ERROR"))
	log.WarnContext(t.Context(), "w")
	log.ErrorContext(t.Context(), "e")
	out, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

// 输出到 journald 时每行带级别前缀，journalctl -p 才能过滤；业务字段里出现 "level" 不影响判断。
func TestNew_JournalStreamGetsPriorityPrefix(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("JOURNAL_STREAM", streamID(t, f))

	got := logAllLevels(t, f)
	want := []string{`<7>{`, `<6>{`, `<4>{`, `<3>{`}
	if len(got) != len(want) {
		t.Fatalf("lines = %q", got)
	}
	for i, w := range want {
		if !strings.HasPrefix(got[i], w) {
			t.Errorf("第 %d 行 = %q, want 前缀 %q", i, got[i], w)
		}
	}
}

// 不是 journald 的那条流（终端、容器、被重定向的子进程）时输出不变，每行都是纯 JSON。
func TestNew_OtherStreamsUnchanged(t *testing.T) {
	for name, env := range map[string]string{"未设置": "", "指向别的流": "1:1"} {
		t.Run(name, func(t *testing.T) {
			f, err := os.Create(filepath.Join(t.TempDir(), "log"))
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("JOURNAL_STREAM", env)
			for _, line := range logAllLevels(t, f) {
				if !strings.HasPrefix(line, "{") {
					t.Errorf("不应带前缀：%q", line)
				}
			}
		})
	}
}
