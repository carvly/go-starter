package domain_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"app.local/internal/kit/errs"
	"app.local/internal/kit/uow/uowtest"
	"app.local/internal/modules/staff/domain"
	"app.local/internal/modules/staff/domain/domaintest"
)

type fakePasswords struct{ compareCalls int }

func (f *fakePasswords) Hash(p string) (string, error) { return "hash:" + p, nil }
func (f *fakePasswords) Compare(h, p string) (bool, error) {
	f.compareCalls++
	return h == "hash:"+p, nil
}

var testOps = []domain.Operation{
	{Method: "POST", Route: "/admin/v1/auth/login", Permission: domain.PermissionPublic, Tag: "staff"},
	{Method: "GET", Route: "/admin/v1/me", Permission: domain.PermissionAuthenticated, Tag: "staff"},
	{Method: "GET", Route: "/admin/v1/accounts", Permission: domain.PermissionSuperAdmin, Tag: "staff"},
	{Method: "GET", Route: "/admin/v1/products", Permission: "product:read", Tag: "products"},
	{Method: "GET", Route: "/admin/v1/products/:id", Permission: "product:read", Tag: "products"},
	{Method: "PUT", Route: "/admin/v1/products/:id", Permission: "product:update", Tag: "products"},
	{Method: "GET", Route: "/admin/v1/orders", Permission: "order:read", Tag: "orders"},
}

type env struct {
	svc   *domain.Service
	repo  *domaintest.Repository
	pw    *fakePasswords
	clock time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{repo: domaintest.NewRepository(), pw: &fakePasswords{}, clock: time.Now()}
	e.svc = domain.NewService(e.repo, e.pw, uowtest.Immediate{},
		domain.Config{SessionIdleTimeout: 30 * time.Minute, SessionMaxAge: 12 * time.Hour, TempPasswordTTL: 24 * time.Hour},
		testOps, domain.WithClock(func() time.Time { return e.clock }))
	return e
}

