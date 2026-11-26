package domain_test

import (
	"context"
	"errors"
	"testing"

	"app.local/internal/kit/errs"
	"app.local/internal/kit/uow/uowtest"
	"app.local/internal/modules/user/domain"
	"app.local/internal/modules/user/domain/domaintest"
)

// countingSessions 记录 ListActiveByUser 的调用次数，可以让 GetValid、Get 返回注入的错误。
type countingSessions struct {
	*domaintest.SessionRepository
	listActive int
	getValid   error
	get        error
}

func (s *countingSessions) ListActiveByUser(ctx context.Context, userID string) ([]*domain.Session, error) {
	s.listActive++
	return s.SessionRepository.ListActiveByUser(ctx, userID)
}

func (s *countingSessions) GetValid(ctx context.Context, sid string) (*domain.Session, error) {
	if s.getValid != nil {
		return nil, s.getValid
	}
	return s.SessionRepository.GetValid(ctx, sid)
}

func (s *countingSessions) Get(ctx context.Context, sid string) (*domain.Session, error) {
	if s.get != nil {
		return nil, s.get
	}
	return s.SessionRepository.Get(ctx, sid)
}

func newPolicyService(policy domain.SessionPolicy) (*domain.Service, *domaintest.Fakes, *countingSessions) {
	fakes := domaintest.NewFakes()
	sessions := &countingSessions{SessionRepository: fakes.Sessions}
	deps := fakes.Deps()
	deps.Sessions = sessions
	deps.UoW = uowtest.Immediate{}
	deps.Issuer = domaintest.Issuer{}
	deps.Config = domaintest.TestConfig()
	deps.SessionPolicy = policy
	return domain.NewService(deps), fakes, sessions
}

func appLogin(class string) domain.LoginClient {
	return domain.LoginClient{Client: domain.ClientIOS, DeviceClass: class, IP: "127.0.0.1"}
}

func mustLogin(t *testing.T, svc *domain.Service, lc domain.LoginClient, subject string) *domain.IssuedTokens {
	t.Helper()
	tokens, err := svc.Login(t.Context(), lc, wechatIdentity(subject))
	if err != nil {
		t.Fatal(err)
	}
	return tokens
}

