package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v5"

	"app.local/internal/modules/user/domain"
	"app.local/internal/modules/user/httpapi"
	"app.local/internal/platform/crypto"
	"app.local/internal/platform/httpserver"
)

// authenticator 是 SessionAuthenticator 的内存实现；err 非空时 Authenticate 总是返回它。
type authenticator struct {
	sessions map[string]*domain.Session
	err      error
}

func (a authenticator) Authenticate(_ context.Context, sid string) (*domain.Session, error) {
	if a.err != nil {
		return nil, a.err
	}
	if s, ok := a.sessions[sid]; ok {
		return s, nil
	}
	return nil, domain.ErrInvalidToken
}

func sessionMw(a authenticator, kid, key string, additional map[string]string) echo.MiddlewareFunc {
	return httpapi.NewSessionMiddleware(httpapi.SessionAuthConfig{
		Sessions: a, KeyID: kid, Key: []byte(key), AdditionalKeys: additional, ReauthWindow: 10 * time.Minute,
	})
}

func TestSessionMiddleware_SetsUserSession(t *testing.T) {
	authTime := time.Now().Add(-time.Minute)
	sess := &domain.Session{ID: "sid-1", UserID: "user-1", AuthTime: authTime, AMR: []string{"wechat"}}
	authMw := sessionMw(authenticator{sessions: map[string]*domain.Session{"sid-1": sess}}, "kid", "secret", nil)

	e := echo.New()
	var got httpserver.UserSession
	e.GET("/protected", func(c *echo.Context) error {
		got, _ = httpserver.UserSessionFrom(c)
		return c.NoContent(http.StatusOK)
	}, authMw)

	issuer := crypto.NewTokenIssuer("secret", "kid")
	token, err := issuer.Issue("user-1", "sid-1", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if got.UserID != "user-1" || got.SessionID != "sid-1" || !got.AuthTime.Equal(authTime) || len(got.AMR) != 1 || got.AMR[0] != "wechat" ||
		!got.ReauthUntil.Equal(authTime.Add(10*time.Minute)) {
		t.Errorf("UserSession = %+v", got)
	}
}

func TestSessionMiddleware_Rejects(t *testing.T) {
	issuer := crypto.NewTokenIssuer("secret", "kid")
	valid, err := issuer.Issue("user-1", "sid-1", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	otherIssuer := jwt.NewWithClaims(jwt.SigningMethodHS256, crypto.AccessClaims{
		SID: "sid-1",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "someone-else",
			Subject:   "user-1",
			Audience:  jwt.ClaimStrings{crypto.Audience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	})
	otherIssuer.Header["kid"] = "kid"
	wrongIss, err := otherIssuer.SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	noSID, err := issuer.Issue("user-1", "", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	dbDown := errors.New("connection refused")
	tests := []struct {
		name     string
		authz    string
		sessions map[string]*domain.Session
		authErr  error
		want     error
	}{
		{"没有 Authorization 头", "", map[string]*domain.Session{"sid-1": {}}, nil, domain.ErrInvalidToken},
		{"不是 Bearer", "Basic abc", map[string]*domain.Session{"sid-1": {}}, nil, domain.ErrInvalidToken},
		{"签名不对", "Bearer " + valid + "x", map[string]*domain.Session{"sid-1": {}}, nil, domain.ErrInvalidToken},
		{"iss 不对", "Bearer " + wrongIss, map[string]*domain.Session{"sid-1": {}}, nil, domain.ErrInvalidToken},
		{"token 里没有 sid", "Bearer " + noSID, map[string]*domain.Session{"sid-1": {}}, nil, domain.ErrInvalidToken},
		{"会话不存在", "Bearer " + valid, map[string]*domain.Session{}, nil, domain.ErrInvalidToken},
		{"会话被新登录顶掉", "Bearer " + valid, nil, domain.ErrSessionReplaced, domain.ErrSessionReplaced},
		// 查库失败也拒绝，但不翻译成 401：渲染成 500，客户端不会因为服务端故障清掉令牌。
		{"查库失败", "Bearer " + valid, nil, dbDown, dbDown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authMw := sessionMw(authenticator{sessions: tt.sessions, err: tt.authErr}, "kid", "secret", nil)
			called := false
			handler := authMw(func(c *echo.Context) error {
				called = true
				return c.NoContent(http.StatusOK)
			})
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			if tt.authz != "" {
				req.Header.Set(echo.HeaderAuthorization, tt.authz)
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			err := handler(c)

			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if called {
				t.Error("被拒绝时不应该放行到 handler")
			}
		})
	}
}

func TestSessionMiddleware_KeyRotation_OldKeyStillVerifiable(t *testing.T) {
	oldIssuer := crypto.NewTokenIssuer("old-secret", "kid-old")
	oldToken, err := oldIssuer.Issue("user-1", "sid-1", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	authMw := sessionMw(authenticator{sessions: map[string]*domain.Session{"sid-1": {ID: "sid-1", UserID: "user-1"}}}, "kid-new", "new-secret", map[string]string{"kid-old": "old-secret"})

	e := echo.New()
	var gotUserID string
	e.GET("/protected", func(c *echo.Context) error {
		s, _ := httpserver.UserSessionFrom(c)
		gotUserID = s.UserID
		return c.NoContent(http.StatusOK)
	}, authMw)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set(echo.HeaderAuthorization, "Bearer "+oldToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || gotUserID != "user-1" {
		t.Errorf("status = %d userID = %q, want 200 + user-1", rec.Code, gotUserID)
	}
}

func handlerFor(called *bool) echo.HandlerFunc {
	return func(c *echo.Context) error {
		*called = true
		return c.NoContent(http.StatusOK)
	}
}

func TestRequireRecentAuth_Window(t *testing.T) {
	tests := []struct {
		name     string
		authTime time.Time
		wantCode int
	}{
		{"刚验证过，放行", time.Now(), http.StatusOK},
		{"超过窗口，拒绝", time.Now().Add(-11 * time.Minute), http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := &domain.Session{ID: "sid-1", UserID: "user-1", AuthTime: tt.authTime}
			authMw := sessionMw(authenticator{sessions: map[string]*domain.Session{"sid-1": sess}}, "kid", "secret", nil)
			recent := httpserver.NewRequireRecentAuth()

			e := echo.New()
			called := false

			issuer := crypto.NewTokenIssuer("secret", "kid")
			token, err := issuer.Issue("user-1", "sid-1", time.Now().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/sensitive", nil)
			req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			err = authMw(recent(handlerFor(&called)))(c)
			if tt.wantCode == http.StatusOK && err != nil {
				t.Errorf("error = %v, want nil", err)
			}
			if tt.wantCode == http.StatusForbidden && !errors.Is(err, domain.ErrReauthRequired) {
				t.Errorf("error = %v, want ErrReauthRequired", err)
			}
		})
	}
}
