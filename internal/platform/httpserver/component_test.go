package httpserver_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"app.local/internal/platform/httpserver"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

type runningServer struct {
	base   string
	cancel context.CancelFunc
	// fail 模拟同组的其它组件出错：lifecycle 以非 Canceled 的 cause 取消 ctx。
	fail context.CancelCauseFunc
	done chan error
}

func startComponent(t *testing.T, drain time.Duration) runningServer {
	t.Helper()
	addr := freeAddr(t)
	c := httpserver.Component("api", newTestServer(t, discardLogger(), false), addr, drain)
	ctx, fail := context.WithCancelCause(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.Start(ctx) }()
	base := "http://" + addr
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server 没有启动：%v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return runningServer{base: base, cancel: func() { fail(nil) }, fail: fail, done: done}
}

// getVia 用 client 发请求，返回响应和这次请求是否复用了已有连接。
func getVia(t *testing.T, client *http.Client, url string) (*http.Response, bool) {
	t.Helper()
	var reused bool
	ctx := httptrace.WithClientTrace(t.Context(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused },
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp, reused
}

// 排空期内：照常处理请求，每个响应带 Connection: close；排空开始前就空闲的连接不被服务端关闭（网关复用它
// 不会失败）；/readyz 返回 503。排空结束后进入优雅关闭，Start 正常返回。
func TestComponent_DrainKeepsServingWithConnectionClose(t *testing.T) {
	const drain = 500 * time.Millisecond
	s := startComponent(t, drain)
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 1}}
	t.Cleanup(client.CloseIdleConnections)

	if resp, _ := getVia(t, client, s.base+"/v1/widgets/1"); resp.Close {
		t.Fatal("排空前不应带 Connection: close")
	}
	s.cancel()
	time.Sleep(100 * time.Millisecond)

	resp, reused := getVia(t, client, s.base+"/v1/widgets/1")
	if !reused {
		t.Error("排空开始前就空闲的连接被服务端关闭了：网关复用它时请求会失败")
	}
	if resp.StatusCode != http.StatusNoContent || !resp.Close {
		t.Errorf("排空期应照常处理并带 Connection: close：status = %d, close = %v", resp.StatusCode, resp.Close)
	}
	if resp, _ := getVia(t, client, s.base+"/readyz"); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("排空期 /readyz = %d, want 503", resp.StatusCode)
	}

	select {
	case err := <-s.done:
		t.Fatalf("排空期内不应停止：%v", err)
	default:
	}
	select {
	case err := <-s.done:
		if err != nil {
			t.Errorf("Start() = %v, want nil", err)
		}
	case <-time.After(drain + 5*time.Second):
		t.Fatal("排空结束后没有停止")
	}
}

// 排空期为 0（默认）时行为与原来一致：立即优雅关闭。
func TestComponent_NoDrainStopsImmediately(t *testing.T) {
	s := startComponent(t, 0)
	start := time.Now()
	s.cancel()
	select {
	case err := <-s.done:
		if err != nil {
			t.Errorf("Start() = %v", err)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("没有排空期时应立即关闭，用了 %s", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("没有停止")
	}
}

// 同组组件出错导致的停止不排空：进程注定退出，排空只会让网关多把流量转给它。
func TestComponent_NoDrainWhenAnotherComponentFailed(t *testing.T) {
	s := startComponent(t, 5*time.Second)
	start := time.Now()
	s.fail(errors.New("admin: listen failed"))
	select {
	case err := <-s.done:
		if err != nil {
			t.Errorf("Start() = %v", err)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("组件出错时应立即关闭，用了 %s", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("没有停止")
	}
}

// 排空期 /readyz 的 503 由 RegisterReadyz 统一给出：没挂 /readyz 的 server（worker 内部端口）排空时仍是 404，
// 后台端口挂了 /readyz，排空时同样是 503，网关的主动检查据此摘掉它。
func TestComponent_DrainReadyzOnlyWhereRegistered(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    *echo.Echo
		want int
	}{
		{"internal", httpserver.NewInternal(discardLogger(), nil), http.StatusNotFound},
		{"admin", newAdminServer(t, httpserver.AdminDeps{}), http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr := freeAddr(t)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- httpserver.Component(tc.name, tc.e, addr, time.Second).Start(ctx) }()
			t.Cleanup(func() {
				if err := <-done; err != nil {
					t.Errorf("Start() = %v", err)
				}
			})
			waitUp(t, "http://"+addr)
			cancel()
			time.Sleep(100 * time.Millisecond)
			resp, _ := getVia(t, http.DefaultClient, "http://"+addr+"/readyz")
			if resp.StatusCode != tc.want {
				t.Errorf("排空期 /readyz = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func waitUp(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server 没有启动：%v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
