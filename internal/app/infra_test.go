package app

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"app.local/internal/config"
	"app.local/internal/platform/crypto"
	"app.local/internal/platform/process"
)

// pgxpool.NewWithConfig 只解析配置、不立即连接，所以这些测试不需要真实 Postgres。

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func baseTestConfig() *config.Config {
	return &config.Config{
		DB: config.DBConfig{DSN: "postgres://app:app@127.0.0.1:5432/app?sslmode=disable"},
		Argon2: config.Argon2Params{
			MemoryKiB: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
		},
		HTTP: config.HTTPConfig{
			BodyLimitBytes:           2 * 1024 * 1024,
			RequestTimeout:           5 * time.Second,
			GlobalRateLimitPerSecond: 1000,
		},
		CORS: config.CORSConfig{AllowOrigins: []string{"https://example.com"}},
	}
}

func testModuleConfigs(t *testing.T) moduleConfigs {
	t.Helper()
	t.Setenv("JWT_SIGNING_KEY", "test-signing-key-0123456789abcdef")
	t.Setenv("JWT_KEY_ID", "test-kid")
	t.Setenv("USER_PUBLIC_BASE_URL", "https://api.example.com")
	configs, err := loadModuleConfigs(config.NewSource())
	if err != nil {
		t.Fatal(err)
	}
	return configs
}

func TestNewInfra_WithoutRedis(t *testing.T) {
	in, err := NewInfra(t.Context(), baseTestConfig(), process.New("api", "test"), discardLogger())
	if err != nil {
		t.Fatalf("NewInfra() error = %v", err)
	}
	defer in.Close(t.Context())

	if in.Redis != nil {
		t.Error("没有配置 REDIS_ADDR 时不应该构造 Redis 客户端")
	}
	if len(in.Pingers) != 1 {
		t.Errorf("len(Pingers) = %d, want 1（只有数据库）", len(in.Pingers))
	}
}

func TestNewInfra_WithRedis_RegistersPinger(t *testing.T) {
	mr := miniredis.RunT(t)
	cfg := baseTestConfig()
	cfg.Redis.Addr = mr.Addr()

	in, err := NewInfra(t.Context(), cfg, process.New("api", "test"), discardLogger())
	if err != nil {
		t.Fatalf("NewInfra() error = %v", err)
	}
	defer in.Close(t.Context())

	if in.Redis == nil {
		t.Fatal("配置了 REDIS_ADDR 就应该构造 Redis 客户端")
	}
	if len(in.Pingers) != 2 {
		t.Fatalf("len(Pingers) = %d, want 2（数据库 + Redis）", len(in.Pingers))
	}
	if err := in.Pingers[1].Ping(t.Context()); err != nil {
		t.Errorf("Redis Pinger 应该能 Ping 通 miniredis: %v", err)
	}
}

// 配了 GATEWAY_ASSERTION_PUBLIC_KEYS 时构造验证器，每个 kid 一条 gateway_assertion_key_info；没配时为 nil。
func TestNewInfra_AssertionVerifier(t *testing.T) {
	in, err := NewInfra(t.Context(), baseTestConfig(), process.New("api", "test"), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	in.Close(t.Context())
	if in.AssertionVerifier != nil {
		t.Error("没配公钥时不应构造验证器")
	}

	_, pub, err := crypto.NewAssertionKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	cfg := baseTestConfig()
	cfg.Gateway.AssertionPublicKeys = map[string]string{"k2": pub, "k1": pub}
	in, err = NewInfra(t.Context(), cfg, process.New("api", "test"), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close(t.Context())
	if in.AssertionVerifier == nil {
		t.Fatal("配了公钥时应构造验证器")
	}
	mfs, err := in.Metrics.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var kids []string
	for _, mf := range mfs {
		if mf.GetName() == "gateway_assertion_key_info" {
			for _, m := range mf.GetMetric() {
				kids = append(kids, m.GetLabel()[0].GetValue())
			}
		}
	}
	if len(kids) != 2 {
		t.Errorf("gateway_assertion_key_info kid = %v, want k1、k2", kids)
	}

	cfg.Gateway.AssertionPublicKeys = map[string]string{"k1": "AAAA"}
	if _, err := NewInfra(t.Context(), cfg, process.New("api", "test"), discardLogger()); err == nil ||
		!strings.Contains(err.Error(), "GATEWAY_ASSERTION_PUBLIC_KEYS") {
		t.Errorf("公钥不合法时 err = %v", err)
	}
}
