package domain

import "time"

type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
)

// Account 是后台员工。PasswordHash 是 Argon2id PHC 字符串。
type Account struct {
	ID                 string
	Username           string
	DisplayName        string
	PasswordHash       string
	SuperAdmin         bool
	MustChangePassword bool
	// TempPasswordExpiresAt 为 nil 表示当前密码不是临时密码。
	TempPasswordExpiresAt *time.Time
	// PasswordChangedAt 是最后一次设置密码的时间；登录失败退避只数这之后的失败。
	PasswordChangedAt time.Time
	Status            Status
	// CreatedBy 为空表示由 CLI 创建。
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Session struct {
	ID         string
	StaffID    string
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
	// RevokedAt 为 nil 表示未吊销。
	RevokedAt *time.Time
	IP        string
	UserAgent string
}

// SessionStatus 是会话在"我的登录设备"里显示的状态。
type SessionStatus string

const (
	SessionActive  SessionStatus = "active"
	SessionRevoked SessionStatus = "revoked"
	// SessionExpired 包括超过绝对有效期和空闲超时。
	SessionExpired SessionStatus = "expired"
)

// Status 的判断与 Repository.Authenticate 一致（账号停用时会话已被吊销，不用另外判断）。
func (s *Session) Status(now time.Time, idleTimeout time.Duration) SessionStatus {
	switch {
	case s.RevokedAt != nil:
		return SessionRevoked
	case !now.Before(s.ExpiresAt) || !s.LastUsedAt.After(now.Add(-idleTimeout)):
		return SessionExpired
	}
	return SessionActive
}

// Principal 是会话校验通过后的当前员工。
type Principal struct {
	SessionID          string
	StaffID            string
	SuperAdmin         bool
	MustChangePassword bool
	// LastUsedAt 用于判断要不要刷新会话的最后使用时间。
	LastUsedAt time.Time
}

type Role struct {
	ID          string
	Name        string
	Description string
	Permissions []string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AuditEntry 是一条操作日志。StaffID 为空表示 CLI 操作或登录时账号不存在。
type AuditEntry struct {
	ID      string
	StaffID string
	// LoginKey 是登录时输入的用户名的摘要（不存原文），只有登录日志有，用于按"用户名 + IP"
	// 计数退避；账号不存在时也有。
	LoginKey  string
	Action    string
	Target    string
	Result    string
	IP        string
	RequestID string
	CreatedAt time.Time
}

// AuditFilter 的零值字段表示不筛选。
type AuditFilter struct {
	StaffID string
	From    time.Time // 含
	To      time.Time // 不含
	Cursor  string
	Limit   int
}

// 登录相关的操作日志动作。HTTP 写操作的动作是"请求方法 + 路由模板"。
const (
	ActionLoginSucceeded = "login_succeeded"
	ActionLoginFailed    = "login_failed"
	// ActionLoginThrottled 是退避期内的尝试：只做哑比较，返回 429 login_throttled。
	ActionLoginThrottled = "login_throttled"
)

// Operation 是 api/admin.yaml 里的一个后台操作，由组合根解析后传入。
type Operation struct {
	Method string
	// Route 是 Echo 的路由模板，带 /admin/v1 前缀，如 /admin/v1/accounts/:id。
	Route      string
	Permission string
	Tag        string
	// Summary、TagSummary 是 spec 里操作和 tag 的中文说明，只用于展示权限码。
	Summary    string
	TagSummary string
}

// 保留的权限值（见 api/admin.yaml 的说明），其余都是 <资源>:<动作> 权限码。
const (
	PermissionPublic        = "public"
	PermissionAuthenticated = "authenticated"
	PermissionSuperAdmin    = "superadmin"
)

// IsPermissionCode 报告 p 是不是可以授予角色的权限码（而不是保留值）。
func IsPermissionCode(p string) bool {
	return p != "" && p != PermissionPublic && p != PermissionAuthenticated && p != PermissionSuperAdmin
}

// 列表分页的默认和最大条数。
const (
	DefaultListLimit = 20
	MaxListLimit     = 100
)
