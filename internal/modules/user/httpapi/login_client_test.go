package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"app.local/internal/modules/user/domain"
	"app.local/internal/modules/user/domain/domaintest"
	"app.local/internal/platform/httpserver"
	"app.local/internal/platform/oauth"
)

func onlySession(t *testing.T, fakes *domaintest.Fakes) *domain.Session {
	t.Helper()
	all := fakes.Sessions.ListAll()
	if len(all) != 1 {
		t.Fatalf("会话数 = %d, want 1", len(all))
	}
	return all[0]
}

func hasCode(rec *httptest.ResponseRecorder, status int, code string) bool {
	return rec.Code == status && strings.Contains(rec.Body.String(), `"code":"`+code+`"`)
}

// App 入口的客户端信息在校验凭证之前检查：不合法时授权码和 nonce 都还没用掉，客户端修正后可以直接重试。
func TestNativeLogin_AppEntryChecksClientBeforeVerifying(t *testing.T) {
	app := &stubNative{caps: oauth.NativeCapabilities{Nonce: true}}
	e, _, fakes, _ := newFlowServerWith(t, oauth.Registry{"oidc-app": {Native: app}})
	_, nonce := issueNonce(t, e, "oidc-app")
	body := map[string]string{"id_token": "T1", "nonce": nonce, "device_class": "phone"}

	for _, platform := range []string{"", "web", "wechat-miniprogram", "Android", "android;x"} {
		rec := nativeLoginWith(t, e, "oidc-app", body, map[string]string{httpserver.HeaderClientPlatform: platform})
		if !hasCode(rec, http.StatusBadRequest, "client_platform_invalid") || !strings.Contains(rec.Body.String(), "X-Client-Platform") {
			t.Errorf("X-Client-Platform=%q: %d %s, want 400 client_platform_invalid，文案写明请求头", platform, rec.Code, rec.Body.String())
		}
	}
	noClass := map[string]string{"id_token": "T1", "nonce": nonce}
	if rec := nativeLogin(t, e, "oidc-app", noClass); !hasCode(rec, http.StatusBadRequest, "device_class_invalid") ||
		!strings.Contains(rec.Body.String(), "device_class") {
		t.Errorf("缺 device_class: %d %s, want 400 device_class_invalid，文案写明字段", rec.Code, rec.Body.String())
	}
	if app.cred != (oauth.NativeCredential{}) {
		t.Fatalf("客户端信息不合法时不应校验凭证，provider 收到了 %+v", app.cred)
	}

	// 同一个 nonce 修正后仍能登录。
	rec := nativeLoginWith(t, e, "oidc-app", map[string]string{"id_token": "T1", "nonce": nonce, "device_class": "tablet", "device_name": "iPad"},
		map[string]string{
			httpserver.HeaderClientPlatform: "ios",
			httpserver.HeaderClientVersion:  "1.4.2+1042",
			httpserver.HeaderClientOS:       "ios",
		})
	if rec.Code != http.StatusOK {
		t.Fatalf("修正后登录 = %d %s", rec.Code, rec.Body.String())
	}
	sess := onlySession(t, fakes)
	if sess.Client != domain.ClientIOS || sess.DeviceClass != domain.DeviceClassTablet || sess.ClientVersion != "1.4.2+1042" ||
		sess.DeviceName != "iPad" || sess.ClientOS != "" {
		t.Errorf("会话 = %+v, want ios/tablet/1.4.2+1042/iPad，App 不记 client_os", sess)
	}
	users := fakes.Users.UsersList()
	if len(users) != 1 || users[0].SignupClient != domain.ClientIOS {
		t.Errorf("signup_client = %+v, want ios", users)
	}
}

