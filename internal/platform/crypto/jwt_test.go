package crypto_test

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"app.local/internal/platform/crypto"
)

func TestTokenIssuer_IssueAndVerify(t *testing.T) {
	issuer := crypto.NewTokenIssuer("current-secret", "kid-1")

	tokenStr, err := issuer.Issue("user-123", "sid-123", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	keyFunc := crypto.KeyFunc("kid-1", []byte("current-secret"), nil)
	claims := &crypto.AccessClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, keyFunc)
	if err != nil {
		t.Fatalf("ParseWithClaims() error = %v", err)
	}
	if !token.Valid {
		t.Fatal("token 应该是有效的")
	}
	if claims.Subject != "user-123" || claims.SID != "sid-123" {
		t.Errorf("Subject/SID = %q/%q, want user-123/sid-123", claims.Subject, claims.SID)
	}
	if claims.Issuer != crypto.Issuer || len(claims.Audience) != 1 || claims.Audience[0] != crypto.Audience {
		t.Errorf("iss/aud = %q/%q, want %q/%q", claims.Issuer, claims.Audience, crypto.Issuer, crypto.Audience)
	}
}

func TestTokenIssuer_KeyRotation_OldKeyStillVerifiable(t *testing.T) {
	// 模拟密钥轮换：旧 token 用旧密钥（kid-old）签发，轮换后当前密钥
	// 换成了 kid-new，但 KeyFunc 的 additionalKeys 里还留着 kid-old，
	// 旧 token 在过期之前应该仍然能被校验通过。
	oldIssuer := crypto.NewTokenIssuer("old-secret", "kid-old")
	oldToken, err := oldIssuer.Issue("user-456", "sid-456", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	keyFunc := crypto.KeyFunc("kid-new", []byte("new-secret"), map[string]string{
		"kid-old": "old-secret",
	})
	claims := &crypto.AccessClaims{}
	if _, err := jwt.ParseWithClaims(oldToken, claims, keyFunc); err != nil {
		t.Fatalf("ParseWithClaims() error = %v, 旧 kid 签发的 token 应该仍可校验", err)
	}
}

func TestTokenIssuer_UnknownKeyID_Rejected(t *testing.T) {
	issuer := crypto.NewTokenIssuer("secret", "kid-unknown-to-verifier")
	tokenStr, err := issuer.Issue("u", "sid", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	keyFunc := crypto.KeyFunc("kid-current", []byte("current-secret"), nil)
	_, err = jwt.ParseWithClaims(tokenStr, &crypto.AccessClaims{}, keyFunc)
	if err == nil {
		t.Fatal("ParseWithClaims() error = nil, want error（未知 kid 应该被拒绝）")
	}
}

func TestTokenIssuer_ExpiredToken_Rejected(t *testing.T) {
	issuer := crypto.NewTokenIssuer("secret", "kid-1")
	tokenStr, err := issuer.Issue("u", "sid", time.Now().Add(-time.Minute)) // 已过期
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	keyFunc := crypto.KeyFunc("kid-1", []byte("secret"), nil)
	_, err = jwt.ParseWithClaims(tokenStr, &crypto.AccessClaims{}, keyFunc)
	if err == nil {
		t.Fatal("ParseWithClaims() error = nil, want error（过期 token 应该被拒绝）")
	}
}
