package user

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"app.local/internal/config"
	"app.local/internal/platform/crypto"
)

// Config 是 user 模块的全部配置：配置段 user、环境变量前缀 USER_；JWT_* 与 WECHAT_* 是外部系统的凭证，按外部系统命名。
type Config struct {
	JWTSigningKey     string            `yaml:"-"`
	JWTKeyID          string            `yaml:"jwt_key_id"`
	JWTAdditionalKeys map[string]string `yaml:"-"`

	SessionMaxAge        time.Duration `yaml:"session_max_age"`
	SessionIdleTimeout   time.Duration `yaml:"session_idle_timeout"`
	AccessTokenTTL       time.Duration `yaml:"access_token_ttl"`
	RefreshGracePeriod   time.Duration `yaml:"refresh_grace_period"`
	ReauthWindow         time.Duration `yaml:"reauth_window"`
	CookieDeliveryOrigin []string      `yaml:"cookie_delivery_origins"`
	PublicBaseURL        string        `yaml:"public_base_url"`
	// AuthMode 是前台鉴权方式：session（默认，按 access token 查会话）或 gateway（验证网关转来的身份断言，
	// 见 docs/agents/auth.md 的"网关鉴权"）。
	AuthMode string `yaml:"auth_mode"`
	// AssertionSigningKey、AssertionSigningKID 是身份断言的签名私钥（base64 的 PKCS#8 DER，P-256）和它的 kid，
	// 只在 user 服务上配置；配置了网关的会话校验端点才能签发断言。
	AssertionSigningKey string `yaml:"-"`
	AssertionSigningKID string `yaml:"assertion_signing_kid"`
	// SessionSlotLimit > 0 时启用内置会话策略 domain.SlotLimit：同一类设备最多保留这么多个会话，新登录顶掉最久没用的；
	// 0 表示不启用。
	SessionSlotLimit int32        `yaml:"session_slot_limit"`
	WeChat           WeChatConfig `yaml:"wechat"`

	// gatewayPublicKeys 是 LoadConfig 读到的 GATEWAY_ASSERTION_PUBLIC_KEYS（基础设施配置），只用于 Validate 里的
	// 交叉检查，见 validateGateway。
	gatewayPublicKeys map[string]string
}

type WeChatConfig struct {
	Web  WeChatAppCredentials
	MP   WeChatAppCredentials
	Mini WeChatAppCredentials
	App  WeChatAppCredentials
}

type WeChatAppCredentials struct {
	AppID  string `yaml:"-"`
	Secret string `yaml:"-"`
}

func (c WeChatAppCredentials) Enabled() bool { return c.AppID != "" && c.Secret != "" }

func DefaultConfig() Config {
	return Config{
		SessionMaxAge:      30 * 24 * time.Hour,
		SessionIdleTimeout: 7 * 24 * time.Hour,
		AccessTokenTTL:     15 * time.Minute,
		RefreshGracePeriod: 10 * time.Second,
		ReauthWindow:       10 * time.Minute,
		AuthMode:           AuthModeSession,
	}
}

// 前台鉴权方式（USER_AUTH_MODE）。
const (
	AuthModeSession = "session"
	AuthModeGateway = "gateway"
)

