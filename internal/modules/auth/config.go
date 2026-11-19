package auth

import (
	"fmt"
	"strings"
	"time"

	"app.local/internal/config"
)

// Config 是 auth 模块的全部配置。
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
	WeChat               WeChatConfig  `yaml:"wechat"`
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
	}
}

func LoadConfig(src *config.Source) (Config, error) {
	cfg := DefaultConfig()
	if err := src.Section("auth", &cfg); err != nil {
		return Config{}, fmt.Errorf("auth config: %w", err)
	}
	for _, item := range []struct {
		name string
		out  any
	}{
		{"JWT_SIGNING_KEY", &cfg.JWTSigningKey},
		{"JWT_KEY_ID", &cfg.JWTKeyID},
		{"JWT_ADDITIONAL_KEYS", &cfg.JWTAdditionalKeys},
		{"AUTH_PUBLIC_BASE_URL", &cfg.PublicBaseURL},
		{"AUTH_COOKIE_DELIVERY_ORIGINS", &cfg.CookieDeliveryOrigin},
		{"WECHAT_WEB_APP_ID", &cfg.WeChat.Web.AppID},
		{"WECHAT_WEB_SECRET", &cfg.WeChat.Web.Secret},
		{"WECHAT_MP_APP_ID", &cfg.WeChat.MP.AppID},
		{"WECHAT_MP_SECRET", &cfg.WeChat.MP.Secret},
		{"WECHAT_MINI_APP_ID", &cfg.WeChat.Mini.AppID},
		{"WECHAT_MINI_SECRET", &cfg.WeChat.Mini.Secret},
		{"WECHAT_APP_APP_ID", &cfg.WeChat.App.AppID},
		{"WECHAT_APP_SECRET", &cfg.WeChat.App.Secret},
		{"AUTH_SESSION_MAX_AGE", &cfg.SessionMaxAge},
		{"AUTH_SESSION_IDLE_TIMEOUT", &cfg.SessionIdleTimeout},
		{"AUTH_ACCESS_TOKEN_TTL", &cfg.AccessTokenTTL},
		{"AUTH_REFRESH_GRACE_PERIOD", &cfg.RefreshGracePeriod},
		{"AUTH_REAUTH_WINDOW", &cfg.ReauthWindow},
	} {
		if err := src.Env(item.name, item.out); err != nil {
			return Config{}, fmt.Errorf("auth config: %w", err)
		}
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
		missing = append(missing, "AUTH_PUBLIC_BASE_URL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("auth config: missing required env var(s): %s", strings.Join(missing, ", "))
	}
	for name, d := range map[string]time.Duration{
		"AUTH_SESSION_MAX_AGE":      c.SessionMaxAge,
		"AUTH_SESSION_IDLE_TIMEOUT": c.SessionIdleTimeout,
		"AUTH_ACCESS_TOKEN_TTL":     c.AccessTokenTTL,
		"AUTH_REFRESH_GRACE_PERIOD": c.RefreshGracePeriod,
		"AUTH_REAUTH_WINDOW":        c.ReauthWindow,
	} {
		if d <= 0 {
			return fmt.Errorf("auth config: %s must be > 0, got %s", name, d)
		}
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
			return fmt.Errorf("auth config: %s_APP_ID and %s_SECRET must be set together", app.name, app.name)
		}
	}
	if !config.IsCanonicalOrigin(c.PublicBaseURL) {
		return fmt.Errorf("auth config: AUTH_PUBLIC_BASE_URL must be a canonical origin, got %q", c.PublicBaseURL)
	}
	for _, origin := range c.CookieDeliveryOrigin {
		if !config.IsCanonicalOrigin(origin) {
			return fmt.Errorf("auth config: AUTH_COOKIE_DELIVERY_ORIGINS entry must be a canonical origin, got %q", origin)
		}
	}
	return nil
}

func (c Config) Redacted() map[string]any {
	return map[string]any{
		"jwt_signing_key":         "***",
		"jwt_key_id":              c.JWTKeyID,
		"auth_public_base_url":    c.PublicBaseURL,
		"cookie_delivery_origins": c.CookieDeliveryOrigin,
		"wechat_web_app_id":       c.WeChat.Web.AppID,
		"wechat_mp_app_id":        c.WeChat.MP.AppID,
		"wechat_mini_app_id":      c.WeChat.Mini.AppID,
		"wechat_app_app_id":       c.WeChat.App.AppID,
	}
}
