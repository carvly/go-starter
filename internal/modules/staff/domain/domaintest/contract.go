package domaintest

import (
	"errors"
	"slices"
	"testing"
	"time"
	"uuid"

	"app.local/internal/modules/staff/domain"
)

// RunRepositoryContract 是 domain.Repository 的行为契约，内存 fake 和 Postgres
// 实现跑同一套用例。newRepo 每次返回一个空仓库。
func RunRepositoryContract(t *testing.T, newRepo func(t *testing.T) domain.Repository) {
	ctx := t.Context()
	newID := func() string { return uuid.NewV7().String() }
	account := func(name string) *domain.Account {
		return &domain.Account{ID: newID(), Username: name, DisplayName: name, PasswordHash: "h", Status: domain.StatusActive,
			PasswordChangedAt: time.Now().Truncate(time.Microsecond)}
	}
	mustCreate := func(t *testing.T, repo domain.Repository, a *domain.Account) *domain.Account {
		t.Helper()
		if err := repo.CreateAccount(ctx, a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	newSession := func(t *testing.T, repo domain.Repository, staffID, tokenHash string, expiresIn time.Duration) *domain.Session {
		t.Helper()
		s := &domain.Session{ID: newID(), StaffID: staffID, ExpiresAt: time.Now().Add(expiresIn), IP: "10.0.0.1", UserAgent: "test"}
		if err := repo.CreateSession(ctx, s, tokenHash); err != nil {
			t.Fatal(err)
		}
		return s
	}

	t.Run("账号：创建、读取、用户名冲突、不存在", func(t *testing.T) {
		repo := newRepo(t)
		a := account("alice")
		a.CreatedBy = newID()
		a.TempPasswordExpiresAt = new(time.Now().Add(time.Hour).Truncate(time.Microsecond))
		a.MustChangePassword = true
		mustCreate(t, repo, a)

		got, err := repo.GetAccountByUsername(ctx, "alice")
		if err != nil || got.ID != a.ID || got.CreatedBy != a.CreatedBy || !got.MustChangePassword ||
			got.TempPasswordExpiresAt == nil || !got.TempPasswordExpiresAt.Equal(*a.TempPasswordExpiresAt) || got.Status != domain.StatusActive {
			t.Errorf("GetAccountByUsername = %+v, %v", got, err)
		}
		if got, err := repo.GetAccount(ctx, a.ID); err != nil || got.Username != "alice" || got.CreatedAt.IsZero() || !got.PasswordChangedAt.Equal(a.PasswordChangedAt) {
			t.Errorf("GetAccount = %+v, %v", got, err)
		}
		if err := repo.CreateAccount(ctx, account("alice")); !errors.Is(err, domain.ErrUsernameTaken) {
			t.Errorf("重复用户名 error = %v, want ErrUsernameTaken", err)
		}
		if _, err := repo.GetAccount(ctx, newID()); !errors.Is(err, domain.ErrAccountNotFound) {
			t.Errorf("GetAccount 不存在 error = %v", err)
		}
		if _, err := repo.GetAccountByUsername(ctx, "nobody"); !errors.Is(err, domain.ErrAccountNotFound) {
			t.Errorf("GetAccountByUsername 不存在 error = %v", err)
		}
		cli := mustCreate(t, repo, account("root"))
		if got, _ := repo.GetAccount(ctx, cli.ID); got.CreatedBy != "" || got.TempPasswordExpiresAt != nil {
			t.Errorf("CLI 创建的账号 CreatedBy/TempPasswordExpiresAt 应为空: %+v", got)
		}
	})

	t.Run("账号：分页按创建时间倒序", func(t *testing.T) {
		repo := newRepo(t)
		var ids []string
		for _, n := range []string{"a1", "a2", "a3"} {
			ids = append(ids, mustCreate(t, repo, account(n)).ID)
			time.Sleep(2 * time.Millisecond)
		}
		page1, next, err := repo.ListAccounts(ctx, "", 2)
		if err != nil || len(page1) != 2 || page1[0].ID != ids[2] || page1[1].ID != ids[1] || next == "" {
			t.Fatalf("第一页 = %v, next=%q, err=%v", page1, next, err)
		}
		page2, next2, err := repo.ListAccounts(ctx, next, 2)
		if err != nil || len(page2) != 1 || page2[0].ID != ids[0] || next2 != "" {
			t.Errorf("第二页 = %v, next=%q, err=%v", page2, next2, err)
		}
		if _, _, err := repo.ListAccounts(ctx, "not-a-cursor", 2); !errors.Is(err, domain.ErrInvalidCursor) {
			t.Errorf("非法 cursor error = %v", err)
		}
	})

	t.Run("账号：改密码、状态、超级管理员标记", func(t *testing.T) {
		repo := newRepo(t)
		a := mustCreate(t, repo, account("alice"))
		exp := time.Now().Add(time.Hour).Truncate(time.Microsecond)
		if err := repo.UpdatePassword(ctx, a.ID, "h2", true, &exp, exp.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetStatus(ctx, a.ID, domain.StatusDisabled); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetSuperAdmin(ctx, a.ID, true); err != nil {
			t.Fatal(err)
		}
		got, _ := repo.GetAccount(ctx, a.ID)
		if got.PasswordHash != "h2" || !got.MustChangePassword || got.TempPasswordExpiresAt == nil || !got.TempPasswordExpiresAt.Equal(exp) ||
			!got.PasswordChangedAt.Equal(exp.Add(-time.Hour)) ||
			got.Status != domain.StatusDisabled || !got.SuperAdmin {
			t.Errorf("更新后 = %+v", got)
		}
		if err := repo.UpdatePassword(ctx, a.ID, "h3", false, nil, time.Now()); err != nil {
			t.Fatal(err)
		}
		if got, _ := repo.GetAccount(ctx, a.ID); got.MustChangePassword || got.TempPasswordExpiresAt != nil {
			t.Errorf("清除临时密码后 = %+v", got)
		}
		for name, err := range map[string]error{
			"UpdatePassword": repo.UpdatePassword(ctx, newID(), "h", false, nil, time.Now()),
			"LockAccount":    repo.LockAccount(ctx, newID()),
			"SetStatus":      repo.SetStatus(ctx, newID(), domain.StatusActive),
			"SetSuperAdmin":  repo.SetSuperAdmin(ctx, newID(), true),
		} {
			if !errors.Is(err, domain.ErrAccountNotFound) {
				t.Errorf("%s 不存在的账号 error = %v", name, err)
			}
		}
	})

	t.Run("锁：账号行、active 超级管理员", func(t *testing.T) {
		repo := newRepo(t)
		a := mustCreate(t, repo, account("alice"))
		if err := repo.LockAccount(ctx, a.ID); err != nil {
			t.Errorf("LockAccount = %v", err)
		}
		r1, r2, r3 := account("root1"), account("root2"), account("root3")
		r1.SuperAdmin, r2.SuperAdmin, r3.SuperAdmin = true, true, true
		for _, r := range []*domain.Account{r1, r2, r3} {
			mustCreate(t, repo, r)
		}
		if err := repo.SetStatus(ctx, r3.ID, domain.StatusDisabled); err != nil {
			t.Fatal(err)
		}
		ids, err := repo.LockActiveSuperAdmins(ctx)
		want := []string{r1.ID, r2.ID}
		slices.Sort(want)
		if err != nil || !slices.Equal(ids, want) {
			t.Errorf("LockActiveSuperAdmins = %v, %v, want 只含 active 的超级管理员 %v", ids, err, want)
		}
		if n, err := repo.CountActiveSuperAdmins(ctx); err != nil || n != 2 {
			t.Errorf("CountActiveSuperAdmins = %d, %v, want 2", n, err)
		}
	})

	t.Run("会话：校验通过、吊销、过期、空闲超时、账号停用", func(t *testing.T) {
		repo := newRepo(t)
		a := account("alice")
		a.SuperAdmin, a.MustChangePassword = true, true
		mustCreate(t, repo, a)
		s := newSession(t, repo, a.ID, "tok", time.Hour)

		p, err := repo.Authenticate(ctx, "tok", time.Hour)
		if err != nil || p.SessionID != s.ID || p.StaffID != a.ID || !p.SuperAdmin || !p.MustChangePassword || p.LastUsedAt.IsZero() {
			t.Fatalf("Authenticate = %+v, %v", p, err)
		}
		if _, err := repo.Authenticate(ctx, "tok", 0); !errors.Is(err, domain.ErrSessionInvalid) {
			t.Errorf("空闲超时 error = %v", err)
		}
		if _, err := repo.Authenticate(ctx, "nope", time.Hour); !errors.Is(err, domain.ErrSessionInvalid) {
			t.Errorf("不存在的 token error = %v", err)
		}
		newSession(t, repo, a.ID, "expired", -time.Minute)
		if _, err := repo.Authenticate(ctx, "expired", time.Hour); !errors.Is(err, domain.ErrSessionInvalid) {
			t.Errorf("已过期 error = %v", err)
		}
		if err := repo.TouchSession(ctx, s.ID); err != nil {
			t.Errorf("TouchSession error = %v", err)
		}
		if err := repo.SetStatus(ctx, a.ID, domain.StatusDisabled); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Authenticate(ctx, "tok", time.Hour); !errors.Is(err, domain.ErrSessionInvalid) {
			t.Errorf("账号停用后 error = %v", err)
		}
	})

	t.Run("会话：只能吊销自己的，批量吊销可保留当前会话", func(t *testing.T) {
		repo := newRepo(t)
		alice := mustCreate(t, repo, account("alice"))
		bob := mustCreate(t, repo, account("bob"))
		s1 := newSession(t, repo, alice.ID, "a1", time.Hour)
		s2 := newSession(t, repo, alice.ID, "a2", time.Hour)
		s3 := newSession(t, repo, alice.ID, "a3", time.Hour)
		b1 := newSession(t, repo, bob.ID, "b1", time.Hour)

		if err := repo.RevokeSession(ctx, alice.ID, b1.ID); !errors.Is(err, domain.ErrSessionNotFound) {
			t.Errorf("吊销别人的会话 error = %v, want ErrSessionNotFound", err)
		}
		if err := repo.RevokeSession(ctx, alice.ID, s1.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Authenticate(ctx, "a1", time.Hour); !errors.Is(err, domain.ErrSessionInvalid) {
			t.Errorf("吊销后 error = %v", err)
		}
		if err := repo.RevokeSessions(ctx, alice.ID, s2.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Authenticate(ctx, "a2", time.Hour); err != nil {
			t.Errorf("保留的会话应该仍然有效: %v", err)
		}
		if _, err := repo.Authenticate(ctx, "a3", time.Hour); !errors.Is(err, domain.ErrSessionInvalid) {
			t.Errorf("批量吊销后 error = %v", err)
		}
		if _, err := repo.Authenticate(ctx, "b1", time.Hour); err != nil {
			t.Errorf("别人的会话不受影响: %v", err)
		}
		list, err := repo.ListSessions(ctx, alice.ID, 10)
		if err != nil || len(list) != 3 {
			t.Fatalf("ListSessions = %d, %v", len(list), err)
		}
		revoked := 0
		for _, s := range list {
			if s.RevokedAt != nil {
				revoked++
			}
			if s.ID == s3.ID && s.RevokedAt == nil {
				t.Error("s3 应已吊销")
			}
		}
		if revoked != 2 {
			t.Errorf("已吊销会话数 = %d, want 2（吊销只标记，不删行）", revoked)
		}
	})

	t.Run("角色：增删改、权限替换、绑定与并集", func(t *testing.T) {
		repo := newRepo(t)
		alice := mustCreate(t, repo, account("alice"))
		editor := &domain.Role{ID: newID(), Name: "editor", Description: "编辑"}
		viewer := &domain.Role{ID: newID(), Name: "viewer"}
		for _, r := range []*domain.Role{editor, viewer} {
			if err := repo.CreateRole(ctx, r); err != nil {
				t.Fatal(err)
			}
		}
		if err := repo.CreateRole(ctx, &domain.Role{ID: newID(), Name: "editor"}); !errors.Is(err, domain.ErrRoleNameTaken) {
			t.Errorf("重复角色名 error = %v", err)
		}
		if err := repo.UpdateRole(ctx, &domain.Role{ID: viewer.ID, Name: "editor"}); !errors.Is(err, domain.ErrRoleNameTaken) {
			t.Errorf("改成已有的角色名 error = %v", err)
		}
		if err := repo.UpdateRole(ctx, &domain.Role{ID: newID(), Name: "x"}); !errors.Is(err, domain.ErrRoleNotFound) {
			t.Errorf("改不存在的角色 error = %v", err)
		}
		if err := repo.SetRolePermissions(ctx, editor.ID, []string{"product:update", "product:read"}); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetRolePermissions(ctx, editor.ID, []string{"product:read", "order:read"}); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetRolePermissions(ctx, viewer.ID, []string{"product:read"}); err != nil {
			t.Fatal(err)
		}
		if got, _ := repo.GetRole(ctx, editor.ID); !slices.Equal(got.Permissions, []string{"order:read", "product:read"}) || got.Description != "编辑" {
			t.Errorf("替换后的权限 = %+v", got)
		}
		n, err := repo.SetAccountRoles(ctx, alice.ID, []string{editor.ID, viewer.ID, newID()})
		if err != nil || n != 2 {
			t.Errorf("SetAccountRoles = %d, %v, want 2（不存在的角色不绑定）", n, err)
		}
		if perms, _ := repo.Permissions(ctx, alice.ID); !slices.Equal(perms, []string{"order:read", "product:read"}) {
			t.Errorf("Permissions = %v, want 去重后的并集", perms)
		}
		if ok, _ := repo.HasPermission(ctx, alice.ID, "order:read"); !ok {
			t.Error("HasPermission(order:read) = false")
		}
		if ok, _ := repo.HasPermission(ctx, alice.ID, "product:update"); ok {
			t.Error("被替换掉的权限仍然有效")
		}
		if err := repo.DeleteRole(ctx, editor.ID); err != nil {
			t.Fatal(err)
		}
		// 批量查询：没有角色的员工不出现在结果里。
		bound, err := repo.AccountRoleIDs(ctx, []string{alice.ID, uuid.NewV7().String()})
		if err != nil || len(bound) != 1 || !slices.Equal(bound[alice.ID], []string{viewer.ID}) {
			t.Errorf("删除角色后的绑定 = %v, %v", bound, err)
		}
		if ok, _ := repo.HasPermission(ctx, alice.ID, "order:read"); ok {
			t.Error("删除角色后它的权限仍然有效")
		}
		if err := repo.DeleteRole(ctx, editor.ID); !errors.Is(err, domain.ErrRoleNotFound) {
			t.Errorf("重复删除 error = %v", err)
		}
		roles, _ := repo.ListRoles(ctx)
		if len(roles) != 1 || roles[0].ID != viewer.ID || !slices.Equal(roles[0].Permissions, []string{"product:read"}) {
			t.Errorf("ListRoles = %+v", roles)
		}
		if _, err := repo.SetAccountRoles(ctx, alice.ID, nil); err != nil {
			t.Fatal(err)
		}
		if perms, _ := repo.Permissions(ctx, alice.ID); len(perms) != 0 {
			t.Errorf("清空角色后 Permissions = %v", perms)
		}
	})

	t.Run("操作日志：筛选、分页、登录失败计数、按保留期删除", func(t *testing.T) {
		repo := newRepo(t)
		alice, bob := newID(), newID()
		// 登录日志按用户名摘要计数；nobody 是不存在的账号（staff_id 为空），一样计数。
		const aliceKey, bobKey, nobodyKey = "key-alice", "key-bob", "key-nobody"
		base := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
		rec := func(staffID, key, action, ip string, at time.Duration) {
			t.Helper()
			if err := repo.RecordAudit(ctx, &domain.AuditEntry{ID: newID(), StaffID: staffID, LoginKey: key, Action: action, IP: ip, Result: "x", CreatedAt: base.Add(at)}); err != nil {
				t.Fatal(err)
			}
		}
		rec(alice, aliceKey, domain.ActionLoginFailed, "1.1.1.1", 1*time.Minute)
		rec(alice, aliceKey, domain.ActionLoginSucceeded, "1.1.1.1", 2*time.Minute)
		rec(alice, aliceKey, domain.ActionLoginFailed, "1.1.1.1", 3*time.Minute)
		rec(alice, aliceKey, domain.ActionLoginFailed, "1.1.1.1", 4*time.Minute)
		rec(alice, aliceKey, domain.ActionLoginFailed, "2.2.2.2", 5*time.Minute)
		rec(bob, bobKey, domain.ActionLoginFailed, "1.1.1.1", 6*time.Minute)
		rec("", nobodyKey, domain.ActionLoginFailed, "1.1.1.1", 7*time.Minute)
		rec("", "", "cli:staff create", "", 8*time.Minute)

		n, last, err := repo.LoginFailures(ctx, aliceKey, "1.1.1.1", time.Time{})
		if err != nil || n != 2 || !last.Equal(base.Add(4*time.Minute)) {
			t.Errorf("LoginFailures = %d, %v, %v, want 2 次（只数这个 IP 上次成功之后的）", n, last, err)
		}
		if n, _, _ := repo.LoginFailures(ctx, aliceKey, "2.2.2.2", time.Time{}); n != 1 {
			t.Errorf("另一个 IP 的成功不清零这个 IP 的计数，LoginFailures(2.2.2.2) = %d, want 1", n)
		}
		if n, _, _ := repo.LoginFailures(ctx, aliceKey, "1.1.1.1", base.Add(3*time.Minute+30*time.Second)); n != 1 {
			t.Errorf("since（改密时间）之前的失败不计，LoginFailures = %d, want 1", n)
		}
		if n, _, _ := repo.LoginFailures(ctx, nobodyKey, "1.1.1.1", time.Time{}); n != 1 {
			t.Errorf("账号不存在的用户名也要计数，LoginFailures = %d, want 1", n)
		}
		if n, _, _ := repo.LoginFailures(ctx, "key-other", "1.1.1.1", time.Time{}); n != 0 {
			t.Errorf("没有记录的用户名 LoginFailures = %d", n)
		}

		all, _, err := repo.QueryAudit(ctx, domain.AuditFilter{Limit: 100})
		if err != nil || len(all) != 8 || all[0].Action != "cli:staff create" || all[0].StaffID != "" {
			t.Fatalf("QueryAudit 全部 = %d 条, %v；应按时间倒序", len(all), err)
		}
		byAlice, _, _ := repo.QueryAudit(ctx, domain.AuditFilter{StaffID: alice, Limit: 100})
		if len(byAlice) != 5 {
			t.Errorf("按员工筛选 = %d 条, want 5", len(byAlice))
		}
		ranged, _, _ := repo.QueryAudit(ctx, domain.AuditFilter{From: base.Add(3 * time.Minute), To: base.Add(5 * time.Minute), Limit: 100})
		if len(ranged) != 2 {
			t.Errorf("按时间 [3m, 5m) 筛选 = %d 条, want 2", len(ranged))
		}
		p1, next, _ := repo.QueryAudit(ctx, domain.AuditFilter{Limit: 4})
		p2, next2, err := repo.QueryAudit(ctx, domain.AuditFilter{Limit: 4, Cursor: next})
		if err != nil || len(p1) != 4 || len(p2) != 4 || next2 != "" || p2[0].ID == p1[3].ID {
			t.Errorf("分页 = %d + %d, next2=%q, err=%v", len(p1), len(p2), next2, err)
		}
		if _, _, err := repo.QueryAudit(ctx, domain.AuditFilter{Limit: 4, Cursor: "bad"}); !errors.Is(err, domain.ErrInvalidCursor) {
			t.Errorf("非法 cursor error = %v", err)
		}

		deleted, err := repo.PurgeAudit(ctx, base.Add(3*time.Minute+30*time.Second))
		if err != nil || deleted != 3 {
			t.Errorf("PurgeAudit = %d, %v, want 3", deleted, err)
		}
		if rest, _, _ := repo.QueryAudit(ctx, domain.AuditFilter{Limit: 100}); len(rest) != 5 {
			t.Errorf("删除后剩 %d 条, want 5", len(rest))
		}
	})
}
