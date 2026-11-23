package httpserver_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"app.local/internal/platform/httpserver"
)

func listen(t *testing.T) (net.Listener, string) {
	t.Helper()
	ln, err := httpserver.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln, ln.Addr().String()
}

type runningServer struct {
	base   string
	cancel context.CancelFunc
	// fail 模拟同组的其它组件出错：lifecycle 以非 Canceled 的 cause 取消 ctx。
	fail context.CancelCauseFunc
	done chan error
}

func startComponent(t *testing.T, stop httpserver.Stop) runningServer {
	t.Helper()
	ln, addr := listen(t)
	return startOn(t, newTestServer(t, discardLogger(), false), ln, addr, stop)
}

func startOn(t *testing.T, e *echo.Echo, ln net.Listener, addr string, stop httpserver.Stop) runningServer {
	t.Helper()
	c := httpserver.Component("api", e, ln, stop)
	ctx, fail := context.WithCancelCause(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.Start(ctx) }()
	base := "http://" + addr
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			http.DefaultClient.CloseIdleConnections() // 探测留下的空闲连接不应计入排空
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
	s := startComponent(t, httpserver.Stop{Drain: drain})
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
	s := startComponent(t, httpserver.Stop{})
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
	s := startComponent(t, httpserver.Stop{Drain: 5 * time.Second})
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
			ln, addr := listen(t)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() {
				done <- httpserver.Component(tc.name, tc.e, ln, httpserver.Stop{Drain: time.Second}).Start(ctx)
			}()
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

// acceptNotifier 在每次 Accept 返回连接时发信号，测试据此确认服务端已经接管了连接，而不是靠 sleep 猜。
type acceptNotifier struct {
	net.Listener
	accepted chan struct{}
}

func (l acceptNotifier) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.accepted <- struct{}{}
	}
	return c, err
}

