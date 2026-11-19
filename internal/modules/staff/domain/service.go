package domain

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
	"uuid"

	"app.local/internal/kit/logctx"
	"app.local/internal/kit/uow"
)

// MinPasswordLength 在 domain 层校验，CLI 和 HTTP 两个入口都受约束。
const MinPasswordLength = 12

// 登录失败退避：同一账号、同一 IP 连续失败 backoffAfter 次后开始等待，每多失败一次
// 等待时间翻倍，最长 backoffMax。不做硬锁定：只按账号锁，攻击者能把管理员锁在外面。
const (
	backoffAfter = 5
	backoffBase  = 30 * time.Second
	backoffMax   = 15 * time.Minute
)

const dummyPassword = "dummy-password-for-constant-time-comparison"

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,63}$`)

type Config struct {
	SessionIdleTimeout time.Duration
	SessionMaxAge      time.Duration
	TempPasswordTTL    time.Duration
}

type Service struct {
	repo      Repository
	passwords PasswordVerifier
	uow       uow.UnitOfWork
	cfg       Config
	// codes 是 admin.yaml 里出现过的全部权限码；spec 里删掉的权限码在检查和展示时忽略。
	codes     []PermissionInfo
	now       func() time.Time
	dummyHash func() string
}

// PermissionInfo 是一个可授予的权限码和它所属的 tag（模块），供角色页按模块分组。
// PermissionInfo 是一个可授予的权限码。TagSummary 是模块的中文名，Operations 是需要这个
// 权限码的操作的说明，都来自 admin.yaml，只用于在角色权限页上展示。
type PermissionInfo struct {
	Code       string
	Tag        string
	TagSummary string
	Operations []string
}

type Option func(*Service)

// WithClock 供测试控制登录退避和临时密码过期的时间。
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

func NewService(repo Repository, passwords PasswordVerifier, u uow.UnitOfWork, cfg Config, ops []Operation, opts ...Option) *Service {
	s := &Service{repo: repo, passwords: passwords, uow: u, cfg: cfg, now: time.Now}
	index := map[string]int{}
	for _, op := range ops {
		if !IsPermissionCode(op.Permission) {
			continue
		}
		i, ok := index[op.Permission]
		if !ok {
			i = len(s.codes)
			index[op.Permission] = i
			s.codes = append(s.codes, PermissionInfo{Code: op.Permission, Tag: op.Tag, TagSummary: op.TagSummary})
		}
		if op.Summary != "" {
			s.codes[i].Operations = append(s.codes[i].Operations, op.Summary)
		}
	}
	slices.SortFunc(s.codes, func(a, b PermissionInfo) int { return cmp.Or(cmp.Compare(a.Tag, b.Tag), cmp.Compare(a.Code, b.Code)) })
	for _, opt := range opts {
		opt(s)
	}
	s.dummyHash = sync.OnceValue(func() string {
		h, err := passwords.Hash(dummyPassword)
		if err != nil {
			return ""
		}
		return h
	})
	return s
}

// LoginInput 的 IP、UserAgent、RequestID 来自请求，用于会话记录、退避计数和操作日志。
type LoginInput struct {
	Username  string
	Password  string
	IP        string
	UserAgent string
	RequestID string
}

// Login 校验用户名和密码，成功后建会话并返回会话 token（只在这里出现一次，库里存摘要）。
// 用户名不存在、密码错误、账号停用对外一律是 ErrInvalidCredentials，并且都做一次密码比较
// （不存在时是哑比较），响应时间不暴露账号是否存在。
//
// 退避按"用户名摘要 + IP"计数，不管账号是否存在：不存在的用户名一样会进入等待，所以等待期
// 可以单独返回 ErrLoginThrottled（带剩余时间），让输错的人知道要等多久，又不暴露账号是否
// 存在。等待期内也做一次哑比较。
//
// 存在的账号在账号行锁上排队："数失败次数 → 比较密码 → 记失败"在一个事务里完成，并发请求
// 不能一起读到"还没到阈值"而绕过退避。
func (s *Service) Login(ctx context.Context, in LoginInput) (string, *Account, error) {
	username := normalizeUsername(in.Username)
	if username == "" || in.Password == "" {
		return "", nil, ErrInvalidCredentials
	}
	key := loginKey(username)

	found, err := s.repo.GetAccountByUsername(ctx, username)
	if err != nil && !errors.Is(err, ErrAccountNotFound) {
		return "", nil, fmt.Errorf("login: %w", err)
	}

	var token string
	var a *Account
	var denied error // 登录被拒绝时事务照常提交，失败记录要留下来供退避计数。
	err = s.uow.Run(ctx, func(ctx context.Context) error {
		var staffID string
		var since time.Time // 存在的账号只数上次设置密码之后的失败
		if found != nil {
			if err := s.repo.LockAccount(ctx, found.ID); err != nil {
				return err
			}
			var err error
			if a, err = s.repo.GetAccount(ctx, found.ID); err != nil {
				return err
			}
			staffID, since = a.ID, a.PasswordChangedAt
		}
		audit := func(action, result string) error {
			return s.RecordAudit(ctx, &AuditEntry{StaffID: staffID, LoginKey: key, Action: action, Result: result, IP: in.IP, RequestID: in.RequestID})
		}
		failures, last, err := s.repo.LoginFailures(ctx, key, in.IP, since)
		if err != nil {
			return err
		}
		if until := last.Add(backoff(failures)); failures >= backoffAfter && s.now().Before(until) {
			_, _ = s.passwords.Compare(s.dummyHash(), in.Password)
			denied = ErrLoginThrottled.WithRetryAfter(until.Sub(s.now()))
			return audit(ActionLoginThrottled, "throttled")
		}
		if a == nil {
			_, _ = s.passwords.Compare(s.dummyHash(), in.Password)
			denied = ErrInvalidCredentials
			return audit(ActionLoginFailed, "invalid_credentials")
		}
		ok, err := s.passwords.Compare(a.PasswordHash, in.Password)
		if err != nil {
			return err
		}
		switch {
		case !ok:
			denied = ErrInvalidCredentials
			return audit(ActionLoginFailed, "invalid_credentials")
		case a.Status != StatusActive:
			denied = ErrInvalidCredentials
			return audit(ActionLoginFailed, "disabled")
		case a.MustChangePassword && a.TempPasswordExpiresAt != nil && !s.now().Before(*a.TempPasswordExpiresAt):
			// 密码正确之后才返回，不泄露账号是否存在。
			denied = ErrTemporaryPasswordExpired
			return audit(ActionLoginFailed, "temporary_password_expired")
		}

		if token, err = randomToken(32); err != nil {
			return err
		}
		sess := &Session{ID: uuid.NewV7().String(), StaffID: a.ID, ExpiresAt: s.now().Add(s.cfg.SessionMaxAge), IP: in.IP, UserAgent: in.UserAgent}
		if err := s.repo.CreateSession(ctx, sess, hashToken(token)); err != nil {
			return err
		}
		return audit(ActionLoginSucceeded, "ok")
	})
	if err != nil {
		return "", nil, fmt.Errorf("login: %w", err)
	}
	if denied != nil {
		return "", nil, denied
	}
	return token, a, nil
}

// backoff 返回连续失败 n 次后需要等待的时间。
func backoff(n int) time.Duration {
	if n < backoffAfter {
		return 0
	}
	shift := min(n-backoffAfter, 10)
	return min(backoffBase<<shift, backoffMax)
}

// Authenticate 校验会话 token，每个后台请求执行一次。不缓存：吊销、停用立即生效；
// 查询失败也拒绝（返回错误）。
func (s *Service) Authenticate(ctx context.Context, token string) (*Principal, error) {
	if token == "" {
		return nil, ErrSessionInvalid
	}
	p, err := s.repo.Authenticate(ctx, hashToken(token), s.cfg.SessionIdleTimeout)
	if err != nil {
		return nil, err
	}
	if s.now().Sub(p.LastUsedAt) > time.Minute {
		if err := s.repo.TouchSession(ctx, p.SessionID); err != nil {
			// 只影响空闲超时的精度，不拒绝这次请求。
			logctx.From(ctx).WarnContext(ctx, "staff: touch session failed", "error", err)
		}
	}
	return p, nil
}

func (s *Service) Logout(ctx context.Context, p *Principal) error {
	return s.repo.RevokeSession(ctx, p.StaffID, p.SessionID)
}

// Authorize 检查当前员工能否执行声明了 permission 的操作。超级管理员直接放行；
// 普通权限码每次查库，收回角色立即生效。
func (s *Service) Authorize(ctx context.Context, p *Principal, permission string) error {
	switch {
	case permission == PermissionAuthenticated:
		return nil
	case p.SuperAdmin:
		return nil
	case !s.isKnownCode(permission):
		// 保留值以外、admin.yaml 里也没有的权限码（包括已从 spec 删掉的）一律拒绝。
		return ErrPermissionDenied
	}
	ok, err := s.repo.HasPermission(ctx, p.StaffID, permission)
	if err != nil {
		return fmt.Errorf("authorize: %w", err)
	}
	if !ok {
		return ErrPermissionDenied
	}
	return nil
}

// Catalog 返回 admin.yaml 里全部可授予的权限码，按 tag、权限码排序。
func (s *Service) Catalog() []PermissionInfo {
	out := slices.Clone(s.codes)
	for i := range out {
		out[i].Operations = slices.Clone(out[i].Operations)
	}
	return out
}

func (s *Service) isKnownCode(code string) bool {
	return slices.ContainsFunc(s.codes, func(p PermissionInfo) bool { return p.Code == code })
}

// Permissions 返回当前员工的有效权限码：超级管理员是全部权限码；其余员工是角色权限码的
// 并集，spec 里已经删掉的权限码不返回。
func (s *Service) Permissions(ctx context.Context, p *Principal) ([]string, error) {
	if p.SuperAdmin {
		out := make([]string, 0, len(s.codes))
		for _, c := range s.codes {
			out = append(out, c.Code)
		}
		slices.Sort(out)
		return out, nil
	}
	perms, err := s.repo.Permissions(ctx, p.StaffID)
	if err != nil {
		return nil, fmt.Errorf("permissions: %w", err)
	}
	return slices.DeleteFunc(perms, func(c string) bool { return !s.isKnownCode(c) }), nil
}

func (s *Service) GetAccount(ctx context.Context, id string) (*Account, error) {
	return s.repo.GetAccount(ctx, id)
}

// AccountRoleIDs 返回这些员工各自绑定的角色 ID，没有角色的员工不在结果里。
func (s *Service) AccountRoleIDs(ctx context.Context, ids []string) (map[string][]string, error) {
	if len(ids) == 0 {
		return map[string][]string{}, nil
	}
	return s.repo.AccountRoleIDs(ctx, ids)
}

// CountActiveSuperAdmins 供员工页提示"只剩一个超级管理员"。
func (s *Service) CountActiveSuperAdmins(ctx context.Context) (int, error) {
	return s.repo.CountActiveSuperAdmins(ctx)
}

func (s *Service) ListAccounts(ctx context.Context, cursor string, limit int) ([]*Account, string, error) {
	if limit <= 0 || limit > MaxListLimit {
		limit = DefaultListLimit
	}
	return s.repo.ListAccounts(ctx, cursor, limit)
}

// ChangePassword 改自己的密码：校验当前密码，清除临时密码状态，吊销除当前会话以外的会话。
func (s *Service) ChangePassword(ctx context.Context, p *Principal, current, next string) error {
	if utf8.RuneCountInString(next) < MinPasswordLength || next == current {
		return ErrInvalidInput
	}
	a, err := s.repo.GetAccount(ctx, p.StaffID)
	if err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	ok, err := s.passwords.Compare(a.PasswordHash, current)
	if err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	if !ok {
		return ErrWrongPassword
	}
	hash, err := s.passwords.Hash(next)
	if err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	return s.uow.Run(ctx, func(ctx context.Context) error {
		if err := s.repo.UpdatePassword(ctx, a.ID, hash, false, nil, s.now()); err != nil {
			return err
		}
		return s.repo.RevokeSessions(ctx, a.ID, p.SessionID)
	})
}

// CreateAccountInput 的 ActorID 为空表示由 CLI 创建。
type CreateAccountInput struct {
	ActorID     string
	Username    string
	DisplayName string
	SuperAdmin  bool
}

// CreateAccount 创建员工并设一个临时密码（返回明文，只出现这一次），首次登录后必须改密。
func (s *Service) CreateAccount(ctx context.Context, in CreateAccountInput) (*Account, string, error) {
	username := normalizeUsername(in.Username)
	display := strings.TrimSpace(in.DisplayName)
	if display == "" {
		display = username
	}
	if !usernamePattern.MatchString(username) || utf8.RuneCountInString(display) > 100 {
		return nil, "", ErrInvalidInput
	}
	temp, hash, expires, err := s.newTempPassword()
	if err != nil {
		return nil, "", fmt.Errorf("create account: %w", err)
	}
	a := &Account{
		ID: uuid.NewV7().String(), Username: username, DisplayName: display, PasswordHash: hash,
		SuperAdmin: in.SuperAdmin, MustChangePassword: true, TempPasswordExpiresAt: &expires,
		PasswordChangedAt: s.now(), Status: StatusActive, CreatedBy: in.ActorID,
	}
	if err := s.repo.CreateAccount(ctx, a); err != nil {
		if errors.Is(err, ErrUsernameTaken) {
			return nil, "", err
		}
		return nil, "", fmt.Errorf("create account: %w", err)
	}
	return a, temp, nil
}

// ResetPassword 设一个新的临时密码并吊销对方全部会话，返回临时密码明文和它的过期时间。
func (s *Service) ResetPassword(ctx context.Context, id string) (string, time.Time, error) {
	return s.resetPassword(ctx, id, false)
}

// RecoverAccount 是 CLI 的恢复手段：重设密码并启用账号，在一个事务里完成，不会出现
// "密码已改、账号仍停用、临时密码没拿到"的中间状态。
func (s *Service) RecoverAccount(ctx context.Context, id string) (string, time.Time, error) {
	return s.resetPassword(ctx, id, true)
}

func (s *Service) resetPassword(ctx context.Context, id string, enable bool) (string, time.Time, error) {
	temp, hash, expires, err := s.newTempPassword()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("reset password: %w", err)
	}
	err = s.uow.Run(ctx, func(ctx context.Context) error {
		if err := s.repo.UpdatePassword(ctx, id, hash, true, &expires, s.now()); err != nil {
			return err
		}
		if enable {
			if err := s.repo.SetStatus(ctx, id, StatusActive); err != nil {
				return err
			}
		}
		return s.repo.RevokeSessions(ctx, id, "")
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return temp, expires, nil
}

func (s *Service) newTempPassword() (plain, hash string, expires time.Time, err error) {
	plain, err = randomToken(16)
	if err != nil {
		return "", "", time.Time{}, err
	}
	hash, err = s.passwords.Hash(plain)
	if err != nil {
		return "", "", time.Time{}, err
	}
	return plain, hash, s.now().Add(s.cfg.TempPasswordTTL), nil
}

// Disable 停用员工并吊销它的全部会话。不能停用自己；停用超级管理员时必须还剩至少一个
// active 的超级管理员（在锁里检查，并发的互相停用不会把超级管理员全部停掉）。
func (s *Service) Disable(ctx context.Context, actorID, id string) error {
	if actorID == id {
		return ErrCannotModifySelf
	}
	return s.uow.Run(ctx, func(ctx context.Context) error {
		if err := s.keepOneSuperAdmin(ctx, id); err != nil {
			return err
		}
		if err := s.repo.SetStatus(ctx, id, StatusDisabled); err != nil {
			return err
		}
		return s.repo.RevokeSessions(ctx, id, "")
	})
}

func (s *Service) Enable(ctx context.Context, id string) error {
	return s.repo.SetStatus(ctx, id, StatusActive)
}

// SetSuperAdmin 不能取消自己的标记；取消别人的标记时同样检查至少留下一个超级管理员。
func (s *Service) SetSuperAdmin(ctx context.Context, actorID, id string, superAdmin bool) error {
	if actorID == id && !superAdmin {
		return ErrCannotModifySelf
	}
	if superAdmin {
		return s.repo.SetSuperAdmin(ctx, id, true)
	}
	return s.uow.Run(ctx, func(ctx context.Context) error {
		if err := s.keepOneSuperAdmin(ctx, id); err != nil {
			return err
		}
		return s.repo.SetSuperAdmin(ctx, id, false)
	})
}

// keepOneSuperAdmin 在事务里锁住全部 active 超级管理员，id 是其中最后一个时拒绝。
func (s *Service) keepOneSuperAdmin(ctx context.Context, id string) error {
	ids, err := s.repo.LockActiveSuperAdmins(ctx)
	if err != nil {
		return err
	}
	if len(ids) == 1 && ids[0] == id {
		return ErrLastSuperAdmin
	}
	return nil
}

// RevokeSessions 吊销员工的全部会话（踢下线）。
func (s *Service) RevokeSessions(ctx context.Context, id string) error {
	if _, err := s.repo.GetAccount(ctx, id); err != nil {
		return err
	}
	return s.repo.RevokeSessions(ctx, id, "")
}

// maxListedSessions 是"我的登录设备"列出的最近会话数。
const maxListedSessions = 50

// ListedSession 是"我的登录设备"里的一行。
type ListedSession struct {
	*Session
	Status SessionStatus
}

func (s *Service) ListSessions(ctx context.Context, staffID string) ([]ListedSession, error) {
	sessions, err := s.repo.ListSessions(ctx, staffID, maxListedSessions)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]ListedSession, len(sessions))
	for i, sess := range sessions {
		out[i] = ListedSession{Session: sess, Status: sess.Status(now, s.cfg.SessionIdleTimeout)}
	}
	return out, nil
}

func (s *Service) RevokeSession(ctx context.Context, staffID, sessionID string) error {
	return s.repo.RevokeSession(ctx, staffID, sessionID)
}

func (s *Service) ListRoles(ctx context.Context) ([]*Role, error) {
	return s.repo.ListRoles(ctx)
}

func (s *Service) GetRole(ctx context.Context, id string) (*Role, error) {
	return s.repo.GetRole(ctx, id)
}

func validRoleName(name string) bool {
	return name != "" && utf8.RuneCountInString(name) <= 100
}

func (s *Service) CreateRole(ctx context.Context, name, description string) (*Role, error) {
	r := &Role{ID: uuid.NewV7().String(), Name: strings.TrimSpace(name), Description: strings.TrimSpace(description), Permissions: []string{}}
	if !validRoleName(r.Name) {
		return nil, ErrInvalidInput
	}
	if err := s.repo.CreateRole(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *Service) UpdateRole(ctx context.Context, id, name, description string) (*Role, error) {
	r := &Role{ID: id, Name: strings.TrimSpace(name), Description: strings.TrimSpace(description)}
	if !validRoleName(r.Name) {
		return nil, ErrInvalidInput
	}
	if err := s.repo.UpdateRole(ctx, r); err != nil {
		return nil, err
	}
	return s.repo.GetRole(ctx, id)
}

func (s *Service) DeleteRole(ctx context.Context, id string) error {
	return s.uow.Run(ctx, func(ctx context.Context) error { return s.repo.DeleteRole(ctx, id) })
}

// SetRolePermissions 用 permissions 替换角色的权限码，只接受 admin.yaml 里出现过的权限码。
func (s *Service) SetRolePermissions(ctx context.Context, id string, permissions []string) (*Role, error) {
	for _, p := range permissions {
		if !s.isKnownCode(p) {
			return nil, ErrUnknownPermission
		}
	}
	err := s.uow.Run(ctx, func(ctx context.Context) error {
		if _, err := s.repo.GetRole(ctx, id); err != nil {
			return err
		}
		return s.repo.SetRolePermissions(ctx, id, permissions)
	})
	if err != nil {
		return nil, err
	}
	return s.repo.GetRole(ctx, id)
}

// BindRoles 用 roleIDs 替换员工的角色；有不存在的角色时整体不生效。
func (s *Service) BindRoles(ctx context.Context, staffID string, roleIDs []string) error {
	unique := slices.Compact(slices.Sorted(slices.Values(roleIDs)))
	return s.uow.Run(ctx, func(ctx context.Context) error {
		if _, err := s.repo.GetAccount(ctx, staffID); err != nil {
			return err
		}
		n, err := s.repo.SetAccountRoles(ctx, staffID, unique)
		if err != nil {
			return err
		}
		if n != len(unique) {
			return ErrRoleNotFound
		}
		return nil
	})
}

// RecordAudit 写一条操作日志，ID 为空时生成。时间取应用时钟：登录退避用同一个时钟
// 比较"最近一次失败"和"现在"。
func (s *Service) RecordAudit(ctx context.Context, e *AuditEntry) error {
	if e.ID == "" {
		e.ID = uuid.NewV7().String()
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = s.now()
	}
	return s.repo.RecordAudit(ctx, e)
}

func (s *Service) QueryAudit(ctx context.Context, f AuditFilter) ([]*AuditEntry, string, error) {
	if f.Limit <= 0 || f.Limit > MaxListLimit {
		f.Limit = DefaultListLimit
	}
	return s.repo.QueryAudit(ctx, f)
}

// PurgeAudit 删除早于保留期的操作日志。
func (s *Service) PurgeAudit(ctx context.Context, retention time.Duration) (int64, error) {
	return s.repo.PurgeAudit(ctx, s.now().Add(-retention))
}

// GetAccountByUsername 供 CLI 按用户名定位账号。
func (s *Service) GetAccountByUsername(ctx context.Context, username string) (*Account, error) {
	return s.repo.GetAccountByUsername(ctx, normalizeUsername(username))
}

func normalizeUsername(u string) string {
	return strings.ToLower(strings.TrimSpace(u))
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken 让库里只存会话 token 的摘要：数据库泄露不等于会话泄露。
// loginKey 是退避计数用的用户名摘要：账号不存在时也要计数，日志里不存输入的原文。
func loginKey(normalizedUsername string) string {
	return hashToken(normalizedUsername)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
