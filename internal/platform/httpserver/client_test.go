package httpserver_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"app.local/internal/platform/httpserver"
	"app.local/internal/platform/logger"
)

func TestParseClientInfo_KeepsWellFormedValues(t *testing.T) {
	h := http.Header{}
	h.Set(httpserver.HeaderClientPlatform, "wechat-miniprogram")
	h.Set(httpserver.HeaderClientOS, "ohos")
	h.Set(httpserver.HeaderClientVersion, "1.4.2+1042")
	h.Set(httpserver.HeaderInstallationID, "0193a6f2-7c1e-7d4a-9b3e-2f6c8d1a5e70")
	got := httpserver.ParseClientInfo(h)
	want := httpserver.ClientInfo{
		Platform: "wechat-miniprogram", OS: "ohos", Version: "1.4.2+1042",
		InstallationID: "0193a6f2-7c1e-7d4a-9b3e-2f6c8d1a5e70",
	}
	if got != want {
		t.Errorf("ParseClientInfo = %+v, want %+v", got, want)
	}
}

func TestClientInfo_SanitizedDropsMalformedValues(t *testing.T) {
	for _, in := range []httpserver.ClientInfo{
		{Platform: "Android"},                                             // 大写
		{Platform: strings.Repeat("a", 33)},                               // 超长
		{Platform: "android\nfake=1"},                                     // 控制字符
		{OS: "ios 18"},                                                    // 空格
		{Version: "1.0;drop"},                                             // 非法字符
		{Version: strings.Repeat("1", 33)},                                // 超长
		{InstallationID: "not-a-uuid"},                                    // 不是 UUID
		{InstallationID: "{0193a6f2-7c1e-7d4a-9b3e-2f6c8d1a5e70}"},        // 花括号
		{InstallationID: "urn:uuid:0193a6f2-7c1e-7d4a-9b3e-2f6c8d1a5e70"}, // urn 前缀
		{InstallationID: "0193a6f27c1e7d4a9b3e2f6c8d1a5e70"},              // 不带连字符
	} {
		if got := in.Sanitized(); got != (httpserver.ClientInfo{}) {
			t.Errorf("Sanitized(%+v) = %+v, want 全部置空", in, got)
		}
	}
}

// 不合法的值置空，请求照常处理。
// iOS 的 UUID().uuidString 是大写：照收，统一转成小写。
func TestClientInfo_SanitizedLowercasesInstallationID(t *testing.T) {
	got := httpserver.ClientInfo{InstallationID: "0193A6F2-7C1E-7D4A-9B3E-2F6C8D1A5E70"}.Sanitized()
	if got.InstallationID != "0193a6f2-7c1e-7d4a-9b3e-2f6c8d1a5e70" {
		t.Errorf("InstallationID = %q, want 小写形式", got.InstallationID)
	}
}

func TestServer_ClientHeadersGoToLogsNotSpan(t *testing.T) {
	var buf bytes.Buffer
	s := newTraceServer(t, false)
	e := newTestServer(t, logger.New("info", &buf), false)

	header := map[string]string{
		httpserver.HeaderClientPlatform: "android",
		httpserver.HeaderClientVersion:  "1.4.2+1042",
		httpserver.HeaderClientOS:       "BAD VALUE",
		httpserver.HeaderInstallationID: "0193a6f2-7c1e-7d4a-9b3e-2f6c8d1a5e70",
	}
	if rec := serve(e, "/v1/no/such/path", header); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404（请求头不合法不影响处理）", rec.Code)
	}
	recs := parseLogLines(t, &buf)
	if len(recs) == 0 {
		t.Fatal("404 应记一条错误日志")
	}
	rec := recs[len(recs)-1]
	for key, want := range map[string]any{
		"client_platform": "android", "client_version": "1.4.2+1042",
		"installation_id": "0193a6f2-7c1e-7d4a-9b3e-2f6c8d1a5e70",
	} {
		if rec[key] != want {
			t.Errorf("日志 %s = %v, want %v", key, rec[key], want)
		}
	}
	if _, ok := rec["client_os"]; ok {
		t.Errorf("不合法的 X-Client-OS 不应进日志：%v", rec)
	}

	serve(s.e, "/v1/widgets/1", header)
	spans := s.spans.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	// 客户端可任意取值，只进日志，不进 span 属性（经 trace_id 关联）。
	for _, a := range spans[0].Attributes() {
		switch k := string(a.Key); {
		case strings.Contains(k, "client"), strings.Contains(k, "installation"):
			t.Errorf("span 不应有客户端请求头的属性：%s=%s", k, a.Value.String())
		}
	}
}

// 没带这些请求头时日志里没有对应字段（后台、旧客户端）。
func TestServer_NoClientHeadersNoFields(t *testing.T) {
	var buf bytes.Buffer
	e := newTestServer(t, logger.New("info", &buf), false)
	serve(e, "/v1/no/such/path", nil)
	for _, rec := range parseLogLines(t, &buf) {
		for _, key := range []string{"client_platform", "client_os", "client_version", "installation_id"} {
			if _, ok := rec[key]; ok {
				t.Errorf("没带请求头时不应有 %s：%v", key, rec)
			}
		}
	}
}

// 跨域的前端要能发这些请求头：预检放行。
func TestServer_CORSAllowsClientHeaders(t *testing.T) {
	s := newTraceServer(t, false)
	req := httptest.NewRequest(http.MethodOptions, "/v1/widgets/1", nil)
	req.Header.Set(echo.HeaderOrigin, "https://example.com")
	req.Header.Set(echo.HeaderAccessControlRequestMethod, http.MethodGet)
	req.Header.Set(echo.HeaderAccessControlRequestHeaders, "x-client-platform,x-client-os,x-client-version,x-installation-id")
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if got := rec.Header().Get(echo.HeaderAccessControlMaxAge); got != "600" {
		t.Errorf("Access-Control-Max-Age = %q, want 600（每个请求都带自定义头，预检结果要缓存）", got)
	}
	got := strings.ToLower(rec.Header().Get(echo.HeaderAccessControlAllowHeaders))
	for _, h := range []string{"x-client-platform", "x-client-os", "x-client-version", "x-installation-id"} {
		if !strings.Contains(got, h) {
			t.Errorf("Access-Control-Allow-Headers = %q, want 含 %s", got, h)
		}
	}
}
