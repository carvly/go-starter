package crypto_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"app.local/internal/platform/crypto"
)

func keyPair(t *testing.T, kid string) (*crypto.AssertionSigner, string) {
	t.Helper()
	priv, pub, err := crypto.NewAssertionKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	s, err := crypto.NewAssertionSigner(priv, kid)
	if err != nil {
		t.Fatal(err)
	}
	return s, pub
}

func TestAssertion_SignAndVerify(t *testing.T) {
	s, pub := keyPair(t, "k1")
	v, err := crypto.NewAssertionVerifier(map[string]string{"k1": pub})
	if err != nil {
		t.Fatal(err)
	}
	authTime := time.Now().Add(-time.Minute).Truncate(time.Second)
	want := crypto.Assertion{UserID: "u1", SessionID: "s1", AuthTime: authTime, AMR: []string{"wechat"},
		ReauthUntil: authTime.Add(10 * time.Minute)}
	token, err := s.Sign(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != want.UserID || got.SessionID != want.SessionID || !got.AuthTime.Equal(want.AuthTime) ||
		!got.ReauthUntil.Equal(want.ReauthUntil) || len(got.AMR) != 1 || got.AMR[0] != "wechat" {
		t.Errorf("Verify = %+v, want %+v", got, want)
	}
	if !v.Accepts(s) {
		t.Error("清单里有签发方的公钥时 Accepts 应为 true")
	}
}

// 伪造类的问题（签名不对、别的算法、别的 aud）是 ErrAssertionInvalid；kid 不认识是 ErrAssertionUnavailable
// （轮换时漏配了验证方，是服务端的问题）。
func TestAssertion_VerifyClassifiesFailures(t *testing.T) {
	s, pub := keyPair(t, "k1")
	other, _ := keyPair(t, "k1")
	stranger, _ := keyPair(t, "k-unknown")
	v, err := crypto.NewAssertionVerifier(map[string]string{"k1": pub})
	if err != nil {
		t.Fatal(err)
	}
	sign := func(s *crypto.AssertionSigner) string {
		tok, err := s.Sign(crypto.Assertion{UserID: "u", SessionID: "s"})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	good := sign(s)

	// HS256 冒充：用公钥字节当 HMAC 密钥（经典的算法混淆）。
	hsToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": crypto.Issuer, "aud": crypto.AssertionAudience, "sub": "u", "sid": "s",
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(),
	})
	hsToken.Header["kid"] = "k1"
	hs, _ := hsToken.SignedString([]byte(pub))
	none := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"sub": "u", "sid": "s"})
	none.Header["kid"] = "k1"
	noneStr, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	// access token（aud=user）不能当断言用。
	issuer := crypto.NewTokenIssuer("some-hs256-secret-0123456789abcdef", "k1")
	access, _ := issuer.Issue("u", "s", time.Now().Add(time.Minute))

	for name, tc := range map[string]struct {
		token string
		want  error
	}{
		"同 kid 别的私钥签的": {sign(other), crypto.ErrAssertionInvalid},
		"篡改过":          {good[:len(good)-4] + "AAAA", crypto.ErrAssertionInvalid},
		"HS256":        {hs, crypto.ErrAssertionInvalid},
		"alg=none":     {noneStr, crypto.ErrAssertionInvalid},
		"access token": {access, crypto.ErrAssertionInvalid},
		"不是 JWT":       {"garbage", crypto.ErrAssertionInvalid},
		"kid 不认识":      {sign(stranger), crypto.ErrAssertionUnavailable},
	} {
		if _, err := v.Verify(tc.token); !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", name, err, tc.want)
		}
	}
}

// 轮换：新旧两把公钥都在清单里时两者签的都通过；删掉旧的之后旧 kid 签的被拒（服务端问题）。
func TestAssertion_Rotation(t *testing.T) {
	oldS, oldPub := keyPair(t, "old")
	newS, newPub := keyPair(t, "new")
	both, err := crypto.NewAssertionVerifier(map[string]string{"old": oldPub, "new": newPub})
	if err != nil {
		t.Fatal(err)
	}
	onlyNew, err := crypto.NewAssertionVerifier(map[string]string{"new": newPub})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []*crypto.AssertionSigner{oldS, newS} {
		tok, _ := s.Sign(crypto.Assertion{UserID: "u", SessionID: "s"})
		if _, err := both.Verify(tok); err != nil {
			t.Errorf("kid %s 在清单里: %v", s.KID(), err)
		}
	}
	tok, _ := oldS.Sign(crypto.Assertion{UserID: "u", SessionID: "s"})
	if _, err := onlyNew.Verify(tok); !errors.Is(err, crypto.ErrAssertionUnavailable) {
		t.Errorf("删掉旧公钥后 error = %v, want ErrAssertionUnavailable", err)
	}
	if got := both.KIDs(); len(got) != 2 || got[0] != "new" || got[1] != "old" {
		t.Errorf("KIDs = %v", got)
	}
	if onlyNew.Accepts(oldS) {
		t.Error("清单里没有 old 时 Accepts 应为 false")
	}
	mismatched, _ := keyPair(t, "new")
	if onlyNew.Accepts(mismatched) {
		t.Error("kid 相同但公钥不匹配时 Accepts 应为 false")
	}
}

// 只接受 P-256：别的曲线在启动时就拒绝，而不是签名时才报错。
func TestAssertion_RejectsNonP256Keys(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	priv, _ := x509.MarshalPKCS8PrivateKey(key)
	pub, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if _, err := crypto.NewAssertionSigner(base64.StdEncoding.EncodeToString(priv), "k"); err == nil {
		t.Error("P-384 私钥应被拒绝")
	}
	if _, err := crypto.NewAssertionVerifier(map[string]string{"k": base64.StdEncoding.EncodeToString(pub)}); err == nil {
		t.Error("P-384 公钥应被拒绝")
	}
	for name, keys := range map[string]map[string]string{"空清单": {}, "不是 base64": {"k": "%%%"}, "不是 DER": {"k": "AAAA"}} {
		if _, err := crypto.NewAssertionVerifier(keys); err == nil {
			t.Errorf("%s 应被拒绝", name)
		}
	}
	if _, err := crypto.NewAssertionSigner("AAAA", "k"); err == nil {
		t.Error("不是 PKCS#8 的私钥应被拒绝")
	}
}
