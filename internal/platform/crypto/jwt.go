package crypto

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenIssuer 固定用 HS256：服务自己签发、自己校验，一个共享密钥就够；出现只需
// 校验、不该持有签名密钥的第二个服务时再换 RS256/ES256。
type TokenIssuer struct {
	signingKey []byte
	keyID      string
}

// NewTokenIssuer 接受当前用于签发新 token 的密钥和它的 kid。密钥轮换
// 时旧密钥挪进 KeyFunc 的 additionalKeys，只用于校验、不再用于签发。
func NewTokenIssuer(signingKey, keyID string) *TokenIssuer {
	return &TokenIssuer{signingKey: []byte(signingKey), keyID: keyID}
}

// AccessClaims 是前台用户 access token 的全部内容：只含
// iss/aud/sub/sid/iat/exp，aud=user 表示前台用户（后台员工不用 JWT）。
type AccessClaims struct {
	SID string `json:"sid"`
	jwt.RegisteredClaims
}

// Issuer 是 access token 的 iss，Audience 是 aud：代码常量，不做成配置
// （见 docs/agents/auth.md 的"会话"）。后台员工的服务端会话不走 JWT，不会撞。
const (
	Issuer   = "go-starter"
	Audience = "user"
)

// Issue 签发一个前台用户的 access token。
func (i *TokenIssuer) Issue(userID, sid string, expiresAt time.Time) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, AccessClaims{
		SID: sid,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    Issuer,
			Subject:   userID,
			Audience:  jwt.ClaimStrings{Audience},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	})
	token.Header["kid"] = i.keyID

	signed, err := token.SignedString(i.signingKey)
	if err != nil {
		return "", fmt.Errorf("issue token: %w", err)
	}
	return signed, nil
}

// KeyFunc 按 token header 的 kid 从"当前密钥 + 旧密钥集合"里选校验密钥，
// 密钥轮换靠多把 kid 并存实现。
func KeyFunc(currentKeyID string, currentKey []byte, additionalKeys map[string]string) jwt.Keyfunc {
	return func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		kid, _ := token.Header["kid"].(string)
		if kid == "" || kid == currentKeyID {
			return currentKey, nil
		}
		if key, ok := additionalKeys[kid]; ok {
			return []byte(key), nil
		}
		return nil, fmt.Errorf("unknown key id: %q", kid)
	}
}
