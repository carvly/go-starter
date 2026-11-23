package app

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"app.local/internal/config"
	"app.local/internal/platform/httpserver"
)

func testListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := httpserver.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// 继承的 socket 按地址认领用途，与 ListenStream= 的顺序无关；认领不了、重复、缺了前台都在启动时失败：
// 没人 accept 的 socket 上的连接不会被拒绝，而是挂到客户端超时。
func TestMatchInherited(t *testing.T) {
	front, admin := testListener(t), testListener(t)
	cfg := config.HTTPConfig{Addr: front.Addr().String(), AdminAddr: admin.Addr().String()}

	got, err := matchInherited([]net.Listener{admin, front}, cfg)
	if err != nil || got.front != front || got.admin != admin {
		t.Fatalf("顺序打乱时 got = %+v, err = %v", got, err)
	}
	if got, err := matchInherited([]net.Listener{front}, cfg); err != nil || got.admin != nil {
		t.Fatalf("没有后台 socket 时 got = %+v, err = %v（有无后台由 checkAdminSocket 比对）", got, err)
	}
	noAdmin := config.HTTPConfig{Addr: cfg.Addr}
	for _, tc := range []struct {
		name    string
		lns     []net.Listener
		cfg     config.HTTPConfig
		wantErr string
	}{
		{"认领不了的地址", []net.Listener{front, testListener(t)}, cfg, "matches neither"},
		{"没配后台地址却传了后台 socket", []net.Listener{front, admin}, noAdmin, "matches neither"},
		{"重复的地址", []net.Listener{front, front}, cfg, "twice"},
		{"缺前台", []net.Listener{admin}, cfg, "no socket for HTTP_ADDR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := matchInherited(tc.lns, tc.cfg); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want 含 %q", err, tc.wantErr)
			}
		})
	}
}

// 不是 systemd socket activation 启动的进程（单元没写 Sockets=）在取 socket 时就失败，说明原因。
func TestInheritListeners_NotSocketActivated(t *testing.T) {
	t.Setenv("LISTEN_PID", "")
	_, err := inheritListeners(config.HTTPConfig{Addr: "127.0.0.1:8080"})
	if err == nil || !strings.Contains(err.Error(), "HTTP_SOCKET_ACTIVATION=true") || !strings.Contains(err.Error(), "LISTEN_PID") {
		t.Errorf("err = %v", err)
	}
}

type pingerFunc func(context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error { return f(ctx) }

// 依赖不可用时不发 READY，按间隔重试，恢复后返回；收到停止信号时放弃。
func TestWaitDependencies(t *testing.T) {
	calls := 0
	flaky := pingerFunc(func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("connection refused")
		}
		return nil
	})
	start := time.Now()
	if err := waitDependencies(t.Context(), []httpserver.Pinger{flaky}, discardLogger()); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || time.Since(start) < 2*time.Second {
		t.Errorf("calls = %d, elapsed = %s, want 3 次、间隔约 1s", calls, time.Since(start))
	}

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	down := pingerFunc(func(context.Context) error { return errors.New("down") })
	if err := waitDependencies(ctx, []httpserver.Pinger{down}, discardLogger()); err == nil {
		t.Error("ctx 取消后应返回错误")
	}
}