// 小程序入口的客户端由入口决定，请求头写什么都不影响；设备形态不适用，忽略。
func TestNativeLogin_MiniprogramEntryIgnoresPlatformHeader(t *testing.T) {
	e, _, fakes, _ := newFlowServerWith(t, oauth.Registry{"mini-app": {Native: &stubNative{}}})
	rec := nativeLoginWith(t, e, "mini-app", map[string]string{"id_token": "T1", "device_class": "tablet"}, map[string]string{
		httpserver.HeaderClientPlatform: "android",
		httpserver.HeaderClientOS:       "ohos",
		httpserver.HeaderClientVersion:  "3.1.0",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("登录 = %d %s", rec.Code, rec.Body.String())
	}
	sess := onlySession(t, fakes)
	if sess.Client != domain.ClientWeChatMiniprogram || sess.ClientOS != "ohos" || sess.ClientVersion != "3.1.0" || sess.DeviceClass != "" {
		t.Errorf("会话 = %+v, want wechat-miniprogram/ohos/3.1.0，不记 device_class", sess)
	}

	// 什么请求头都不带也能登录。
	e2, _, fakes2, _ := newFlowServerWith(t, oauth.Registry{"mini-app": {Native: &stubNative{}}})
	if rec := nativeLoginWith(t, e2, "mini-app", map[string]string{"id_token": "T2"}, nil); rec.Code != http.StatusOK {
		t.Fatalf("不带请求头登录 = %d %s", rec.Code, rec.Body.String())
	}
	if sess := onlySession(t, fakes2); sess.Client != domain.ClientWeChatMiniprogram || sess.ClientVersion != "" {
		t.Errorf("会话 = %+v", sess)
	}
}

// 浏览器兑换只有浏览器能走完，客户端固定是 web，请求头只取版本。
func TestExchange_ClientIsAlwaysWeb(t *testing.T) {
	e, _, fakes, _ := newFlowServerWith(t, oauth.Registry{"plain": {Provider: &stubProvider{}}})
	state, binding := startLogin(t, e, "plain")
	rec := getCallback(t, e, "plain", "state="+state+"&code=C1", binding)
	code := locationOf(t, rec).Query().Get("code")

	body, _ := json.Marshal(map[string]string{"code": code})
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/oauth/exchange", strings.NewReader(string(body)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set(httpserver.HeaderClientPlatform, "ios")
	req.Header.Set(httpserver.HeaderClientVersion, "a1b2c3d")
	req.AddCookie(binding)
	if rec := doRequest(t, e, req); rec.Code != http.StatusOK {
		t.Fatalf("exchange = %d %s", rec.Code, rec.Body.String())
	}
	if sess := onlySession(t, fakes); sess.Client != domain.ClientWeb || sess.ClientVersion != "a1b2c3d" {
		t.Errorf("会话 = %+v, want web/a1b2c3d", sess)
	}
}

// 被同类设备顶掉的会话：带鉴权的接口返回 401 session_replaced，会话列表返回客户端信息。
func TestSessionReplaced_EndToEnd(t *testing.T) {
	e, svc, fakes, issuer := newFlowServerWith(t, oauth.Registry{"oidc-app": {Native: &stubNative{}}})
	svc.SetSessionPolicy(domain.NewSlotLimit(1))
	login := func() map[string]any {
		rec := nativeLoginWith(t, e, "oidc-app", map[string]string{"id_token": "T1", "device_class": "phone"}, map[string]string{
			httpserver.HeaderClientPlatform: "android", httpserver.HeaderClientVersion: "2.0.0",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("登录 = %d %s", rec.Code, rec.Body.String())
		}
		var tokens map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &tokens)
		return tokens
	}
	// 测试的 Service 用假签发器，access token 由真签发器按会话重签。
	listSessions := func(sess *domain.Session) *httptest.ResponseRecorder {
		access, err := issuer.Issue(sess.UserID, sess.ID, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/v1/me/sessions", nil)
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+access)
		return doRequest(t, e, req)
	}
	sessionByFamily := func(tokens map[string]any) *domain.Session {
		family, _, _ := strings.Cut(tokens["refresh_token"].(string), ".")
		v, err := fakes.Sessions.GetByFamily(t.Context(), family)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	first := login()
	second := login()
	if rec := listSessions(sessionByFamily(first)); !hasCode(rec, http.StatusUnauthorized, "session_replaced") {
		t.Errorf("被顶掉的会话 = %d %s, want 401 session_replaced", rec.Code, rec.Body.String())
	}
	rec := listSessions(sessionByFamily(second))
	if rec.Code != http.StatusOK {
		t.Fatalf("新会话 = %d %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0]["client"] != "android" || list.Items[0]["device_class"] != "phone" ||
		list.Items[0]["client_version"] != "2.0.0" {
		t.Errorf("会话列表 = %v", list.Items)
	}
	if _, ok := list.Items[0]["client_os"]; ok {
		t.Errorf("没有上报的 client_os 不应返回：%v", list.Items[0])
	}

	// refresh token 同样告诉持有者被顶掉了。
	body, _ := json.Marshal(map[string]string{"refresh_token": first["refresh_token"].(string)})
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/session/refresh", strings.NewReader(string(body)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if rec := doRequest(t, e, req); !hasCode(rec, http.StatusUnauthorized, "session_replaced") {
		t.Errorf("刷新被顶掉的会话 = %d %s, want 401 session_replaced", rec.Code, rec.Body.String())
	}
}
