package ratelimit_test

import (
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"app.local/internal/platform/ratelimit"
	"app.local/internal/platform/redis"
)

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestRedisStore_SharesQuotaAcrossStoreInstances(t *testing.T) {
	mr := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	limit := ratelimit.Limit{Rate: 2, Period: time.Minute, Burst: 2}
	// 两个 store 模拟两个实例：额度必须共享，而不是各自 2 次。
	a := ratelimit.NewStore(client, "test", limit, discardLogger())
	b := ratelimit.NewStore(client, "test", limit, discardLogger())

	for i, s := range []interface{ Allow(string) (bool, error) }{a, b} {
		if ok, err := s.Allow("1.2.3.4"); err != nil || !ok {
			t.Fatalf("第 %d 次 Allow = %v, %v, want true", i+1, ok, err)
		}
	}
	if ok, _ := a.Allow("1.2.3.4"); ok {
		t.Error("第 3 次应该被限流")
	}
	if ok, _ := a.Allow("5.6.7.8"); !ok {
		t.Error("不同标识符的额度应该相互独立")
	}
}

func TestRedisStore_FailsOpenWhenRedisDown(t *testing.T) {
	mr := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	mr.Close()

	s := ratelimit.NewStore(client, "test", ratelimit.Limit{Rate: 1, Period: time.Minute, Burst: 1}, discardLogger())
	if ok, err := s.Allow("1.2.3.4"); err != nil || !ok {
		t.Errorf("Redis 不可用时 Allow = %v, %v, want true, nil", ok, err)
	}
}

func TestNewStore_WithoutRedis_UsesMemory(t *testing.T) {
	s := ratelimit.NewStore(nil, "test", ratelimit.Limit{Rate: 1, Period: time.Minute, Burst: 1}, discardLogger())
	if ok, _ := s.Allow("x"); !ok {
		t.Fatal("第 1 次应该放行")
	}
	if ok, _ := s.Allow("x"); ok {
		t.Error("第 2 次应该被限流")
	}
}

// 黑洞地址：接受连接但从不响应，模拟网络不通。限流必须很快放行，不能拖住请求。
func TestRedisStore_FailsOpenQuicklyWhenRedisHangs(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	client := redis.NewClient(ln.Addr().String(), "")
	t.Cleanup(func() { _ = client.Close() })

	s := ratelimit.NewStore(client, "test", ratelimit.Limit{Rate: 1, Period: time.Minute, Burst: 1}, discardLogger())
	start := time.Now()
	if ok, err := s.Allow("1.2.3.4"); err != nil || !ok {
		t.Errorf("Allow = %v, %v, want true, nil", ok, err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Redis 不响应时 Allow 耗时 %v，应该在限流查询超时后很快放行", elapsed)
	}
}

func TestPerSecond_FractionalRate(t *testing.T) {
	// 0.5 次/秒：第一次放行，紧接着的第二次拒绝，而不是全部拒绝。
	s := ratelimit.NewStore(nil, "test", ratelimit.PerSecond(0.5), discardLogger())
	if ok, _ := s.Allow("x"); !ok {
		t.Fatal("0.5 次/秒时第一次请求应该放行")
	}
	if ok, _ := s.Allow("x"); ok {
		t.Error("紧接着的第二次请求应该被限流")
	}

	l := ratelimit.PerSecond(1.5)
	if got := float64(l.Rate) / l.Period.Seconds(); got != 1.5 || l.Burst != 3 {
		t.Errorf("PerSecond(1.5) = %+v（%v 次/秒），want 1.5 次/秒、burst 3", l, got)
	}
}