func sessionOf(t *testing.T, fakes *domaintest.Fakes, sid string) *domain.Session {
	t.Helper()
	v, err := fakes.Sessions.Get(t.Context(), sid)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestLogin_NoPolicyDoesNotListSessions(t *testing.T) {
	svc, fakes, sessions := newPolicyService(nil)
	first := mustLogin(t, svc, appLogin(domain.DeviceClassPhone), "np-1")
	mustLogin(t, svc, appLogin(domain.DeviceClassPhone), "np-1")
	if sessions.listActive != 0 {
		t.Errorf("没有策略时 ListActiveByUser 调用了 %d 次，want 0", sessions.listActive)
	}
	if sessionOf(t, fakes, first.Session.ID).RevokedAt != nil {
		t.Error("没有策略时不应顶掉任何会话")
	}
}

func TestSlotLimit_ReplacesSameSlotOnly(t *testing.T) {
	svc, fakes, _ := newPolicyService(domain.NewSlotLimit(1))
	phone := mustLogin(t, svc, appLogin(domain.DeviceClassPhone), "sl-1")
	tablet := mustLogin(t, svc, appLogin(domain.DeviceClassTablet), "sl-1")
	web := mustLogin(t, svc, domain.LoginClient{Client: domain.ClientWeb}, "sl-1")
	mini := mustLogin(t, svc, domain.LoginClient{Client: domain.ClientWeChatMiniprogram}, "sl-1")
	for _, tk := range []*domain.IssuedTokens{phone, tablet, web, mini} {
		if sessionOf(t, fakes, tk.Session.ID).RevokedAt != nil {
			t.Fatalf("不同类别之间不应互踢：%s 被吊销", tk.Session.Client+"/"+tk.Session.DeviceClass)
		}
	}

	// Android 手机和 iOS 手机是同一类（phone）。
	android := mustLogin(t, svc, domain.LoginClient{Client: domain.ClientAndroid, DeviceClass: domain.DeviceClassPhone}, "sl-1")
	old := sessionOf(t, fakes, phone.Session.ID)
	if old.RevokedAt == nil || !old.Replaced {
		t.Errorf("同类别的旧会话应被顶掉并标记 replaced：%+v", old)
	}
	if v := sessionOf(t, fakes, android.Session.ID); v.RevokedAt != nil {
		t.Error("新会话不应被吊销")
	}
	if sessionOf(t, fakes, tablet.Session.ID).RevokedAt != nil {
		t.Error("平板不应受手机登录影响")
	}
}

func TestSlotLimit_KeepsNewestWithinLimit(t *testing.T) {
	svc, fakes, _ := newPolicyService(domain.NewSlotLimit(2))
	a := mustLogin(t, svc, appLogin(domain.DeviceClassPhone), "sl-2")
	b := mustLogin(t, svc, appLogin(domain.DeviceClassPhone), "sl-2")
	if sessionOf(t, fakes, a.Session.ID).RevokedAt != nil {
		t.Fatal("没超过上限时不应顶掉")
	}
	mustLogin(t, svc, appLogin(domain.DeviceClassPhone), "sl-2")
	if v := sessionOf(t, fakes, a.Session.ID); v.RevokedAt == nil || !v.Replaced {
		t.Error("超过上限时应顶掉最久没用的")
	}
	if sessionOf(t, fakes, b.Session.ID).RevokedAt != nil {
		t.Error("较新的会话不应被顶掉")
	}
}

func TestAuthenticate_ReplacedInvalidAndQueryFailure(t *testing.T) {
	svc, _, sessions := newPolicyService(domain.NewSlotLimit(1))
	old := mustLogin(t, svc, appLogin(domain.DeviceClassPhone), "au-1")
	mustLogin(t, svc, appLogin(domain.DeviceClassPhone), "au-1")
	if _, err := svc.Authenticate(t.Context(), old.Session.ID); !errors.Is(err, domain.ErrSessionReplaced) {
		t.Errorf("被顶掉的会话 error = %v, want ErrSessionReplaced", err)
	}

	loggedOut := mustLogin(t, svc, domain.LoginClient{Client: domain.ClientWeb}, "au-2")
	if err := svc.Logout(t.Context(), loggedOut.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(t.Context(), loggedOut.Session.ID); !errors.Is(err, domain.ErrInvalidToken) {
		t.Errorf("登出的会话 error = %v, want ErrInvalidToken", err)
	}
	if _, err := svc.Authenticate(t.Context(), "no-such-sid"); !errors.Is(err, domain.ErrInvalidToken) {
		t.Errorf("不存在的会话 error = %v, want ErrInvalidToken", err)
	}

	// 查询失败不是"登录失效"：不能翻译成 401，否则数据库故障时客户端会清掉令牌。
	dbDown := errors.New("connection refused")
	sessions.getValid = dbDown
	if _, err := svc.Authenticate(t.Context(), old.Session.ID); !errors.Is(err, dbDown) || isUnauthorized(err) {
		t.Errorf("GetValid 失败 error = %v, want 原样返回的查询错误", err)
	}
	sessions.getValid, sessions.get = nil, dbDown
	if _, err := svc.Authenticate(t.Context(), old.Session.ID); !errors.Is(err, dbDown) || isUnauthorized(err) {
		t.Errorf("Get 失败 error = %v, want 原样返回的查询错误", err)
	}
}

func isUnauthorized(err error) bool {
	be, ok := errs.As(err)
	return ok && be.Kind == errs.KindUnauthorized
}

func TestRefresh_ReplacedSessionOnlyToldToHolder(t *testing.T) {
	svc, fakes, _ := newPolicyService(domain.NewSlotLimit(1))
	old := mustLogin(t, svc, appLogin(domain.DeviceClassPhone), "rp-1")
	rotated, err := svc.Refresh(t.Context(), old.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	mustLogin(t, svc, appLogin(domain.DeviceClassPhone), "rp-1")

	for name, token := range map[string]string{"当前值": rotated.RefreshToken, "上一代": old.RefreshToken} {
		if _, err := svc.Refresh(t.Context(), token); !errors.Is(err, domain.ErrSessionReplaced) {
			t.Errorf("%s error = %v, want ErrSessionReplaced", name, err)
		}
	}
	family := old.Session.RefreshFamily
	if _, err := svc.Refresh(t.Context(), family+".forged-random"); !errors.Is(err, domain.ErrInvalidToken) {
		t.Errorf("只知道 family error = %v, want ErrInvalidToken", err)
	}
	if v := sessionOf(t, fakes, old.Session.ID); !v.Replaced {
		t.Error("刷新不应改掉 replaced 标记")
	}
}

// policyFunc 把函数适配成 SessionPolicy。
type policyFunc func(ctx context.Context, c domain.SessionCandidate) (domain.Decide, error)

func (f policyFunc) Prepare(ctx context.Context, c domain.SessionCandidate) (domain.Decide, error) {
	return f(ctx, c)
}

func TestLogin_PolicyCannotRevokeForeignSessions(t *testing.T) {
	var victim string
	policy := policyFunc(func(context.Context, domain.SessionCandidate) (domain.Decide, error) {
		return func(domain.SessionCandidate, []domain.ActiveSession) ([]string, error) {
			return []string{victim}, nil
		}, nil
	})
	svc, fakes, _ := newPolicyService(policy)
	other := mustLogin(t, svc, appLogin(domain.DeviceClassPhone), "fg-victim")
	victim = other.Session.ID
	mine := mustLogin(t, svc, appLogin(domain.DeviceClassPhone), "fg-1") // 首次登录不调用策略

	before := len(fakes.Sessions.ListAll())
	if _, err := svc.Login(t.Context(), appLogin(domain.DeviceClassPhone), wechatIdentity("fg-1")); err == nil {
		t.Fatal("策略返回别人的会话时登录应失败")
	}
	if sessionOf(t, fakes, other.Session.ID).RevokedAt != nil || sessionOf(t, fakes, mine.Session.ID).RevokedAt != nil {
		t.Error("登录失败时不应吊销任何会话")
	}
	if after := len(fakes.Sessions.ListAll()); after != before {
		t.Errorf("登录失败时会话数从 %d 变成 %d", before, after)
	}
}

func TestLogin_PolicyCanRejectLogin(t *testing.T) {
	errTooMany := errs.New(errs.KindConflict, "too_many_devices", "登录的设备太多")
	policy := policyFunc(func(context.Context, domain.SessionCandidate) (domain.Decide, error) {
		return func(domain.SessionCandidate, []domain.ActiveSession) ([]string, error) { return nil, errTooMany }, nil
	})
	svc, fakes, _ := newPolicyService(policy)
	mustLogin(t, svc, appLogin(domain.DeviceClassPhone), "rj-1")
	before := len(fakes.Sessions.ListAll())
	if _, err := svc.Login(t.Context(), appLogin(domain.DeviceClassPhone), wechatIdentity("rj-1")); !errors.Is(err, errTooMany) {
		t.Errorf("error = %v, want 策略返回的错误", err)
	}
	if after := len(fakes.Sessions.ListAll()); after != before {
		t.Errorf("拒绝登录时不应建会话：%d → %d", before, after)
	}
}

// 准备策略之后、登录事务之前，登录方式换成了另一个账号：准备的数据不属于这个账号，登录失败让客户端重试。
func TestLogin_IdentityMovedAfterPrepareFails(t *testing.T) {
	var fakes *domaintest.Fakes
	policy := policyFunc(func(ctx context.Context, c domain.SessionCandidate) (domain.Decide, error) {
		idn, err := fakes.Identities.GetByProviderSubject(ctx, domain.ProviderWeChat, "mv-1")
		if err != nil {
			return nil, err
		}
		// 模拟并发：这个微信被解绑后又绑到了另一个账号上。
		if err := fakes.Identities.Delete(ctx, idn.ID); err != nil {
			return nil, err
		}
		if err := fakes.OpenIDs.DeleteByUser(ctx, idn.UserID); err != nil {
			return nil, err
		}
		other := domaintest.ValidUser()
		other.Status = domain.UserStatusActive
		if err := fakes.Users.Create(ctx, other); err != nil {
			return nil, err
		}
		moved := domaintest.ValidIdentity()
		moved.UserID, moved.Provider, moved.Subject = other.ID, domain.ProviderWeChat, "mv-1"
		if err := fakes.Identities.Create(ctx, moved); err != nil {
			return nil, err
		}
		return func(domain.SessionCandidate, []domain.ActiveSession) ([]string, error) { return nil, nil }, nil
	})
	svc, f, _ := newPolicyService(policy)
	fakes = f
	mustLogin(t, svc, appLogin(domain.DeviceClassPhone), "mv-1")
	if _, err := svc.Login(t.Context(), appLogin(domain.DeviceClassPhone), wechatIdentity("mv-1")); !errors.Is(err, domain.ErrLoginFailed) {
		t.Errorf("error = %v, want ErrLoginFailed", err)
	}
}

func TestSetSessionPolicy_OnlyOnce(t *testing.T) {
	svc, _, _ := newPolicyService(domain.NewSlotLimit(1))
	defer func() {
		if recover() == nil {
			t.Error("已有策略时再登记应 panic")
		}
	}()
	svc.SetSessionPolicy(domain.NewSlotLimit(2))
}

// staleUsers 的 GetByID 总是读到 active：模拟"先读状态、再加锁"之间账号被并发停用。
type staleUsers struct{ *domaintest.Repository }

func (u staleUsers) GetByID(ctx context.Context, id string) (*domain.User, error) {
	v, err := u.Repository.GetByID(ctx, id)
	if err == nil {
		v.Status = domain.UserStatusActive
	}
	return v, err
}

func TestLogin_ConcurrentlyDisabledAccountRejected(t *testing.T) {
	fakes := domaintest.NewFakes()
	deps := fakes.Deps()
	deps.Users = staleUsers{fakes.Users}
	deps.UoW = uowtest.Immediate{}
	deps.Issuer = domaintest.Issuer{}
	deps.Config = domaintest.TestConfig()
	svc := domain.NewService(deps)
	tokens := mustLogin(t, svc, domain.LoginClient{Client: domain.ClientWeb}, "cd-1")
	if _, err := fakes.Users.Disable(t.Context(), tokens.Session.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(t.Context(), domain.LoginClient{Client: domain.ClientWeb}, wechatIdentity("cd-1")); !errors.Is(err, domain.ErrAccountUnavailable) {
		t.Errorf("已停用账号登录 error = %v, want ErrAccountUnavailable", err)
	}
}

// 绑定、重新验证取当前会话时，查询失败也不能说成登录失效（401），否则数据库抖动会让客户端清掉令牌。
func TestBoundOperations_QueryFailureIsNotUnauthorized(t *testing.T) {
	svc, _, sessions := newPolicyService(nil)
	tokens := mustLogin(t, svc, domain.LoginClient{Client: domain.ClientWeb}, "qf-1")
	dbDown := errors.New("connection refused")
	sessions.getValid = dbDown
	if _, err := svc.BindIdentity(t.Context(), tokens.Session.ID, wechatIdentity("qf-2")); !errors.Is(err, dbDown) || isUnauthorized(err) {
		t.Errorf("BindIdentity error = %v, want 原样返回的查询错误", err)
	}
	if err := svc.Reauthenticate(t.Context(), tokens.Session.ID, wechatIdentity("qf-1")); !errors.Is(err, dbDown) || isUnauthorized(err) {
		t.Errorf("Reauthenticate error = %v, want 原样返回的查询错误", err)
	}
	sessions.getValid = nil
	if err := svc.Reauthenticate(t.Context(), "no-such-sid", wechatIdentity("qf-1")); !errors.Is(err, domain.ErrInvalidToken) {
		t.Errorf("会话不存在 error = %v, want ErrInvalidToken", err)
	}
}
