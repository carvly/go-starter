//go:build integration

// Package gatewaytest 在真实的 OpenResty 里验证网关鉴权（docs/agents/auth.md 的"网关鉴权"）：配置由 render.sh 渲染，
// gateway_auth.lua、全机一份的 http.conf 和 snippets/proxy.conf 原样装进容器；后端是同一个容器里的 stub（真实的
// httpserver 和网关身份中间件，校验端点按令牌返回约定里的每一种结果）。
package gatewaytest

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"app.local/internal/platform/crypto"
)

const (
	image  = "openresty/openresty:1.27.1.2-alpine"
	domain = "api.test"
	name   = "gw"
)

type gateway struct {
	t      *testing.T
	ctr    testcontainers.Container
	base   string
	client *http.Client
}

func startGateway(t *testing.T) *gateway {
	t.Helper()
	ctx := t.Context()
	root := repoRoot(t)
	dir := t.TempDir()

	stub := filepath.Join(dir, "stub")
	build := exec.Command("go", "build", "-o", stub, "./deploy/baremetal/gatewaytest/stub")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build stub: %v\n%s", err, out)
	}
	rendered, plain := filepath.Join(dir, "rendered"), filepath.Join(dir, "plain")
	renderConf(t, root, rendered, "--internal-port", "8082")
	renderConf(t, root, plain) // 不开网关鉴权的渲染结果，只做 openresty -t
	selfSigned(t, dir)
	priv, pub, err := crypto.NewAssertionKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	// 挂载目录、由入口脚本放到位：socktainer 下 testcontainers 的 Files 复制不可靠。
	for src, dst := range map[string]string{
		filepath.Join(root, "deploy/baremetal/openresty/http.conf"):            "http.conf",
		filepath.Join(root, "deploy/baremetal/openresty/snippets/proxy.conf"):  "proxy.conf",
		filepath.Join(root, "deploy/baremetal/openresty/lua/gateway_auth.lua"): "gateway_auth.lua",
		filepath.Join(rendered, "openresty", name+".conf"):                     "project.conf",
		filepath.Join(plain, "openresty", name+".conf"):                        "plain.conf",
	} {
		copyFile(t, src, filepath.Join(dir, dst))
	}
	install := strings.Join([]string{
		"rm -f /etc/nginx/conf.d/default.conf",
		"mkdir -p /var/log/openresty /etc/openresty/certs /etc/openresty/lua /usr/local/openresty/nginx/conf/snippets",
		"cp /gw/http.conf /etc/nginx/conf.d/00-http.conf",
		"cp /gw/project.conf /etc/nginx/conf.d/" + name + ".conf",
		"cp /gw/proxy.conf /usr/local/openresty/nginx/conf/snippets/proxy.conf",
		"cp /gw/gateway_auth.lua /etc/openresty/lua/gateway_auth.lua",
		"cp /gw/tls.crt /etc/openresty/certs/" + domain + ".crt",
		"cp /gw/tls.key /etc/openresty/certs/" + domain + ".key",
		"(/gw/stub &)",
		"exec openresty -g 'daemon off;'",
	}, " && ")
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        image,
			ExposedPorts: []string{"443/tcp"},
			HostConfigModifier: func(hc *container.HostConfig) {
				hc.Binds = append(hc.Binds, dir+":/gw:ro")
			},
			Env:        map[string]string{"STUB_SIGNING_KEY": priv, "STUB_PUBLIC_KEY": pub},
			Entrypoint: []string{"sh", "-c", install},
			WaitingFor: wait.ForAll(wait.ForLog("stub ready"), wait.ForListeningPort("443/tcp")).WithDeadline(time.Minute),
		},
		Started: true,
	})
	if ctr != nil {
		t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })
	}
	if err != nil {
		if ctr != nil {
			if logs, lerr := ctr.Logs(context.Background()); lerr == nil {
				out, _ := io.ReadAll(logs)
				t.Logf("container logs:\n%s", out)
			}
		}
		t.Fatalf("start openresty: %v", err)
	}
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := ctr.MappedPort(ctx, "443/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return &gateway{
		t:    t,
		ctr:  ctr,
		base: "https://" + net.JoinHostPort(host, port.Port()),
		client: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{ServerName: domain, InsecureSkipVerify: true},
		}},
	}
}

