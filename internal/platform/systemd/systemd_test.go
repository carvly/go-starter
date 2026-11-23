//go:build unix

package systemd_test

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"app.local/internal/platform/systemd"
)

// 子进程模式：模拟被 systemd 启动的进程。LISTEN_PID 只能在子进程里取到自己的 pid 后再设置（systemd 在 fork
// 之后、exec 之前设置）。
func TestMain(m *testing.M) {
	if os.Getenv("SYSTEMD_TEST_CHILD") == "1" {
		_ = os.Setenv("LISTEN_PID", fmt.Sprint(os.Getpid()))
		lns, err := systemd.Listeners()
		if err != nil {
			fmt.Println("error:", err)
			os.Exit(0)
		}
		var addrs []string
		for _, ln := range lns {
			addrs = append(addrs, ln.Addr().String())
		}
		fmt.Printf("addrs=%s env=%q%q%q\n", strings.Join(addrs, ","),
			os.Getenv("LISTEN_PID"), os.Getenv("LISTEN_FDS"), os.Getenv("LISTEN_FDNAMES"))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runChild(t *testing.T, fds string, files ...*os.File) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "SYSTEMD_TEST_CHILD=1", "LISTEN_FDS="+fds, "LISTEN_FDNAMES=x")
	cmd.ExtraFiles = files // ExtraFiles[i] 在子进程里是 fd 3+i
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func listenerFile(t *testing.T) (*os.File, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	f, err := ln.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f, ln.Addr().String()
}

// fd 3 起按顺序成为 listener；读取后三个环境变量都被清除。
func TestListeners_InheritsFDsInOrderAndClearsEnv(t *testing.T) {
	f1, a1 := listenerFile(t)
	f2, a2 := listenerFile(t)
	got := runChild(t, "2", f1, f2)
	want := fmt.Sprintf(`addrs=%s,%s env=""""""`, a1, a2)
	if got != want {
		t.Errorf("child = %q, want %q", got, want)
	}
}

func TestListeners_RejectsBadEnvironment(t *testing.T) {
	for _, tc := range []struct{ name, pid, fds, wantErr string }{
		{"不是 socket activation 启动", "", "1", "LISTEN_PID is not set"},
		{"LISTEN_PID 属于别的进程", "1", "1", "is not this process"},
		{"LISTEN_FDS 非法", fmt.Sprint(os.Getpid()), "0", "must be a positive number"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LISTEN_PID", tc.pid)
			t.Setenv("LISTEN_FDS", tc.fds)
			t.Setenv("LISTEN_FDNAMES", "x")
			_, err := systemd.Listeners()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want 含 %q", err, tc.wantErr)
			}
			for _, k := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
				if v, ok := os.LookupEnv(k); ok {
					t.Errorf("出错时也应清除 %s，仍为 %q", k, v)
				}
			}
		})
	}
}

// 传进来的 fd 不是监听 socket（例如 .socket 写错成别的类型）时报错，指出是哪个 fd。
func TestListeners_RejectsNonSocketFD(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "notsock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if got := runChild(t, "1", f); !strings.Contains(got, "fd 3 is not a listening socket") {
		t.Errorf("child = %q", got)
	}
}

func TestNotify_WritesStateToNotifySocket(t *testing.T) {
	// unix socket 路径有长度上限（macOS 104 字节），t.TempDir() 太长。
	dir, err := os.MkdirTemp("/tmp", "sd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	names := map[string]string{"路径": filepath.Join(dir, "notify")}
	if runtime.GOOS == "linux" {
		names["抽象命名空间"] = "@" + filepath.Base(dir) + "-notify"
	}
	for name, addr := range names {
		t.Run(name, func(t *testing.T) {
			listenName := addr
			if strings.HasPrefix(addr, "@") {
				listenName = "\x00" + addr[1:]
			}
			conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: listenName, Net: "unixgram"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			t.Setenv("NOTIFY_SOCKET", addr)
			if err := systemd.Notify(systemd.Ready); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 64)
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			n, err := conn.Read(buf)
			if err != nil || string(buf[:n]) != "READY=1" {
				t.Errorf("got %q, %v", buf[:n], err)
			}
		})
	}
}

func TestNotify_NoSocketIsNoop(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	if err := systemd.Notify(systemd.Ready); err != nil {
		t.Errorf("Notify() = %v, want nil", err)
	}
}