func LoadConfig(src *config.Source) (Config, error) {
	cfg := DefaultConfig()
	if err := src.Section("user", &cfg); err != nil {
		return Config{}, fmt.Errorf("user config: %w", err)
	}
	for _, item := range []struct {
		name string
		out  any
	}{
		{"JWT_SIGNING_KEY", &cfg.JWTSigningKey},
		{"JWT_KEY_ID", &cfg.JWTKeyID},
		{"JWT_ADDITIONAL_KEYS", &cfg.JWTAdditionalKeys},
		{"USER_PUBLIC_BASE_URL", &cfg.PublicBaseURL},
		{"USER_COOKIE_DELIVERY_ORIGINS", &cfg.CookieDeliveryOrigin},
		{"USER_AUTH_MODE", &cfg.AuthMode},
		{"USER_ASSERTION_SIGNING_KEY", &cfg.AssertionSigningKey},
		{"USER_ASSERTION_SIGNING_KID", &cfg.AssertionSigningKID},
		{"WECHAT_WEB_APP_ID", &cfg.WeChat.Web.AppID},
		{"WECHAT_WEB_SECRET", &cfg.WeChat.Web.Secret},
		{"WECHAT_MP_APP_ID", &cfg.WeChat.MP.AppID},
		{"WECHAT_MP_SECRET", &cfg.WeChat.MP.Secret},
		{"WECHAT_MINI_APP_ID", &cfg.WeChat.Mini.AppID},
		{"WECHAT_MINI_SECRET", &cfg.WeChat.Mini.Secret},
		{"WECHAT_APP_APP_ID", &cfg.WeChat.App.AppID},
		{"WECHAT_APP_SECRET", &cfg.WeChat.App.Secret},
		{"USER_SESSION_MAX_AGE", &cfg.SessionMaxAge},
		{"USER_SESSION_IDLE_TIMEOUT", &cfg.SessionIdleTimeout},
		{"USER_ACCESS_TOKEN_TTL", &cfg.AccessTokenTTL},
		{"USER_REFRESH_GRACE_PERIOD", &cfg.RefreshGracePeriod},
		{"USER_REAUTH_WINDOW", &cfg.ReauthWindow},
		{"USER_SESSION_SLOT_LIMIT", &cfg.SessionSlotLimit},
	} {
		if err := src.Env(item.name, item.out); err != nil {
			return Config{}, fmt.Errorf("user config: %w", err)
		}
	}
	if err := src.Env("GATEWAY_ASSERTION_PUBLIC_KEYS", &cfg.gatewayPublicKeys); err != nil {
		return Config{}, fmt.Errorf("user config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var missing []string
	if c.JWTSigningKey == "" {
		missing = append(missing, "JWT_SIGNING_KEY")
	}
	if c.JWTKeyID == "" {
		missing = append(missing, "JWT_KEY_ID")
	}
	if c.PublicBaseURL == "" {
		missing = append(missing, "USER_PUBLIC_BASE_URL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("user config: missing required env var(s): %s", strings.Join(missing, ", "))
	}
	for name, d := range map[string]time.Duration{
		"USER_SESSION_MAX_AGE":      c.SessionMaxAge,
		"USER_SESSION_IDLE_TIMEOUT": c.SessionIdleTimeout,
		"USER_ACCESS_TOKEN_TTL":     c.AccessTokenTTL,
		"USER_REFRESH_GRACE_PERIOD": c.RefreshGracePeriod,
		"USER_REAUTH_WINDOW":        c.ReauthWindow,
	} {
		if d <= 0 {
			return fmt.Errorf("user config: %s must be > 0, got %s", name, d)
		}
	}
	if err := c.validateJWTKeys(); err != nil {
		return err
	}
	if err := c.validateGateway(); err != nil {
		return err
	}
	if c.SessionSlotLimit < 0 {
		return fmt.Errorf("user config: USER_SESSION_SLOT_LIMIT must be >= 0, got %d", c.SessionSlotLimit)
	}
	for _, app := range []struct {
		name string
		cred WeChatAppCredentials
	}{
		{"WECHAT_WEB", c.WeChat.Web},
		{"WECHAT_MP", c.WeChat.MP},
		{"WECHAT_MINI", c.WeChat.Mini},
		{"WECHAT_APP", c.WeChat.App},
	} {
		if (app.cred.AppID == "") != (app.cred.Secret == "") {
			return fmt.Errorf("user config: %s_APP_ID and %s_SECRET must be set together", app.name, app.name)
		}
	}
	if !config.IsCanonicalOrigin(c.PublicBaseURL) {
		return fmt.Errorf("user config: USER_PUBLIC_BASE_URL must be a canonical origin, got %q", c.PublicBaseURL)
	}
	for _, origin := range c.CookieDeliveryOrigin {
		if !config.IsCanonicalOrigin(origin) {
			return fmt.Errorf("user config: USER_COOKIE_DELIVERY_ORIGINS entry must be a canonical origin, got %q", origin)
		}
	}
	return nil
}

func (c Config) Redacted() map[string]any {
	return map[string]any{
		"jwt_signing_key":         "***",
		"jwt_key_id":              c.JWTKeyID,
		"public_base_url":         c.PublicBaseURL,
		"cookie_delivery_origins": c.CookieDeliveryOrigin,
		"auth_mode":               c.AuthMode,
		"assertion_signing_key":   redactedIfSet(c.AssertionSigningKey),
		"assertion_signing_kid":   c.AssertionSigningKID,
		"session_slot_limit":      c.SessionSlotLimit,
		"wechat_web_app_id":       c.WeChat.Web.AppID,
		"wechat_mp_app_id":        c.WeChat.MP.AppID,
		"wechat_mini_app_id":      c.WeChat.Mini.AppID,
		"wechat_app_app_id":       c.WeChat.App.AppID,
	}
}

// minJWTKeyBytes 是 HS256 密钥的最小长度：HMAC-SHA256 的输出长度。任何人拿到一个 access token 都能离线
// 暴力猜密钥，短密钥（如照抄的 "changeme"）等于把签发登录令牌的能力交出去。
const minJWTKeyBytes = 32

// validateJWTKeys 检查当前密钥和轮换用的旧密钥。旧密钥的 kid 与当前 kid 相同时永远不会被用到
// （KeyFunc 先匹配当前 kid），轮换会悄悄出错，所以直接拒绝。
func (c Config) validateJWTKeys() error {
	if len(c.JWTSigningKey) < minJWTKeyBytes {
		return fmt.Errorf("user config: JWT_SIGNING_KEY must be at least %d bytes (e.g. openssl rand -base64 32)", minJWTKeyBytes)
	}
	for kid, key := range c.JWTAdditionalKeys {
		if kid == c.JWTKeyID {
			return fmt.Errorf("user config: JWT_ADDITIONAL_KEYS must not contain the current JWT_KEY_ID %q", kid)
		}
		if len(key) < minJWTKeyBytes {
			return fmt.Errorf("user config: JWT_ADDITIONAL_KEYS key %q must be at least %d bytes", kid, minJWTKeyBytes)
		}
	}
	return nil
}

func redactedIfSet(s string) string {
	if s == "" {
		return ""
	}
	return "***"
}

// validateGateway 检查网关鉴权相关的配置。publicKeys 是 GATEWAY_ASSERTION_PUBLIC_KEYS：它是分发给所有验证方的
// 公钥清单（基础设施配置），这里读同一个变量，检查签发方正在用的私钥对应的公钥在清单里。不自动把自己的公钥加进
// 自己的验证集合：那样签发方自己验得过，清单里漏了这把公钥的其它服务却全部失败，配错被掩盖。
func (c Config) validateGateway() error {
	publicKeys := c.gatewayPublicKeys
	switch c.AuthMode {
	case AuthModeSession, AuthModeGateway:
	default:
		return fmt.Errorf("user config: USER_AUTH_MODE must be %q or %q, got %q", AuthModeSession, AuthModeGateway, c.AuthMode)
	}
	if c.AuthMode == AuthModeGateway && len(publicKeys) == 0 {
		return errors.New("user config: USER_AUTH_MODE=gateway requires GATEWAY_ASSERTION_PUBLIC_KEYS")
	}
	if (c.AssertionSigningKey == "") != (c.AssertionSigningKID == "") {
		return errors.New("user config: USER_ASSERTION_SIGNING_KEY and USER_ASSERTION_SIGNING_KID must be set together")
	}
	if c.AssertionSigningKey == "" {
		return nil
	}
	signer, err := crypto.NewAssertionSigner(c.AssertionSigningKey, c.AssertionSigningKID)
	if err != nil {
		return fmt.Errorf("user config: USER_ASSERTION_SIGNING_KEY: %w", err)
	}
	if len(publicKeys) == 0 {
		return errors.New("user config: USER_ASSERTION_SIGNING_KEY is set but GATEWAY_ASSERTION_PUBLIC_KEYS is empty: " +
			"add this key's public key under USER_ASSERTION_SIGNING_KID")
	}
	verifier, err := crypto.NewAssertionVerifier(publicKeys)
	if err != nil {
		return fmt.Errorf("user config: GATEWAY_ASSERTION_PUBLIC_KEYS: %w", err)
	}
	if !verifier.Accepts(signer) {
		return fmt.Errorf("user config: GATEWAY_ASSERTION_PUBLIC_KEYS has no matching public key for USER_ASSERTION_SIGNING_KID %q: "+
			"every verifier would reject the assertions this service signs", c.AssertionSigningKID)
	}
	return nil
}
