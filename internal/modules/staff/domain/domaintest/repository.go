// Package domaintest 提供 staff 的内存 fake，供本模块和组合根的测试复用。
package domaintest

import (
	"context"
	"slices"
	"sync"
	"time"

	"app.local/internal/modules/staff/domain"
)

type session struct {
	domain.Session
	tokenHash string
}

// Repository 是 domain.Repository 的内存实现。时间一律取 time.Now()，和 Postgres
// 实现用 now() 的语义一致。
type Repository struct {
	mu           sync.Mutex
	accounts     map[string]*domain.Account
	accountOrder []string // 新的在前
	sessions     map[string]*session
	roles        map[string]*domain.Role
	accountRoles map[string][]string // staffID -> roleIDs
	audit        []*domain.AuditEntry
}

func NewRepository() *Repository {
	return &Repository{
		accounts:     map[string]*domain.Account{},
		sessions:     map[string]*session{},
		roles:        map[string]*domain.Role{},
		accountRoles: map[string][]string{},
	}
}

func (f *Repository) CreateAccount(_ context.Context, a *domain.Account) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, x := range f.accounts {
		if x.Username == a.Username {
			return domain.ErrUsernameTaken
		}
	}
	now := time.Now()
	a.CreatedAt, a.UpdatedAt = now, now
	f.accounts[a.ID] = new(*a)
	f.accountOrder = slices.Insert(f.accountOrder, 0, a.ID)
	return nil
}

func (f *Repository) GetAccount(_ context.Context, id string) (*domain.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.accounts[id]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}
	return new(*a), nil
}

func (f *Repository) GetAccountByUsername(_ context.Context, username string) (*domain.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.accounts {
		if a.Username == username {
			return new(*a), nil
		}
	}
	return nil, domain.ErrAccountNotFound
}

func (f *Repository) ListAccounts(_ context.Context, cursor string, limit int) ([]*domain.Account, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	start := 0
	if cursor != "" {
		i := slices.Index(f.accountOrder, cursor)
		if i < 0 {
			return nil, "", domain.ErrInvalidCursor
		}
		start = i + 1
	}
	end := min(start+limit, len(f.accountOrder))
	items := make([]*domain.Account, 0, end-start)
	for _, id := range f.accountOrder[start:end] {
		items = append(items, new(*f.accounts[id]))
	}
	next := ""
	if end < len(f.accountOrder) {
		next = f.accountOrder[end-1]
	}
	return items, next, nil
}

func (f *Repository) update(id string, fn func(*domain.Account)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.accounts[id]
	if !ok {
		return domain.ErrAccountNotFound
	}
	fn(a)
	a.UpdatedAt = time.Now()
	return nil
}

func (f *Repository) UpdatePassword(_ context.Context, id, passwordHash string, mustChange bool, tempExpiresAt *time.Time, changedAt time.Time) error {
	return f.update(id, func(a *domain.Account) {
		a.PasswordHash, a.MustChangePassword, a.TempPasswordExpiresAt, a.PasswordChangedAt = passwordHash, mustChange, tempExpiresAt, changedAt
	})
}

// LockAccount 在 fake 里只检查账号存在：fake 的方法各自持锁，不模拟跨调用的行锁。
func (f *Repository) LockAccount(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.accounts[id]; !ok {
		return domain.ErrAccountNotFound
	}
	return nil
}

func (f *Repository) CountActiveSuperAdmins(_ context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, a := range f.accounts {
		if a.SuperAdmin && a.Status == domain.StatusActive {
			n++
		}
	}
	return n, nil
}

