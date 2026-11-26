package crypto

import (
	"errors"
	"testing"
	"time"
)

func newTestPair(t *testing.T, kid string) (*AssertionSigner, string) {
	t.Helper()
	priv, pub, err := NewAssertionKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewAssertionSigner(priv, kid)
	if err != nil {
		t.Fatal(err)
	}
	return s, pub
}

// 过期、尚未生效都是服务端的问题（时钟偏差、在途过久），和签名不对区分开。
func TestAssertionVerifier_TimeProblemsAreUnavailable(t *testing.T) {
	s, pub := newTestPair(t, "k1")
	v, err := NewAssertionVerifier(map[string]string{"k1": pub})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	s.now = func() time.Time { return base }
	token, err := s.Sign(Assertion{UserID: "u", SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	for name, now := range map[string]time.Time{
		"过期（超过有效期和允许偏差）": base.Add(AssertionTTL + assertionLeeway + time.Second),
		"尚未签发（验证方时钟慢）":   base.Add(-assertionLeeway - time.Second),
	} {
		v.now = func() time.Time { return now }
		if _, err := v.Verify(token); !errors.Is(err, ErrAssertionUnavailable) {
			t.Errorf("%s: error = %v, want ErrAssertionUnavailable", name, err)
		}
	}
	v.now = func() time.Time { return base.Add(AssertionTTL + assertionLeeway - time.Second) }
	if _, err := v.Verify(token); err != nil {
		t.Errorf("允许偏差之内 error = %v", err)
	}
}
