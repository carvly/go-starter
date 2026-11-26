package user

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"app.local/internal/config"
	"app.local/internal/platform/crypto"
)

// testSigningKey 满足 JWT 密钥的最小长度（32 字节）。
const testSigningKey = "test-signing-key-0123456789abcdef"

func setAuthEnv(t *testing.T) {
	t.Helper()
	t.Setenv("JWT_SIGNING_KEY", testSigningKey)
	t.Setenv("JWT_KEY_ID", "test-kid")
	t.Setenv("USER_PUBLIC_BASE_URL", "https://api.example.com")
	t.Setenv("WECHAT_WEB_APP_ID", "web-appid")
	t.Setenv("WECHAT_WEB_SECRET", "web-secret")
	t.Setenv("WECHAT_MP_APP_ID", "mp-appid")
	t.Setenv("WECHAT_MP_SECRET", "mp-secret")
	t.Setenv("WECHAT_MINI_APP_ID", "mini-appid")
	t.Setenv("WECHAT_MINI_SECRET", "mini-secret")
	t.Setenv("WECHAT_APP_APP_ID", "app-appid")
	t.Setenv("WECHAT_APP_SECRET", "app-secret")
}

func TestLoadConfig_RequiredAndDefaults(t *testing.T) {
	setAuthEnv(t)
	cfg, err := LoadConfig(config.NewSource())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionMaxAge != DefaultConfig().SessionMaxAge || cfg.AccessTokenTTL != DefaultConfig().AccessTokenTTL {
		t.Errorf("session defaults = %+v", cfg)
	}
	if !cfg.WeChat.Web.Enabled() || !cfg.WeChat.MP.Enabled() || !cfg.WeChat.Mini.Enabled() || !cfg.WeChat.App.Enabled() {
		t.Errorf("微信入口没有全部启用：%+v", cfg.WeChat)
	}
}

func TestLoadConfig_MissingRequired(t *testing.T) {
	if _, err := LoadConfig(config.NewSource()); err == nil {
		t.Fatal("LoadConfig() error = nil, want missing required fields")
	}
}

func TestLoadConfig_JWTAdditionalKeys(t *testing.T) {
	setAuthEnv(t)
	old1, old2 := strings.Repeat("1", 32), strings.Repeat("2", 32)
	t.Setenv("JWT_ADDITIONAL_KEYS", "old-1:"+old1+", old-2:"+old2)
	cfg, err := LoadConfig(config.NewSource())
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.JWTAdditionalKeys) != 2 || cfg.JWTAdditionalKeys["old-1"] != old1 {
		t.Errorf("JWTAdditionalKeys = %v", cfg.JWTAdditionalKeys)
	}
}

// 弱密钥、与当前 kid 重名的旧密钥、写错格式的旧密钥都在启动时拒绝，错误写明变量名。
func TestLoadConfig_RejectsUnsafeJWTKeys(t *testing.T) {
	long := strings.Repeat("k", 32)
	for name, env := range map[string]map[string]string{
		"当前密钥太短":        {"JWT_SIGNING_KEY": "changeme"},
		"旧密钥太短":         {"JWT_ADDITIONAL_KEYS": "old:short"},
		"旧密钥与当前 kid 同名": {"JWT_ADDITIONAL_KEYS": "test-kid:" + long},
		"旧密钥格式不对":       {"JWT_ADDITIONAL_KEYS": "old" + long},
	} {
		t.Run(name, func(t *testing.T) {
			setAuthEnv(t)
			var want string
			for k, v := range env {
				t.Setenv(k, v)
				want = k
			}
			if _, err := LoadConfig(config.NewSource()); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("LoadConfig() error = %v, want 写明 %s", err, want)
			}
		})
	}
}

func TestLoadConfig_WeChatEntriesOptionalButMustBeComplete(t *testing.T) {
	setAuthEnv(t)
	for _, key := range []string{"WECHAT_WEB_APP_ID", "WECHAT_WEB_SECRET", "WECHAT_MP_APP_ID", "WECHAT_MP_SECRET", "WECHAT_APP_APP_ID", "WECHAT_APP_SECRET"} {
		t.Setenv(key, "")
	}
	cfg, err := LoadConfig(config.NewSource())
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.WeChat.Mini.Enabled() || cfg.WeChat.Web.Enabled() || cfg.WeChat.MP.Enabled() || cfg.WeChat.App.Enabled() {
		t.Errorf("enabled entries = %+v", cfg.WeChat)
	}
	t.Setenv("WECHAT_MP_APP_ID", "mp-only")
	if _, err := LoadConfig(config.NewSource()); err == nil || !strings.Contains(err.Error(), "WECHAT_MP") {
		t.Fatalf("half-configured error = %v", err)
	}
}

func TestLoadConfig_RejectsInvalidSessionAndOrigin(t *testing.T) {
	setAuthEnv(t)
	t.Setenv("USER_ACCESS_TOKEN_TTL", "0s")
	if _, err := LoadConfig(config.NewSource()); err == nil {
		t.Fatal("USER_ACCESS_TOKEN_TTL=0 should fail")
	}
	setAuthEnv(t)
	t.Setenv("USER_PUBLIC_BASE_URL", "https://api.example.com/")
	if _, err := LoadConfig(config.NewSource()); err == nil {
		t.Fatal("non-canonical public base URL should fail")
	}
}