// 关停时已被 accept、还没发出请求的连接（共享 socket 交接时由内核分给旧进程的连接）必须得到响应：
// http.Server.Shutdown 开始后读完请求的连接会被直接关闭而不响应，网关收到 EOF，非幂等请求直接 502。
func TestComponent_AcceptedConnectionGetsResponseDuringShutdown(t *testing.T) {
	for name, stop := range map[string]httpserver.Stop{
		"不排空":            {},
		"排空":             {Drain: 300 * time.Millisecond},
		"共享 listener 排空": {Drain: 300 * time.Millisecond, SharedListener: true},
	} {
		t.Run(name, func(t *testing.T) {
			raw, addr := listen(t)
			ln := acceptNotifier{Listener: raw, accepted: make(chan struct{}, 16)}
			s := startOn(t, newTestServer(t, discardLogger(), false), ln, addr, stop)
			<-ln.accepted // startOn 等待启动时的那次 /healthz 探测
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			select {
			case <-ln.accepted:
			case <-time.After(5 * time.Second):
				t.Fatal("服务端没有 accept 这条连接")
			}
			s.cancel()
			time.Sleep(100 * time.Millisecond) // 进入关停之后才发请求
			if _, err := io.WriteString(conn, "GET /v1/widgets/1 HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
				t.Fatal(err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatalf("已 accept 的连接没有得到响应：%v", err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Errorf("status = %d, want 204", resp.StatusCode)
			}
			if err := <-s.done; err != nil {
				t.Errorf("Start() = %v", err)
			}
		})
	}
}

// 没有排空期（容器部署的默认）时，关停期间已有 keep-alive 连接上的请求照样处理，但响应带 Connection: close，连接随即
// 关闭、不再被复用；正在处理的请求做完后 Start 返回。
func TestComponent_NoDrainStillClosesKeepAliveConnections(t *testing.T) {
	e := newTestServer(t, discardLogger(), false)
	release := make(chan struct{})
	e.GET("/slow", func(c *echo.Context) error {
		<-release
		return c.NoContent(http.StatusOK)
	})
	ln, addr := listen(t)
	s := startOn(t, e, ln, addr, httpserver.Stop{})
	idle := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 1}}
	t.Cleanup(idle.CloseIdleConnections)
	getVia(t, idle, "http://"+addr+"/v1/widgets/1") // 留下一条空闲的 keep-alive 连接

	slowDone := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/slow") // 另一条连接上的请求一直在处理，让关停停在等连接这一步
		if err == nil {
			_ = resp.Body.Close()
		}
		slowDone <- err
	}()
	time.Sleep(100 * time.Millisecond)
	s.cancel()
	time.Sleep(100 * time.Millisecond)

	resp, reused := getVia(t, idle, "http://"+addr+"/v1/widgets/1")
	if !reused || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("关停期间空闲连接上的请求应照常处理：reused = %v, status = %d", reused, resp.StatusCode)
	}
	if !resp.Close {
		t.Error("关停期间的响应应带 Connection: close")
	}
	close(release)
	if err := <-slowDone; err != nil {
		t.Errorf("正在处理的请求应做完：%v", err)
	}
	select {
	case err := <-s.done:
		if err != nil {
			t.Errorf("Start() = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("请求做完后没有停止")
	}
}

// 共享 listener（socket activation）收到停止信号立即不再 accept：新连接留在队列里等其它实例接；已建立的连接照常处理、
// 响应带 Connection: close；连接都关闭后不必等满排空期。
func TestComponent_SharedListenerStopsAcceptingImmediately(t *testing.T) {
	ln, addr := listen(t)
	s := startOn(t, newTestServer(t, discardLogger(), false), ln, addr, httpserver.Stop{Drain: 5 * time.Second, SharedListener: true})
	keep := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 1}}
	getVia(t, keep, "http://"+addr+"/v1/widgets/1") // 一条已建立的 keep-alive 连接，排空期要服务它
	start := time.Now()
	s.cancel()
	time.Sleep(100 * time.Millisecond)

	fresh := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 300 * time.Millisecond}
	if resp, err := fresh.Get("http://" + addr + "/v1/widgets/1"); err == nil {
		_ = resp.Body.Close()
		t.Fatal("停止后新连接不应再被这个进程 accept（应留在队列里等接班的实例）")
	}
	resp, reused := getVia(t, keep, "http://"+addr+"/v1/widgets/1")
	if !reused || resp.StatusCode != http.StatusNoContent || !resp.Close {
		t.Errorf("已建立的连接应照常处理并带 Connection: close：reused = %v, status = %d, close = %v", reused, resp.StatusCode, resp.Close)
	}
	keep.CloseIdleConnections()
	select {
	case err := <-s.done:
		if err != nil {
			t.Errorf("Start() = %v", err)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("连接都关闭后应提前结束排空，用了 %s", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("没有停止")
	}
}

// socket activation 交接：两个组件服务同一个监听 socket（fd 各自 dup 一份，相当于两个进程从 systemd 继承），
// 持续有新连接时停掉其中一个，另一个接走全部连接，客户端没有任何错误。
func TestComponent_SharedSocketHandoffLosesNothing(t *testing.T) {
	base, addr := listen(t)
	f, err := base.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	dup, err := net.FileListener(f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	start := func(ln net.Listener) (context.CancelFunc, chan error) {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			done <- httpserver.Component("api", newTestServer(t, discardLogger(), false), ln, httpserver.Stop{SharedListener: true}).Start(ctx)
		}()
		return cancel, done
	}
	stopOld, oldDone := start(base)
	stopNew, newDone := start(dup)
	t.Cleanup(func() {
		stopNew()
		if err := <-newDone; err != nil {
			t.Errorf("新组件 Start() = %v", err)
		}
	})
	waitUp(t, "http://"+addr)

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 5 * time.Second}
	var mu sync.Mutex
	var ok, failed int
	var firstErr error
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				resp, err := client.Get("http://" + addr + "/v1/widgets/1")
				mu.Lock()
				if err != nil {
					failed++
					if firstErr == nil {
						firstErr = err
					}
				} else {
					ok++
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
				mu.Unlock()
			}
		})
	}
	time.Sleep(200 * time.Millisecond)
	stopOld()
	if err := <-oldDone; err != nil {
		t.Errorf("旧组件 Start() = %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
	if failed > 0 || ok == 0 {
		t.Errorf("交接期间 ok=%d failed=%d，第一个错误：%v", ok, failed, firstErr)
	}
}

// slowAcceptListener 关闭后 Accept 要等 release 才返回，模拟关闭 listener 之后 Serve 迟迟不返回。
type slowAcceptListener struct {
	net.Listener
	release chan struct{}
}

func (l slowAcceptListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.release
	}
	return c, err
}

// Serve 超过 serveReturnTimeout 才返回时，Shutdown 会再关一次已经关过的 listener，这不是关停失败。
func TestComponent_SlowServeReturnIsNotShutdownError(t *testing.T) {
	ln, addr := listen(t)
	release := make(chan struct{})
	var logs syncBuffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	s := startOn(t, newTestServer(t, log, false), slowAcceptListener{ln, release}, addr, httpserver.Stop{})
	s.cancel()
	time.AfterFunc(1500*time.Millisecond, func() { close(release) })
	select {
	case err := <-s.done:
		if err != nil {
			t.Errorf("Start() = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("没有停止")
	}
	if strings.Contains(logs.String(), "graceful shutdown error") {
		t.Errorf("Serve 返回慢不应记成关停失败：\n%s", logs.String())
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
