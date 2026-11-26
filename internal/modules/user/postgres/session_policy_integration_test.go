//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"app.local/internal/modules/user"
	"app.local/internal/modules/user/domain"
	"app.local/internal/modules/user/domain/domaintest"
	"app.local/internal/modules/user/postgres"
	"app.local/internal/platform/dbtx"
	"app.local/internal/platform/postgres/pgtest"
)

// newPostgresService 用真实的 Postgres 仓储组装 Service（事务也是真的），policy 为 nil 时不启用会话策略。
func newPostgresService(handle *dbtx.Handle, policy domain.SessionPolicy) *domain.Service {
	cfg := domaintest.TestConfig()
	return domain.NewService(domain.Deps{
		Users:         postgres.NewRepository(handle),
		Identities:    postgres.NewIdentityRepository(handle),
		OpenIDs:       postgres.NewWeChatOpenIDRepository(handle),
		Sessions:      postgres.NewSessionRepository(handle, cfg.SessionIdleTimeout),
		States:        postgres.NewOAuthStateRepository(handle),
		Publisher:     &domaintest.Publisher{},
		Issuer:        domaintest.Issuer{},
		UoW:           handle,
		Config:        cfg,
		SessionPolicy: policy,
	})
}

// 登录先拿到用户行锁、停用后到：停用等登录提交后，必须连这次登录刚建的会话一起吊销。停用的吊销如果和改状态写在同一条
// 语句里（数据修改的 CTE），用的是语句开始时的快照，会漏掉这个会话，账号再启用后它就复活了。
func TestService_DisableAfterConcurrentLoginRevokesItsSession(t *testing.T) {
	handle := pgtest.Handle(t, user.Migrations)
	svc := newPostgresService(handle, nil)
	sessions := postgres.NewSessionRepository(handle, domaintest.TestConfig().SessionIdleTimeout)
	ext := domain.ExternalIdentity{Provider: domain.ProviderWeChat, Subject: "disable-race-1", WeChatAppID: "app-appid", WeChatOpenID: "openid-disable-race-1"}
	lc := domain.LoginClient{Client: domain.ClientWeb, IP: "127.0.0.1"}
	first, err := svc.Login(t.Context(), lc, ext)
	if err != nil {
		t.Fatal(err)
	}

	var second *domain.IssuedTokens
	disabled := make(chan error, 1)
	// 外层事务让登录停在提交之前：登录已锁住用户行，停用在等它。
	err = handle.Run(t.Context(), func(ctx context.Context) error {
		var err error
		if second, err = svc.Login(ctx, lc, ext); err != nil {
			return err
		}
		go func() {
			_, err := svc.Disable(context.Background(), first.Session.UserID)
			disabled <- err
		}()
		waitForLockWait(t, handle)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-disabled; err != nil {
		t.Fatal(err)
	}
	got, err := sessions.Get(t.Context(), second.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RevokedAt == nil {
		t.Error("停用后，和它并发、先提交的那次登录建的会话没有被吊销")
	}
}

// waitForLockWait 等到有别的连接在等锁（停用在等登录事务的用户行锁）。
func waitForLockWait(t *testing.T, handle *dbtx.Handle) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		// 用独立连接查询，不走外层事务。
		if err := handle.Resolve(context.Background()).QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND datname = current_database()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("停用没有在等登录的用户行锁")
}

// 同一账号同一类设备并发登录：登录事务里 TouchLogin 的用户行锁让它们排队，后一个看得到前一个的会话并把它顶掉，
// 结束时这一类只剩一个有效会话。各次登录用同一账号的不同登录方式，排除登录方式行锁的干扰：去掉用户行锁，
// 各事务看不到彼此的新会话，会留下多个。
func TestLogin_ConcurrentLoginsRespectSlotLimit(t *testing.T) {
	handle := pgtest.Handle(t, user.Migrations)
	svc := newPostgresService(handle, domain.NewSlotLimit(1))
	sessions := postgres.NewSessionRepository(handle, domaintest.TestConfig().SessionIdleTimeout)
	identities := postgres.NewIdentityRepository(handle)
	ext := domain.ExternalIdentity{Provider: domain.ProviderWeChat, Subject: "concurrent-1", WeChatAppID: "app-appid", WeChatOpenID: "openid-concurrent-1"}
	lc := domain.LoginClient{Client: domain.ClientAndroid, DeviceClass: domain.DeviceClassPhone, IP: "127.0.0.1"}
	first, err := svc.Login(t.Context(), lc, ext)
	if err != nil {
		t.Fatal(err)
	}

	const n = 10
	for i := range n {
		idn := domaintest.ValidIdentity()
		idn.UserID, idn.Provider, idn.Subject = first.Session.UserID, fmt.Sprintf("test-provider-%d", i), "concurrent-1"
		if err := identities.Create(t.Context(), idn); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Go(func() {
			other := domain.ExternalIdentity{Provider: fmt.Sprintf("test-provider-%d", i), Subject: "concurrent-1"}
			if _, err := svc.Login(t.Context(), lc, other); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("并发登录失败：%v", err)
	}

	active, err := sessions.ListActiveByUser(t.Context(), first.Session.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 {
		t.Errorf("phone 类别的有效会话 = %d 个，want 1", len(active))
	}
	old, err := sessions.Get(t.Context(), first.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.RevokedAt == nil || !old.Replaced {
		t.Errorf("第一次登录的会话应被顶掉：%+v", old)
	}
}