func TestLoadConfig_SecretInYAMLRejected(t *testing.T) {
	setAuthEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("user:\n  jwt_signing_key: leaked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", path)
	t.Setenv("DB_DSN", "postgres://unused")
	t.Setenv("CORS_ALLOW_ORIGINS", "https://example.com")
	infra, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(infra.Source); err == nil {
		t.Fatal("JWT secret in YAML should fail")
	}
}

func TestConfig_Redacted(t *testing.T) {
	setAuthEnv(t)
	cfg, err := LoadConfig(config.NewSource())
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Redacted()["jwt_signing_key"]; got != "***" {
		t.Errorf("jwt_signing_key = %v", got)
	}
}

func TestLoadConfig_SessionSlotLimit(t *testing.T) {
	setAuthEnv(t)
	cfg, err := LoadConfig(config.NewSource())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionSlotLimit != 0 || configuredSessionPolicy(cfg) != nil {
		t.Errorf("默认不启用会话策略：limit=%d", cfg.SessionSlotLimit)
	}
	t.Setenv("USER_SESSION_SLOT_LIMIT", "1")
	if cfg, err = LoadConfig(config.NewSource()); err != nil || configuredSessionPolicy(cfg) == nil {
		t.Errorf("USER_SESSION_SLOT_LIMIT=1 应启用内置策略：%v", err)
	}
	for _, bad := range []string{"-1", "one"} {
		t.Setenv("USER_SESSION_SLOT_LIMIT", bad)
		if _, err := LoadConfig(config.NewSource()); err == nil || !strings.Contains(err.Error(), "USER_SESSION_SLOT_LIMIT") {
			t.Errorf("USER_SESSION_SLOT_LIMIT=%q error = %v, want 指明配置项的错误", bad, err)
		}
	}
}

// 网关鉴权的启动校验：模式取值、gateway 模式要有公钥、签名私钥与 kid 成对、公钥清单里必须有签发方自己的公钥。
func TestLoadConfig_GatewayChecks(t *testing.T) {
	priv, pub, err := crypto.NewAssertionKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	_, otherPub, _ := crypto.NewAssertionKeyPair()
	ok := map[string]map[string]string{
		"默认 session":  {},
		"gateway 有公钥": {"USER_AUTH_MODE": "gateway", "GATEWAY_ASSERTION_PUBLIC_KEYS": "k1:" + pub},
		"签发方的公钥在清单里":  {"USER_ASSERTION_SIGNING_KEY": priv, "USER_ASSERTION_SIGNING_KID": "k1", "GATEWAY_ASSERTION_PUBLIC_KEYS": "k0:" + otherPub + ",k1:" + pub},
	}
	for name, env := range ok {
		t.Run(name, func(t *testing.T) {
			setAuthEnv(t)
			for k, v := range env {
				t.Setenv(k, v)
			}
			if _, err := LoadConfig(config.NewSource()); err != nil {
				t.Errorf("LoadConfig() error = %v", err)
			}
		})
	}
	bad := map[string]struct {
		env  map[string]string
		want string
	}{
		"模式取值不对":       {map[string]string{"USER_AUTH_MODE": "trusted"}, "USER_AUTH_MODE"},
		"gateway 没有公钥": {map[string]string{"USER_AUTH_MODE": "gateway"}, "GATEWAY_ASSERTION_PUBLIC_KEYS"},
		"私钥缺 kid":      {map[string]string{"USER_ASSERTION_SIGNING_KEY": priv, "GATEWAY_ASSERTION_PUBLIC_KEYS": "k1:" + pub}, "USER_ASSERTION_SIGNING_KID"},
		"私钥不合法":        {map[string]string{"USER_ASSERTION_SIGNING_KEY": "AAAA", "USER_ASSERTION_SIGNING_KID": "k1", "GATEWAY_ASSERTION_PUBLIC_KEYS": "k1:" + pub}, "USER_ASSERTION_SIGNING_KEY"},
		"清单为空":         {map[string]string{"USER_ASSERTION_SIGNING_KEY": priv, "USER_ASSERTION_SIGNING_KID": "k1"}, "GATEWAY_ASSERTION_PUBLIC_KEYS"},
		"清单里没有这个 kid":  {map[string]string{"USER_ASSERTION_SIGNING_KEY": priv, "USER_ASSERTION_SIGNING_KID": "k1", "GATEWAY_ASSERTION_PUBLIC_KEYS": "k0:" + pub}, "USER_ASSERTION_SIGNING_KID"},
		"kid 对但公钥不匹配":  {map[string]string{"USER_ASSERTION_SIGNING_KEY": priv, "USER_ASSERTION_SIGNING_KID": "k1", "GATEWAY_ASSERTION_PUBLIC_KEYS": "k1:" + otherPub}, "USER_ASSERTION_SIGNING_KID"},
	}
	for name, tc := range bad {
		t.Run(name, func(t *testing.T) {
			setAuthEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if _, err := LoadConfig(config.NewSource()); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("LoadConfig() error = %v, want 写明 %s", err, tc.want)
			}
		})
	}
}