func (g *gateway) do(method, path string, header map[string]string, body io.Reader) (*http.Response, []byte) {
	g.t.Helper()
	req, err := http.NewRequestWithContext(g.t.Context(), method, g.base+path, body)
	if err != nil {
		g.t.Fatal(err)
	}
	req.Host = domain
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		g.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

type verifyCall struct {
	Header http.Header `json:"header"`
	At     time.Time   `json:"at"`
}

func (g *gateway) verifyCalls() []verifyCall {
	g.t.Helper()
	_, body := g.do(http.MethodGet, "/v1/debug/verify-calls", nil, nil)
	var calls []verifyCall
	if err := json.Unmarshal(body, &calls); err != nil {
		g.t.Fatalf("verify-calls: %v %s", err, body)
	}
	return calls
}

func (g *gateway) accessLog() string {
	g.t.Helper()
	_, r, err := g.ctr.Exec(g.t.Context(), []string{"cat", "/var/log/openresty/" + name + "_access.log"})
	if err != nil {
		g.t.Fatal(err)
	}
	data, _ := io.ReadAll(r)
	return string(data)
}

func hasCode(resp *http.Response, body []byte, status int, code string) bool {
	return resp.StatusCode == status && bytes.Contains(body, []byte(`"code":"`+code+`"`))
}

func TestGateway(t *testing.T) {
	g := startGateway(t)

	t.Run("匿名请求不发子请求，客户端自带的身份头被清掉", func(t *testing.T) {
		before := len(g.verifyCalls())
		resp, body := g.do(http.MethodGet, "/v1/public", map[string]string{
			"X-Auth-Assertion": "forged", "X-Auth-Status": "session_replaced",
		}, nil)
		var seen map[string]string
		_ = json.Unmarshal(body, &seen)
		if resp.StatusCode != http.StatusOK || seen["assertion"] != "" || seen["status"] != "" {
			t.Errorf("= %d %s, want 200 且后端看不到客户端带来的 X-Auth-*", resp.StatusCode, body)
		}
		if after := len(g.verifyCalls()); after != before {
			t.Errorf("匿名请求发了 %d 次子请求", after-before)
		}
	})

	t.Run("有效令牌：后端拿到断言里的身份，校验和业务同一个 trace_id", func(t *testing.T) {
		resp, body := g.do(http.MethodGet, "/v1/whoami", map[string]string{
			"Authorization": "Bearer valid", "Cookie": "refresh=secret", "Origin": "https://app.test", "X-Client-Platform": "ios",
		}, nil)
		if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"user_id":"u-1"`)) {
			t.Fatalf("= %d %s", resp.StatusCode, body)
		}
		calls := g.verifyCalls()
		last := calls[len(calls)-1].Header
		for _, h := range []string{"Cookie", "Origin", "X-Client-Platform"} {
			if last.Get(h) != "" {
				t.Errorf("子请求不应转发 %s：%v", h, last)
			}
		}
		if last.Get("Authorization") != "Bearer valid" || last.Get("Content-Length") != "" {
			t.Errorf("子请求的请求头 = %v", last)
		}
		traceID := resp.Header.Get("X-Trace-Id")
		if traceID == "" || last.Get("X-Trace-Id") != traceID {
			t.Errorf("子请求 X-Trace-Id = %q，业务请求 = %q，want 相同", last.Get("X-Trace-Id"), traceID)
		}
		if !strings.Contains(g.accessLog(), `"auth":"ok"`) {
			t.Errorf("访问日志应记 auth=ok：%s", g.accessLog())
		}
	})

	t.Run("失效原因原样转给后端：401 invalid_token、session_replaced", func(t *testing.T) {
		for token, code := range map[string]string{"invalid": "invalid_token", "replaced": "session_replaced"} {
			resp, body := g.do(http.MethodGet, "/v1/whoami", map[string]string{"Authorization": "Bearer " + token}, nil)
			if !hasCode(resp, body, http.StatusUnauthorized, code) {
				t.Errorf("%s = %d %s, want 401 %s", token, resp.StatusCode, body, code)
			}
		}
		// 公开接口带失效令牌照常处理。
		if resp, body := g.do(http.MethodGet, "/v1/public", map[string]string{"Authorization": "Bearer invalid"}, nil); resp.StatusCode != http.StatusOK {
			t.Errorf("公开接口带失效令牌 = %d %s", resp.StatusCode, body)
		}
	})

	t.Run("校验不可用：503 auth_unavailable，不是 401", func(t *testing.T) {
		for _, token := range []string{"error", "down"} {
			resp, body := g.do(http.MethodGet, "/v1/whoami", map[string]string{"Authorization": "Bearer " + token}, nil)
			if !hasCode(resp, body, http.StatusServiceUnavailable, "auth_unavailable") || resp.Header.Get("X-Trace-Id") == "" {
				t.Errorf("%s = %d %v %s, want 503 auth_unavailable + X-Trace-Id", token, resp.StatusCode, resp.Header, body)
			}
		}
		if !strings.Contains(g.accessLog(), `"auth":"unavailable"`) {
			t.Error("访问日志应记 auth=unavailable")
		}
	})

	t.Run("校验端点限流：429 原样回给客户端", func(t *testing.T) {
		resp, body := g.do(http.MethodGet, "/v1/whoami", map[string]string{"Authorization": "Bearer limited"}, nil)
		if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "1" ||
			!bytes.Contains(body, []byte("rate limit exceeded")) {
			t.Errorf("= %d %v %s, want 429 + Retry-After + 校验端点的响应体", resp.StatusCode, resp.Header, body)
		}
	})

	t.Run("不开网关鉴权的渲染结果也能通过 openresty -t", func(t *testing.T) {
		code, out, err := g.ctr.Exec(t.Context(), []string{"sh", "-c",
			"cp /gw/plain.conf /etc/nginx/conf.d/" + name + ".conf && openresty -t; rc=$?; " +
				"cp /gw/project.conf /etc/nginx/conf.d/" + name + ".conf; exit $rc"})
		if err != nil {
			t.Fatal(err)
		}
		if code != 0 {
			msg, _ := io.ReadAll(out)
			t.Errorf("openresty -t = %d\n%s", code, msg)
		}
	})

	t.Run("带请求体的请求：先读完请求体再换断言，请求体完整到达后端", func(t *testing.T) {
		const size = 1 << 20
		pr, pw := io.Pipe()
		sent := make(chan time.Time, 1)
		go func() {
			_, _ = pw.Write(make([]byte, size/2))
			time.Sleep(2 * time.Second)
			_, _ = pw.Write(make([]byte, size/2))
			sent <- time.Now()
			_ = pw.Close()
		}()
		start := len(g.verifyCalls())
		resp, body := g.do(http.MethodPost, "/v1/upload", map[string]string{"Authorization": "Bearer valid", "Content-Type": "application/octet-stream"}, pr)
		if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"bytes":1048576`)) {
			t.Fatalf("= %d %s", resp.StatusCode, body)
		}
		finished := <-sent
		calls := g.verifyCalls()[start:]
		if len(calls) != 1 || calls[0].At.Before(finished.Add(-500*time.Millisecond)) {
			t.Errorf("子请求在 %v，请求体在 %v 发完：断言要在请求体读完之后才签发", calls, finished)
		}
	})
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "..", "..", "..")
}

// selfSigned 在 dir 下写 tls.crt、tls.key（域名 api.test 的自签证书）。
func selfSigned(t *testing.T, dir string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: domain}, DNSNames: []string{domain},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	crtPath, keyPath := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	if err := os.WriteFile(crtPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// renderConf 用 render.sh 渲染一份配置到 out（项目名、端口、域名与容器里的布局一致），extra 是额外参数。
func renderConf(t *testing.T, root, out string, extra ...string) {
	t.Helper()
	args := append([]string{"--name", name, "--api-port", "8080", "--worker-port", "9090", "--api-domain", domain, "--out", out}, extra...)
	cmd := exec.Command("deploy/baremetal/render.sh", args...)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("render.sh: %v\n%s", err, output)
	}
}