// newActive 创建员工并把临时密码改成 password，返回员工。
func (e *env) newActive(t *testing.T, username, password string, superAdmin bool) *domain.Account {
	t.Helper()
	a, _, err := e.svc.CreateAccount(t.Context(), domain.CreateAccountInput{Username: username, SuperAdmin: superAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.repo.UpdatePassword(t.Context(), a.ID, "hash:"+password, false, nil, e.clock); err != nil {
		t.Fatal(err)
	}
	e.clock = e.clock.Add(time.Second)
	return a
}

func (e *env) login(t *testing.T, username, password, ip string) (string, error) {
	t.Helper()
	token, _, err := e.svc.Login(t.Context(), domain.LoginInput{Username: username, Password: password, IP: ip})
	return token, err
}

func TestLogin_SuccessCreatesSessionAndAudit(t *testing.T) {
	e := newEnv(t)
	a := e.newActive(t, "alice", "correct-horse-battery", false)
	token, err := e.login(t, " Alice ", "correct-horse-battery", "1.1.1.1")
	if err != nil || token == "" {
		t.Fatalf("Login = %q, %v", token, err)
	}
	p, err := e.svc.Authenticate(t.Context(), token)
	if err != nil || p.StaffID != a.ID {
		t.Fatalf("Authenticate = %+v, %v", p, err)
	}
	log := e.repo.Audit()
	if len(log) != 1 || log[0].Action != domain.ActionLoginSucceeded || log[0].StaffID != a.ID || log[0].IP != "1.1.1.1" {
		t.Errorf("登录成功只记一行日志: %+v", log)
	}
}

func TestLogin_FailuresAreIndistinguishable(t *testing.T) {
	e := newEnv(t)
	a := e.newActive(t, "alice", "correct-horse-battery", false)
	disabled := e.newActive(t, "bob", "correct-horse-battery", false)
	if err := e.svc.Disable(t.Context(), a.ID, disabled.ID); err != nil {
		t.Fatal(err)
	}

	for name, try := range map[string][2]string{
		"用户不存在": {"nobody", "whatever-password"},
		"密码错误":  {"alice", "wrong-password"},
		"账号停用":  {"bob", "correct-horse-battery"},
	} {
		before := e.pw.compareCalls
		if _, err := e.login(t, try[0], try[1], "1.1.1.1"); !errors.Is(err, domain.ErrInvalidCredentials) {
			t.Errorf("%s: error = %v, want ErrInvalidCredentials", name, err)
		}
		if e.pw.compareCalls != before+1 {
			t.Errorf("%s: 应该恰好做一次密码比较（不存在时是哑比较），got %d", name, e.pw.compareCalls-before)
		}
	}
	for _, entry := range e.repo.Audit() {
		if entry.Action == domain.ActionLoginFailed && entry.StaffID == "" && entry.Target != "" {
			t.Errorf("账号不存在时不应记录输入的用户名: %+v", entry)
		}
	}
}

// retryAfter 断言 err 是等待期错误，返回剩余时间。
func retryAfter(t *testing.T, err error) time.Duration {
	t.Helper()
	be, ok := errs.As(err)
	if !errors.Is(err, domain.ErrLoginThrottled) || !ok {
		t.Fatalf("error = %v, want ErrLoginThrottled", err)
	}
	return be.RetryAfter
}

func TestLogin_BackoffPerAccountAndIP(t *testing.T) {
	e := newEnv(t)
	e.newActive(t, "alice", "correct-horse-battery", false)
	for range 5 {
		if _, err := e.login(t, "alice", "wrong", "1.1.1.1"); !errors.Is(err, domain.ErrInvalidCredentials) {
			t.Fatalf("前 5 次输错只是密码错误: %v", err)
		}
	}
	before := e.pw.compareCalls
	_, err := e.login(t, "alice", "correct-horse-battery", "1.1.1.1")
	if wait := retryAfter(t, err); wait != 30*time.Second {
		t.Errorf("连续失败 5 次后，正确的密码也要等 30 秒，RetryAfter = %v", wait)
	}
	if e.pw.compareCalls != before+1 {
		t.Errorf("退避期内也要做一次（哑）比较，响应时间才不暴露账号是否存在")
	}
	e.clock = e.clock.Add(time.Second)
	if _, err := e.login(t, "alice", "correct-horse-battery", "2.2.2.2"); err != nil {
		t.Errorf("换一个 IP 不受这个 IP 的退避影响（只按账号锁会被用来把管理员锁在外面）: %v", err)
	}
	_, err = e.login(t, "alice", "correct-horse-battery", "1.1.1.1")
	if wait := retryAfter(t, err); wait != 29*time.Second {
		t.Errorf("另一个 IP 登录成功不能清掉这个 IP 的退避（否则攻击者能借合法登录反复重置），RetryAfter = %v, want 29s", wait)
	}
	e.clock = e.clock.Add(31 * time.Second)
	if _, err := e.login(t, "alice", "correct-horse-battery", "1.1.1.1"); err != nil {
		t.Errorf("等待期过后可以登录: %v", err)
	}

	e.clock = e.clock.Add(time.Second)
	start := e.clock
	for range 5 {
		_, _ = e.login(t, "alice", "wrong", "3.3.3.3")
	}
	e.clock = start.Add(31 * time.Second)
	if _, err := e.login(t, "alice", "wrong", "3.3.3.3"); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("失败 5 次后等 30 秒可以再试: error = %v", err)
	}
	e.clock = start.Add(31*time.Second + 59*time.Second)
	_, err = e.login(t, "alice", "correct-horse-battery", "3.3.3.3")
	if wait := retryAfter(t, err); wait != time.Second {
		t.Errorf("失败 6 次要等 60 秒（翻倍），59 秒时还剩 %v, want 1s", wait)
	}
	e.clock = start.Add(31*time.Second + 61*time.Second)
	if _, err := e.login(t, "alice", "correct-horse-battery", "3.3.3.3"); err != nil {
		t.Errorf("等待期过后可以登录: %v", err)
	}
	throttled := 0
	for _, entry := range e.repo.Audit() {
		if entry.Action == domain.ActionLoginThrottled {
			throttled++
		}
	}
	if throttled != 3 {
		t.Errorf("被退避拒绝的尝试也要记日志，got %d 条, want 3", throttled)
	}
}

// 等待期单独返回 ErrLoginThrottled，前提是不存在的用户名也一样计数、一样等待，否则
// "会不会进入等待"就暴露了账号是否存在。
func TestLogin_BackoffSameForUnknownUsername(t *testing.T) {
	e := newEnv(t)
	e.newActive(t, "alice", "correct-horse-battery", false)
	attempt := func(username string) []error {
		var got []error
		for range 6 {
			_, err := e.login(t, username, "wrong", "1.1.1.1")
			got = append(got, err)
		}
		return got
	}
	existing, unknown := attempt("alice"), attempt(" Nobody ")
	for i := range existing {
		be1, _ := errs.As(existing[i])
		be2, _ := errs.As(unknown[i])
		if be1.Code != be2.Code || be1.RetryAfter != be2.RetryAfter {
			t.Errorf("第 %d 次：存在的账号 %v，不存在的用户名 %v，必须相同", i+1, existing[i], unknown[i])
		}
	}
	retryAfter(t, unknown[5])
	if _, err := e.login(t, "nobody", "wrong", "1.1.1.1"); !errors.Is(err, domain.ErrLoginThrottled) {
		t.Errorf("用户名先规范化再计数（去空格、转小写）: %v", err)
	}
	for _, entry := range e.repo.Audit() {
		if entry.StaffID == "" && (entry.LoginKey == "" || strings.Contains(entry.LoginKey, "nobody")) {
			t.Errorf("账号不存在的登录日志要有用户名摘要、不能有原文: %+v", entry)
		}
	}
}

func TestLogin_TemporaryPassword(t *testing.T) {
	e := newEnv(t)
	a, temp, err := e.svc.CreateAccount(t.Context(), domain.CreateAccountInput{Username: "alice"})
	if err != nil || temp == "" || !a.MustChangePassword || a.TempPasswordExpiresAt == nil {
		t.Fatalf("CreateAccount = %+v, %q, %v", a, temp, err)
	}
	token, err := e.login(t, "alice", temp, "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := e.svc.Authenticate(t.Context(), token)
	if !p.MustChangePassword {
		t.Error("用临时密码登录后 MustChangePassword 应为 true")
	}

	e.clock = time.Now().Add(25 * time.Hour)
	if _, err := e.login(t, "alice", temp, "1.1.1.1"); !errors.Is(err, domain.ErrTemporaryPasswordExpired) {
		t.Errorf("临时密码 24 小时后过期: error = %v", err)
	}
	if _, err := e.login(t, "alice", "wrong", "1.1.1.1"); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Errorf("密码错误时不能泄露临时密码已过期: error = %v", err)
	}
}

func TestChangePassword(t *testing.T) {
	e := newEnv(t)
	_, temp, _ := e.svc.CreateAccount(t.Context(), domain.CreateAccountInput{Username: "alice"})
	t1, _ := e.login(t, "alice", temp, "1.1.1.1")
	t2, _ := e.login(t, "alice", temp, "1.1.1.1")
	p, _ := e.svc.Authenticate(t.Context(), t1)

	if err := e.svc.ChangePassword(t.Context(), p, "wrong", "a-new-long-password"); !errors.Is(err, domain.ErrWrongPassword) {
		t.Errorf("当前密码错误 error = %v", err)
	}
	if err := e.svc.ChangePassword(t.Context(), p, temp, "short"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("新密码太短 error = %v", err)
	}
	if err := e.svc.ChangePassword(t.Context(), p, temp, "a-new-long-password"); err != nil {
		t.Fatal(err)
	}
	if p, err := e.svc.Authenticate(t.Context(), t1); err != nil || p.MustChangePassword {
		t.Errorf("当前会话保留且不再要求改密: %+v, %v", p, err)
	}
	if _, err := e.svc.Authenticate(t.Context(), t2); !errors.Is(err, domain.ErrSessionInvalid) {
		t.Errorf("改密后其它会话应被吊销: %v", err)
	}
}

func TestAccountManagement_CannotLockSelfOut(t *testing.T) {
	e := newEnv(t)
	root := e.newActive(t, "root", "correct-horse-battery", true)
	if err := e.svc.Disable(t.Context(), root.ID, root.ID); !errors.Is(err, domain.ErrCannotModifySelf) {
		t.Errorf("停用自己 error = %v", err)
	}
	if err := e.svc.SetSuperAdmin(t.Context(), root.ID, root.ID, false); !errors.Is(err, domain.ErrCannotModifySelf) {
		t.Errorf("取消自己的超级管理员 error = %v", err)
	}
	if _, _, err := e.svc.CreateAccount(t.Context(), domain.CreateAccountInput{Username: "Root"}); !errors.Is(err, domain.ErrUsernameTaken) {
		t.Errorf("用户名大小写不敏感 error = %v", err)
	}
	for _, bad := range []string{"", "a", "has space", "中文名"} {
		if _, _, err := e.svc.CreateAccount(t.Context(), domain.CreateAccountInput{Username: bad}); !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("用户名 %q error = %v", bad, err)
		}
	}
}

