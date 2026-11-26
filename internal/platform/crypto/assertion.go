package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// 身份断言是 user 服务校验会话后签发、只在网关到后端的内网里流转的短期凭证（docs/agents/auth.md 的"网关鉴权"）。
// 用 ES256：只有签发方持有私钥，验证方拿到的公钥不能签名，拆出去的服务被攻破也伪造不了断言。
const (
	// AssertionAudience 与 access token 的 Audience 不同，两种令牌不能互相冒用。
	AssertionAudience = "gateway-assertion"
	// AssertionTTL 只限制断言泄露后的重放窗口，不是缓存：每个请求都重新签发。
	AssertionTTL = 30 * time.Second
	// assertionLeeway 是验证时允许的签发方与验证方的时钟偏差。
	assertionLeeway = 5 * time.Second
)

// Verify 的两类失败，用于排查（写进错误日志）：前者多半是公钥贴错或伪造，后者多半是密钥轮换漏配、时钟偏差。
// 网关后面两者都不是客户端令牌的问题，网关身份中间件都返回 503。
var (
	// ErrAssertionInvalid：断言格式不对、签名不对、aud/iss/alg 不符。
	ErrAssertionInvalid = errors.New("assertion invalid")
	// ErrAssertionUnavailable：kid 不认识、已过期或尚未生效。
	ErrAssertionUnavailable = errors.New("assertion unavailable")
)

// Assertion 是断言携带的身份。
type Assertion struct {
	UserID    string
	SessionID string
	AuthTime  time.Time
	AMR       []string
	// ReauthUntil 是最近验证的截止时间（auth_time + 重新验证窗口），由签发方算好：验证方不需要知道窗口多长。
	ReauthUntil time.Time
}

type assertionClaims struct {
	SID         string           `json:"sid"`
	AuthTime    *jwt.NumericDate `json:"auth_time,omitempty"`
	AMR         []string         `json:"amr,omitempty"`
	ReauthUntil *jwt.NumericDate `json:"reauth_until,omitempty"`
	jwt.RegisteredClaims
}

// AssertionSigner 签发断言，只在 user 服务里。
type AssertionSigner struct {
	key *ecdsa.PrivateKey
	kid string
	now func() time.Time
}

// NewAssertionSigner 解析 base64 编码的 PKCS#8 DER 私钥，必须是 P-256。
func NewAssertionSigner(privateKeyB64, kid string) (*AssertionSigner, error) {
	if kid == "" {
		return nil, errors.New("assertion signer: kid is empty")
	}
	der, err := base64.StdEncoding.DecodeString(privateKeyB64)
	if err != nil {
		return nil, fmt.Errorf("assertion signer: private key is not base64: %w", err)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("assertion signer: private key is not PKCS#8 DER: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("assertion signer: private key must be an ECDSA P-256 key (ES256)")
	}
	return &AssertionSigner{key: key, kid: kid, now: time.Now}, nil
}

// KID 是签名用的 kid。
func (s *AssertionSigner) KID() string { return s.kid }

// Sign 签发一个有效期 AssertionTTL 的断言。
func (s *AssertionSigner) Sign(a Assertion) (string, error) {
	now := s.now()
	claims := assertionClaims{
		SID: a.SessionID,
		AMR: a.AMR,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    Issuer,
			Subject:   a.UserID,
			Audience:  jwt.ClaimStrings{AssertionAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(AssertionTTL)),
		},
	}
	if !a.AuthTime.IsZero() {
		claims.AuthTime = jwt.NewNumericDate(a.AuthTime)
	}
	if !a.ReauthUntil.IsZero() {
		claims.ReauthUntil = jwt.NewNumericDate(a.ReauthUntil)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = s.kid
	signed, err := token.SignedString(s.key)
	if err != nil {
		return "", fmt.Errorf("sign assertion: %w", err)
	}
	return signed, nil
}

// AssertionVerifier 按 kid 选公钥验证断言。验证方只需要一组可接受的公钥，不区分"当前"和"旧"。
type AssertionVerifier struct {
	keys map[string]*ecdsa.PublicKey
	now  func() time.Time
}

// NewAssertionVerifier 解析 kid → base64 编码的 SPKI DER 公钥，每把都必须是 P-256；keys 为空时返回错误。
func NewAssertionVerifier(keys map[string]string) (*AssertionVerifier, error) {
	if len(keys) == 0 {
		return nil, errors.New("assertion verifier: no public keys")
	}
	out := make(map[string]*ecdsa.PublicKey, len(keys))
	for kid, b64 := range keys {
		der, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("assertion verifier: public key %q is not base64: %w", kid, err)
		}
		parsed, err := x509.ParsePKIXPublicKey(der)
		if err != nil {
			return nil, fmt.Errorf("assertion verifier: public key %q is not SPKI DER: %w", kid, err)
		}
		pub, ok := parsed.(*ecdsa.PublicKey)
		if !ok || pub.Curve != elliptic.P256() {
			return nil, fmt.Errorf("assertion verifier: public key %q must be an ECDSA P-256 key (ES256)", kid)
		}
		out[kid] = pub
	}
	return &AssertionVerifier{keys: out, now: time.Now}, nil
}