func (f *Repository) LockActiveSuperAdmins(_ context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for _, a := range f.accounts {
		if a.SuperAdmin && a.Status == domain.StatusActive {
			ids = append(ids, a.ID)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

func (f *Repository) SetStatus(_ context.Context, id string, status domain.Status) error {
	return f.update(id, func(a *domain.Account) { a.Status = status })
}

func (f *Repository) SetSuperAdmin(_ context.Context, id string, superAdmin bool) error {
	return f.update(id, func(a *domain.Account) { a.SuperAdmin = superAdmin })
}

func (f *Repository) CreateSession(_ context.Context, s *domain.Session, tokenHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	s.CreatedAt, s.LastUsedAt = now, now
	f.sessions[s.ID] = &session{Session: *s, tokenHash: tokenHash}
	return nil
}

func (f *Repository) Authenticate(_ context.Context, tokenHash string, idleTimeout time.Duration) (*domain.Principal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	for _, s := range f.sessions {
		if s.tokenHash != tokenHash {
			continue
		}
		a, ok := f.accounts[s.StaffID]
		if !ok || s.RevokedAt != nil || !s.ExpiresAt.After(now) || !s.LastUsedAt.After(now.Add(-idleTimeout)) || a.Status != domain.StatusActive {
			return nil, domain.ErrSessionInvalid
		}
		return &domain.Principal{SessionID: s.ID, StaffID: a.ID, SuperAdmin: a.SuperAdmin,
			MustChangePassword: a.MustChangePassword, LastUsedAt: s.LastUsedAt}, nil
	}
	return nil, domain.ErrSessionInvalid
}

func (f *Repository) TouchSession(_ context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.sessions[sessionID]; ok && s.LastUsedAt.Before(time.Now().Add(-time.Minute)) {
		s.LastUsedAt = time.Now()
	}
	return nil
}

// AgeSession 把会话的时间往前推 d，供测试模拟空闲超时和绝对过期。
func (f *Repository) AgeSession(sessionID string, lastUsedAgo, expiresIn time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[sessionID]
	s.LastUsedAt = time.Now().Add(-lastUsedAgo)
	s.ExpiresAt = time.Now().Add(expiresIn)
}

func (f *Repository) RevokeSession(_ context.Context, staffID, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[sessionID]
	if !ok || s.StaffID != staffID {
		return domain.ErrSessionNotFound
	}
	if s.RevokedAt == nil {
		s.RevokedAt = new(time.Now())
	}
	return nil
}

func (f *Repository) RevokeSessions(_ context.Context, staffID, exceptSessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sessions {
		if s.StaffID == staffID && s.ID != exceptSessionID && s.RevokedAt == nil {
			s.RevokedAt = new(time.Now())
		}
	}
	return nil
}

func (f *Repository) ListSessions(_ context.Context, staffID string, limit int) ([]*domain.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Session
	for _, s := range f.sessions {
		if s.StaffID == staffID {
			out = append(out, new(s.Session))
		}
	}
	slices.SortFunc(out, func(a, b *domain.Session) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out[:min(limit, len(out))], nil
}

func (f *Repository) CreateRole(_ context.Context, r *domain.Role) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.roleNameTaken(r.Name, "") {
		return domain.ErrRoleNameTaken
	}
	now := time.Now()
	r.CreatedAt, r.UpdatedAt = now, now
	f.roles[r.ID] = &domain.Role{ID: r.ID, Name: r.Name, Description: r.Description, CreatedAt: now, UpdatedAt: now}
	return nil
}

func (f *Repository) roleNameTaken(name, exceptID string) bool {
	for _, x := range f.roles {
		if x.Name == name && x.ID != exceptID {
			return true
		}
	}
	return false
}

func (f *Repository) UpdateRole(_ context.Context, r *domain.Role) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	x, ok := f.roles[r.ID]
	if !ok {
		return domain.ErrRoleNotFound
	}
	if f.roleNameTaken(r.Name, r.ID) {
		return domain.ErrRoleNameTaken
	}
	x.Name, x.Description, x.UpdatedAt = r.Name, r.Description, time.Now()
	return nil
}

func (f *Repository) DeleteRole(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.roles[id]; !ok {
		return domain.ErrRoleNotFound
	}
	delete(f.roles, id)
	for staffID, ids := range f.accountRoles {
		f.accountRoles[staffID] = slices.DeleteFunc(ids, func(x string) bool { return x == id })
	}
	return nil
}

func (f *Repository) GetRole(_ context.Context, id string) (*domain.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.roles[id]
	if !ok {
		return nil, domain.ErrRoleNotFound
	}
	return copyRole(r), nil
}

func copyRole(r *domain.Role) *domain.Role {
	c := *r
	c.Permissions = slices.Clone(r.Permissions)
	if c.Permissions == nil {
		c.Permissions = []string{}
	}
	return &c
}

func (f *Repository) ListRoles(_ context.Context) ([]*domain.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*domain.Role, 0, len(f.roles))
	for _, r := range f.roles {
		out = append(out, copyRole(r))
	}
	slices.SortFunc(out, func(a, b *domain.Role) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

func (f *Repository) SetRolePermissions(_ context.Context, roleID string, permissions []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.roles[roleID]
	if !ok {
		return domain.ErrRoleNotFound
	}
	r.Permissions = slices.Compact(slices.Sorted(slices.Values(permissions)))
	return nil
}

func (f *Repository) SetAccountRoles(_ context.Context, staffID string, roleIDs []string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var kept []string
	for _, id := range slices.Compact(slices.Sorted(slices.Values(roleIDs))) {
		if _, ok := f.roles[id]; ok {
			kept = append(kept, id)
		}
	}
	f.accountRoles[staffID] = kept
	return len(kept), nil
}

func (f *Repository) AccountRoleIDs(_ context.Context, staffIDs []string) (map[string][]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string][]string{}
	for _, id := range staffIDs {
		if roles := f.accountRoles[id]; len(roles) > 0 {
			out[id] = slices.Sorted(slices.Values(roles))
		}
	}
	return out, nil
}

func (f *Repository) HasPermission(ctx context.Context, staffID, permission string) (bool, error) {
	perms, err := f.Permissions(ctx, staffID)
	return slices.Contains(perms, permission), err
}

func (f *Repository) Permissions(_ context.Context, staffID string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, id := range f.accountRoles[staffID] {
		if r, ok := f.roles[id]; ok {
			out = append(out, r.Permissions...)
		}
	}
	out = slices.Compact(slices.Sorted(slices.Values(out)))
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func (f *Repository) RecordAudit(_ context.Context, e *domain.AuditEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	f.audit = append(f.audit, new(*e))
	return nil
}

// Audit 返回全部日志（按写入顺序），供测试断言。
func (f *Repository) Audit() []domain.AuditEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.AuditEntry, 0, len(f.audit))
	for _, e := range f.audit {
		out = append(out, *e)
	}
	return out
}

func (f *Repository) QueryAudit(_ context.Context, flt domain.AuditFilter) ([]*domain.AuditEntry, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var matched []*domain.AuditEntry
	for _, e := range slices.Backward(f.audit) {
		if (flt.StaffID == "" || e.StaffID == flt.StaffID) &&
			(flt.From.IsZero() || !e.CreatedAt.Before(flt.From)) &&
			(flt.To.IsZero() || e.CreatedAt.Before(flt.To)) {
			matched = append(matched, e)
		}
	}
	start := 0
	if flt.Cursor != "" {
		i := slices.IndexFunc(matched, func(e *domain.AuditEntry) bool { return e.ID == flt.Cursor })
		if i < 0 {
			return nil, "", domain.ErrInvalidCursor
		}
		start = i + 1
	}
	end := min(start+flt.Limit, len(matched))
	items := make([]*domain.AuditEntry, 0, end-start)
	for _, e := range matched[start:end] {
		items = append(items, new(*e))
	}
	next := ""
	if end < len(matched) {
		next = matched[end-1].ID
	}
	return items, next, nil
}

func (f *Repository) LoginFailures(_ context.Context, loginKey, ip string, since time.Time) (int, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	lastSuccess := since
	for _, e := range f.audit {
		if e.LoginKey == loginKey && e.IP == ip && e.Action == domain.ActionLoginSucceeded && e.CreatedAt.After(lastSuccess) {
			lastSuccess = e.CreatedAt
		}
	}
	count, last := 0, time.Time{}
	for _, e := range f.audit {
		if e.LoginKey == loginKey && e.IP == ip && e.Action == domain.ActionLoginFailed && e.CreatedAt.After(lastSuccess) {
			count++
			if e.CreatedAt.After(last) {
				last = e.CreatedAt
			}
		}
	}
	return count, last, nil
}

func (f *Repository) PurgeAudit(_ context.Context, before time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(f.audit)
	f.audit = slices.DeleteFunc(f.audit, func(e *domain.AuditEntry) bool { return e.CreatedAt.Before(before) })
	return int64(n - len(f.audit)), nil
}

var _ domain.Repository = (*Repository)(nil)