func TestDisableAndResetRevokeSessions(t *testing.T) {
	e := newEnv(t)
	root := e.newActive(t, "root", "correct-horse-battery", true)
	alice := e.newActive(t, "alice", "correct-horse-battery", false)
	token, _ := e.login(t, "alice", "correct-horse-battery", "1.1.1.1")

	if err := e.svc.Disable(t.Context(), root.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Authenticate(t.Context(), token); !errors.Is(err, domain.ErrSessionInvalid) {
		t.Errorf("停用后旧会话立即失效: %v", err)
	}
	if err := e.svc.Enable(t.Context(), alice.ID); err != nil {
		t.Fatal(err)
	}
	token, _ = e.login(t, "alice", "correct-horse-battery", "1.1.1.1")
	temp, expires, err := e.svc.ResetPassword(t.Context(), alice.ID)
	if err != nil || temp == "" || !expires.Equal(e.clock.Add(24*time.Hour)) {
		t.Fatal(err)
	}
	if _, err := e.svc.Authenticate(t.Context(), token); !errors.Is(err, domain.ErrSessionInvalid) {
		t.Errorf("重设密码后旧会话失效: %v", err)
	}
	if _, err := e.login(t, "alice", "correct-horse-battery", "1.1.1.1"); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Errorf("旧密码不再可用: %v", err)
	}
	if _, _, err := e.svc.ResetPassword(t.Context(), "01936f3c-2b3c-7d4e-8f5a-6b7c8d9e0f1a"); !errors.Is(err, domain.ErrAccountNotFound) {
		t.Errorf("重设不存在的账号 error = %v", err)
	}
}

