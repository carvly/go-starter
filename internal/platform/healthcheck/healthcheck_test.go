package healthcheck_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"app.local/internal/platform/healthcheck"
)

func TestRun_Healthy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	addr := ":" + serverPort(t, srv)
	if code := healthcheck.Run(addr); code != 0 {
		t.Errorf("Run(%q) = %d, want 0", addr, code)
	}
}

func TestRun_UnhealthyStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	addr := ":" + serverPort(t, srv)
	if code := healthcheck.Run(addr); code != 1 {
		t.Errorf("Run(%q) = %d, want 1（非 200 应该视为不健康）", addr, code)
	}
}

func TestRun_ConnectionRefused(t *testing.T) {
	// 故意用一个没有监听任何服务的端口，模拟进程还没起来/已经崩溃。
	if code := healthcheck.Run(":1"); code != 1 {
		t.Errorf("Run(:1) = %d, want 1（连不上应该视为不健康）", code)
	}
}

func TestRun_InvalidAddr(t *testing.T) {
	if code := healthcheck.Run("not-a-valid-addr"); code != 1 {
		t.Errorf("Run(%q) = %d, want 1", "not-a-valid-addr", code)
	}
}

func serverPort(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("解析测试服务器地址失败: %v", err)
	}
	return port
}