// KIDs 是已加载的公钥 kid，排好序，用于启动日志和 gateway_assertion_key_info 指标。
func (v *AssertionVerifier) KIDs() []string {
	return slices.Sorted(maps.Keys(v.keys))
}

// Accepts 报告 v 是否认得 s 的签名：kid 在清单里，且对应公钥就是 s 的私钥的公钥。签发方启动时用它检查分发给
// 所有验证方的公钥清单是否包含自己正在用的那把。
func (v *AssertionVerifier) Accepts(s *AssertionSigner) bool {
	pub, ok := v.keys[s.kid]
	return ok && pub.Equal(&s.key.PublicKey)
}

// Verify 验证断言。签名不对、格式或 aud/iss/alg 不符返回 ErrAssertionInvalid；kid 不认识、过期或尚未生效返回
// ErrAssertionUnavailable。
func (v *AssertionVerifier) Verify(token string) (Assertion, error) {
	claims := &assertionClaims{}
	_, err := jwt.ParseWithClaims(token, claims, v.keyFunc,
		jwt.WithValidMethods([]string{jwt.SigningMethodES256.Alg()}),
		jwt.WithIssuer(Issuer),
		jwt.WithAudience(AssertionAudience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(assertionLeeway),
		jwt.WithTimeFunc(v.now),
	)
	switch {
	case err == nil:
	case errors.Is(err, errUnknownKID), errors.Is(err, jwt.ErrTokenExpired),
		errors.Is(err, jwt.ErrTokenNotValidYet), errors.Is(err, jwt.ErrTokenUsedBeforeIssued):
		return Assertion{}, fmt.Errorf("%w: %w", ErrAssertionUnavailable, err)
	default:
		return Assertion{}, fmt.Errorf("%w: %w", ErrAssertionInvalid, err)
	}
	if claims.Subject == "" || claims.SID == "" {
		return Assertion{}, fmt.Errorf("%w: missing sub or sid", ErrAssertionInvalid)
	}
	a := Assertion{UserID: claims.Subject, SessionID: claims.SID, AMR: claims.AMR}
	if claims.AuthTime != nil {
		a.AuthTime = claims.AuthTime.Time
	}
	if claims.ReauthUntil != nil {
		a.ReauthUntil = claims.ReauthUntil.Time
	}
	return a, nil
}

var errUnknownKID = errors.New("unknown key id")

func (v *AssertionVerifier) keyFunc(token *jwt.Token) (any, error) {
	if _, ok := token.Method.(*jwt.SigningMethodECDSA); !ok {
		return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
	}
	kid, _ := token.Header["kid"].(string)
	if kid == "" {
		return nil, errors.New("missing key id")
	}
	pub, ok := v.keys[kid]
	if !ok {
		return nil, fmt.Errorf("%w: %q", errUnknownKID, kid)
	}
	return pub, nil
}

// NewAssertionKeyPair 生成一对 P-256 密钥，按 NewAssertionSigner、NewAssertionVerifier 接受的格式（base64 的
// PKCS#8 DER 私钥、SPKI DER 公钥）返回。测试用；部署时用 openssl 生成（docs/agents/deploy-ci.md）。
func NewAssertionKeyPair() (privateKeyB64, publicKeyB64 string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	priv, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", err
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(priv), base64.StdEncoding.EncodeToString(pub), nil
}
