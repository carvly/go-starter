//go:build unix

package logger_test

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

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

// systemd 231 之前（CentOS 7 的 219）不设置 JOURNAL_STREAM：stdout 是连到 journald stdout socket 的 unix socket 时
// 同样加前缀；连到别的 unix socket 时不加。
func TestNew_UnixSocketToJournaldWithoutJournalStream(t *testing.T) {
	t.Setenv("JOURNAL_STREAM", "")
	dir, err := os.MkdirTemp("/tmp", "jr") // unix socket 路径有长度上限，t.TempDir() 太长
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	journald := filepath.Join(dir, "stdout")
	t.Cleanup(logger.SetJournalStdoutSocket(journald))

	for name, tc := range map[string]struct {
		path       string
		wantPrefix string
	}{
		"journald":  {journald, "<3>{"},
		"别的 socket": {filepath.Join(dir, "other"), "{"},
	} {
		t.Run(name, func(t *testing.T) {
			ln, err := net.Listen("unix", tc.path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ln.Close() })
			conn, err := net.Dial("unix", tc.path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			f, err := conn.(*net.UnixConn).File()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = f.Close() })
			server, err := ln.Accept()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = server.Close() })

			logger.New("info", f).ErrorContext(t.Context(), "e")
			_ = server.SetReadDeadline(time.Now().Add(2 * time.Second))
			line, err := bufio.NewReader(server).ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(line, tc.wantPrefix) {
				t.Errorf("line = %q, want 前缀 %q", line, tc.wantPrefix)
			}
		})
	}
}