func TestAuthorizeAndPermissions(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	alice := e.newActive(t, "alice", "correct-horse-battery", false)
	role, _ := e.svc.CreateRole(ctx, "editor", "")
	if _, err := e.svc.SetRolePermissions(ctx, role.ID, []string{"product:read", "made:up"}); !errors.Is(err, domain.ErrUnknownPermission) {
		t.Errorf("admin.yaml 里没有的权限码 error = %v", err)
	}
	if _, err := e.svc.SetRolePermissions(ctx, role.ID, []string{"product:read"}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.BindRoles(ctx, alice.ID, []string{role.ID}); err != nil {
		t.Fatal(err)
	}
	p := &domain.Principal{StaffID: alice.ID}
	super := &domain.Principal{StaffID: "x", SuperAdmin: true}

	for _, tt := range []struct {
		p    *domain.Principal
		perm string
		ok   bool
	}{
		{p, domain.PermissionAuthenticated, true},
		{p, "product:read", true},
		{p, "product:update", false},
		{p, domain.PermissionSuperAdmin, false},
		{p, "", false},
		{super, domain.PermissionSuperAdmin, true},
		{super, "order:read", true},
	} {
		err := e.svc.Authorize(ctx, tt.p, tt.perm)
		if (err == nil) != tt.ok || (err != nil && !errors.Is(err, domain.ErrPermissionDenied)) {
			t.Errorf("Authorize(super=%v, %q) = %v, want ok=%v", tt.p.SuperAdmin, tt.perm, err, tt.ok)
		}
	}

	if perms, _ := e.svc.Permissions(ctx, p); !slices.Equal(perms, []string{"product:read"}) {
		t.Errorf("Permissions = %v", perms)
	}
	if perms, _ := e.svc.Permissions(ctx, super); !slices.Equal(perms, []string{"order:read", "product:read", "product:update"}) {
		t.Errorf("超级管理员的 Permissions = %v, want 全部权限码", perms)
	}
	// spec 里删掉的权限码（直接写进库模拟历史数据）不再生效、不再返回。
	_ = e.repo.SetRolePermissions(ctx, role.ID, []string{"product:read", "legacy:thing"})
	if perms, _ := e.svc.Permissions(ctx, p); !slices.Equal(perms, []string{"product:read"}) {
		t.Errorf("已失效的权限码不应返回: %v", perms)
	}
	if err := e.svc.Authorize(ctx, p, "legacy:thing"); !errors.Is(err, domain.ErrPermissionDenied) {
		t.Errorf("已失效的权限码不能用于 Authorize（路由映射里也不会有它）: %v", err)
	}

	cat := e.svc.Catalog()
	if len(cat) != 3 || cat[0].Tag != "orders" || cat[0].Code != "order:read" {
		t.Errorf("Catalog = %+v, want 按 tag、权限码排序且不含保留值", cat)
	}
	if err := e.svc.BindRoles(ctx, alice.ID, []string{role.ID, "01936f3c-2b3c-7d4e-8f5a-6b7c8d9e0f1a"}); !errors.Is(err, domain.ErrRoleNotFound) {
		t.Errorf("绑定不存在的角色 error = %v", err)
	}
}

func TestResetPasswordClearsBackoff(t *testing.T) {
	e := newEnv(t)
	alice := e.newActive(t, "alice", "correct-horse-battery", false)
	for range 5 {
		_, _ = e.login(t, "alice", "wrong", "1.1.1.1")
	}
	e.clock = e.clock.Add(time.Second)
	temp, _, err := e.svc.RecoverAccount(t.Context(), alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.login(t, "alice", temp, "1.1.1.1"); err != nil {
		t.Errorf("重设密码之后，之前的失败不再计入退避: %v", err)
	}
}

func TestRecoverAccountEnablesAndResets(t *testing.T) {
	e := newEnv(t)
	root := e.newActive(t, "root", "correct-horse-battery", true)
	alice := e.newActive(t, "alice", "correct-horse-battery", true)
	if err := e.svc.Disable(t.Context(), root.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	temp, _, err := e.svc.RecoverAccount(t.Context(), alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.GetAccount(t.Context(), alice.ID); got.Status != domain.StatusActive || !got.MustChangePassword {
		t.Errorf("恢复后 = %+v, want active 且必须改密", got)
	}
	if _, err := e.login(t, "alice", temp, "1.1.1.1"); err != nil {
		t.Errorf("恢复后用临时密码登录: %v", err)
	}
}

func TestKeepsAtLeastOneActiveSuperAdmin(t *testing.T) {
	e := newEnv(t)
	root := e.newActive(t, "root", "correct-horse-battery", true)
	other := e.newActive(t, "other", "correct-horse-battery", true)
	// 模拟并发：操作人自己的超级管理员身份已经被另一个请求停掉，target 是剩下的最后一个。
	if err := e.svc.Disable(t.Context(), root.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Disable(t.Context(), other.ID, root.ID); !errors.Is(err, domain.ErrLastSuperAdmin) {
		t.Errorf("停用最后一个 active 超级管理员 error = %v, want ErrLastSuperAdmin", err)
	}
	if err := e.svc.SetSuperAdmin(t.Context(), other.ID, root.ID, false); !errors.Is(err, domain.ErrLastSuperAdmin) {
		t.Errorf("取消最后一个超级管理员的标记 error = %v, want ErrLastSuperAdmin", err)
	}
	alice := e.newActive(t, "alice", "correct-horse-battery", false)
	if err := e.svc.Disable(t.Context(), root.ID, alice.ID); err != nil {
		t.Errorf("停用普通员工不受影响: %v", err)
	}
}

func TestCatalog_GroupsOperationsByCode(t *testing.T) {
	ops := []domain.Operation{
		{Method: "GET", Route: "/admin/v1/me", Permission: domain.PermissionAuthenticated, Tag: "staff", Summary: "当前员工", TagSummary: "员工"},
		{Method: "GET", Route: "/admin/v1/products", Permission: "product:read", Tag: "products", Summary: "商品列表", TagSummary: "商品"},
		{Method: "GET", Route: "/admin/v1/products/:id", Permission: "product:read", Tag: "products", Summary: "商品详情", TagSummary: "商品"},
		{Method: "PUT", Route: "/admin/v1/products/:id", Permission: "product:update", Tag: "products", Summary: "修改商品", TagSummary: "商品"},
	}
	svc := domain.NewService(domaintest.NewRepository(), &fakePasswords{}, uowtest.Immediate{}, domain.Config{}, ops)
	got := svc.Catalog()
	if len(got) != 2 {
		t.Fatalf("Catalog = %+v, want 2 个权限码（保留值不算）", got)
	}
	if got[0].Code != "product:read" || got[0].TagSummary != "商品" || !slices.Equal(got[0].Operations, []string{"商品列表", "商品详情"}) {
		t.Errorf("product:read = %+v", got[0])
	}
	got[0].Operations[0] = "被改了"
	if svc.Catalog()[0].Operations[0] != "商品列表" {
		t.Error("Catalog 返回的切片被调用方修改后影响了 Service 内部状态")
	}
}
