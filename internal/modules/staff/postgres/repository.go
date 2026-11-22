// Package postgres 是 staff 的 Repository 实现（pgx/v5，经 dbtx.Handle 取连接）。
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	staff "app.local/internal/modules/staff/domain"
	"app.local/internal/platform/dbtx"
	platformpg "app.local/internal/platform/postgres"
)

type Repository struct {
	handle *dbtx.Handle
}

func NewRepository(handle *dbtx.Handle) *Repository {
	return &Repository{handle: handle}
}

// nullable 把空字符串存成 NULL：created_by、staff_id 用 NULL 表示"没有"（CLI、账号不存在）。
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

const accountColumns = `id, username, display_name, password_hash, super_admin, must_change_password,
	temp_password_expires_at, password_changed_at, status, created_by, created_at, updated_at`

func scanAccount(row pgx.CollectableRow) (*staff.Account, error) {
	var a staff.Account
	var createdBy *string
	err := row.Scan(&a.ID, &a.Username, &a.DisplayName, &a.PasswordHash, &a.SuperAdmin, &a.MustChangePassword,
		&a.TempPasswordExpiresAt, &a.PasswordChangedAt, &a.Status, &createdBy, &a.CreatedAt, &a.UpdatedAt)
	a.CreatedBy = deref(createdBy)
	return &a, err
}

func (r *Repository) CreateAccount(ctx context.Context, a *staff.Account) error {
	err := r.handle.Resolve(ctx).QueryRow(ctx, `
		INSERT INTO staff_accounts (id, username, display_name, password_hash, super_admin, must_change_password,
			temp_password_expires_at, password_changed_at, status, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now(), now())
		RETURNING created_at, updated_at
	`, a.ID, a.Username, a.DisplayName, a.PasswordHash, a.SuperAdmin, a.MustChangePassword,
		a.TempPasswordExpiresAt, a.PasswordChangedAt, a.Status, nullable(a.CreatedBy)).Scan(&a.CreatedAt, &a.UpdatedAt)
	if platformpg.UniqueViolation(err) == "uk_staff_accounts_username" {
		return staff.ErrUsernameTaken
	}
	if err != nil {
		return fmt.Errorf("postgres: create staff account: %w", err)
	}
	return nil
}

func (r *Repository) GetAccount(ctx context.Context, id string) (*staff.Account, error) {
	rows, err := r.handle.Resolve(ctx).Query(ctx, `SELECT `+accountColumns+` FROM staff_accounts WHERE id = $1`, id)
	return collectAccount(rows, err)
}

func (r *Repository) GetAccountByUsername(ctx context.Context, username string) (*staff.Account, error) {
	rows, err := r.handle.Resolve(ctx).Query(ctx, `SELECT `+accountColumns+` FROM staff_accounts WHERE username = $1`, username)
	return collectAccount(rows, err)
}

func collectAccount(rows pgx.Rows, err error) (*staff.Account, error) {
	if err != nil {
		return nil, fmt.Errorf("postgres: get staff account: %w", err)
	}
	a, err := pgx.CollectExactlyOneRow(rows, scanAccount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, staff.ErrAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: get staff account: %w", err)
	}
	return a, nil
}

// ListAccounts 多取一行判断是否还有下一页。
func (r *Repository) ListAccounts(ctx context.Context, cursor string, limit int) ([]*staff.Account, string, error) {
	if limit <= 0 {
		limit = staff.DefaultListLimit
	}
	createdAt, id, hasCursor, err := platformpg.DecodeCursor(cursor)
	if err != nil {
		return nil, "", staff.ErrInvalidCursor
	}
	q := r.handle.Resolve(ctx)
	var rows pgx.Rows
	if hasCursor {
		rows, err = q.Query(ctx, `SELECT `+accountColumns+` FROM staff_accounts
			WHERE (created_at, id) < ($1, $2) ORDER BY created_at DESC, id DESC LIMIT $3`, createdAt, id, limit+1)
	} else {
		rows, err = q.Query(ctx, `SELECT `+accountColumns+` FROM staff_accounts
			ORDER BY created_at DESC, id DESC LIMIT $1`, limit+1)
	}
	if err != nil {
		return nil, "", fmt.Errorf("postgres: list staff accounts: %w", err)
	}
	items, err := pgx.CollectRows(rows, scanAccount)
	if err != nil {
		return nil, "", fmt.Errorf("postgres: list staff accounts: %w", err)
	}
	next := ""
	if len(items) > limit {
		last := items[limit-1]
		next = platformpg.EncodeCursor(last.CreatedAt, last.ID)
		items = items[:limit]
	}
	return items, next, nil
}

func (r *Repository) execAccount(ctx context.Context, what, sql string, args ...any) error {
	tag, err := r.handle.Resolve(ctx).Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("postgres: %s: %w", what, err)
	}
	if tag.RowsAffected() == 0 {
		return staff.ErrAccountNotFound
	}
	return nil
}

func (r *Repository) UpdatePassword(ctx context.Context, id, passwordHash string, mustChange bool, tempExpiresAt *time.Time, changedAt time.Time) error {
	return r.execAccount(ctx, "update staff password", `
		UPDATE staff_accounts
		SET password_hash = $2, must_change_password = $3, temp_password_expires_at = $4,
		    password_changed_at = $5, updated_at = now()
		WHERE id = $1
	`, id, passwordHash, mustChange, tempExpiresAt, changedAt)
}

func (r *Repository) LockAccount(ctx context.Context, id string) error {
	var got string
	err := r.handle.Resolve(ctx).QueryRow(ctx, `SELECT id FROM staff_accounts WHERE id = $1 FOR UPDATE`, id).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		return staff.ErrAccountNotFound
	}
	if err != nil {
		return fmt.Errorf("postgres: lock staff account: %w", err)
	}
	return nil
}

// activeSuperAdmin 是"active 的超级管理员"的条件，LockActiveSuperAdmins 和
// CountActiveSuperAdmins 共用，$1 绑定 staff.StatusActive。
const activeSuperAdmin = `super_admin AND status = $1`

func (r *Repository) CountActiveSuperAdmins(ctx context.Context) (int, error) {
	var n int
	err := r.handle.Resolve(ctx).QueryRow(ctx,
		`SELECT count(*) FROM staff_accounts WHERE `+activeSuperAdmin, staff.StatusActive).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("postgres: count staff super admins: %w", err)
	}
	return n, nil
}

// LockActiveSuperAdmins 用 FOR UPDATE 锁住符合条件的行：并发事务在这里排队，后到的一个在
// 前一个提交后重新评估 WHERE，看得到对方的停用或取消标记。
func (r *Repository) LockActiveSuperAdmins(ctx context.Context) ([]string, error) {
	rows, err := r.handle.Resolve(ctx).Query(ctx,
		`SELECT id FROM staff_accounts WHERE `+activeSuperAdmin+` ORDER BY id FOR UPDATE`, staff.StatusActive)
	if err != nil {
		return nil, fmt.Errorf("postgres: lock staff super admins: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("postgres: lock staff super admins: %w", err)
	}
	return ids, nil
}

func (r *Repository) SetStatus(ctx context.Context, id string, status staff.Status) error {
	return r.execAccount(ctx, "set staff status",
		`UPDATE staff_accounts SET status = $2, updated_at = now() WHERE id = $1`, id, status)
}

func (r *Repository) SetSuperAdmin(ctx context.Context, id string, superAdmin bool) error {
	return r.execAccount(ctx, "set staff super admin",
		`UPDATE staff_accounts SET super_admin = $2, updated_at = now() WHERE id = $1`, id, superAdmin)
}

func (r *Repository) CreateSession(ctx context.Context, s *staff.Session, tokenHash string) error {
	err := r.handle.Resolve(ctx).QueryRow(ctx, `
		INSERT INTO staff_sessions (id, token_hash, staff_id, created_at, last_used_at, expires_at, ip, user_agent, updated_at)
		VALUES ($1, $2, $3, now(), now(), $4, $5, $6, now())
		RETURNING created_at, last_used_at
	`, s.ID, tokenHash, s.StaffID, s.ExpiresAt, s.IP, s.UserAgent).Scan(&s.CreatedAt, &s.LastUsedAt)
	if err != nil {
		return fmt.Errorf("postgres: create staff session: %w", err)
	}
	return nil
}

// Authenticate 是每个后台请求都要执行的查询：会话和账号状态一次取出，不加缓存，
// 吊销、停用立即生效。
func (r *Repository) Authenticate(ctx context.Context, tokenHash string, idleTimeout time.Duration) (*staff.Principal, error) {
	var p staff.Principal
	err := r.handle.Resolve(ctx).QueryRow(ctx, `
		SELECT s.id, s.staff_id, s.last_used_at, a.super_admin, a.must_change_password
		FROM staff_sessions s JOIN staff_accounts a ON a.id = s.staff_id
		WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND s.expires_at > now()
		  AND s.last_used_at > now() - make_interval(secs => $2) AND a.status = 'active'
	`, tokenHash, idleTimeout.Seconds()).Scan(&p.SessionID, &p.StaffID, &p.LastUsedAt, &p.SuperAdmin, &p.MustChangePassword)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, staff.ErrSessionInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: authenticate staff session: %w", err)
	}
	return &p, nil
}

func (r *Repository) TouchSession(ctx context.Context, sessionID string) error {
	_, err := r.handle.Resolve(ctx).Exec(ctx, `
		UPDATE staff_sessions SET last_used_at = now(), updated_at = now()
		WHERE id = $1 AND last_used_at < now() - interval '1 minute'
	`, sessionID)
	if err != nil {
		return fmt.Errorf("postgres: touch staff session: %w", err)
	}
	return nil
}

func (r *Repository) RevokeSession(ctx context.Context, staffID, sessionID string) error {
	tag, err := r.handle.Resolve(ctx).Exec(ctx, `
		UPDATE staff_sessions SET revoked_at = COALESCE(revoked_at, now()), updated_at = now()
		WHERE id = $1 AND staff_id = $2
	`, sessionID, staffID)
	if err != nil {
		return fmt.Errorf("postgres: revoke staff session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return staff.ErrSessionNotFound
	}
	return nil
}

func (r *Repository) RevokeSessions(ctx context.Context, staffID, exceptSessionID string) error {
	_, err := r.handle.Resolve(ctx).Exec(ctx, `
		UPDATE staff_sessions SET revoked_at = now(), updated_at = now()
		WHERE staff_id = $1 AND revoked_at IS NULL AND ($2::uuid IS NULL OR id <> $2::uuid)
	`, staffID, nullable(exceptSessionID))
	if err != nil {
		return fmt.Errorf("postgres: revoke staff sessions: %w", err)
	}
	return nil
}

func (r *Repository) ListSessions(ctx context.Context, staffID string, limit int) ([]*staff.Session, error) {
	rows, err := r.handle.Resolve(ctx).Query(ctx, `
		SELECT id, staff_id, created_at, last_used_at, expires_at, revoked_at, ip, user_agent
		FROM staff_sessions WHERE staff_id = $1 ORDER BY created_at DESC LIMIT $2
	`, staffID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: list staff sessions: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*staff.Session, error) {
		var s staff.Session
		err := row.Scan(&s.ID, &s.StaffID, &s.CreatedAt, &s.LastUsedAt, &s.ExpiresAt, &s.RevokedAt, &s.IP, &s.UserAgent)
		return &s, err
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: list staff sessions: %w", err)
	}
	return out, nil
}

func (r *Repository) CreateRole(ctx context.Context, role *staff.Role) error {
	err := r.handle.Resolve(ctx).QueryRow(ctx, `
		INSERT INTO staff_roles (id, name, description, created_at, updated_at)
		VALUES ($1, $2, $3, now(), now()) RETURNING created_at, updated_at
	`, role.ID, role.Name, role.Description).Scan(&role.CreatedAt, &role.UpdatedAt)
	if platformpg.UniqueViolation(err) == "uk_staff_roles_name" {
		return staff.ErrRoleNameTaken
	}
	if err != nil {
		return fmt.Errorf("postgres: create staff role: %w", err)
	}
	return nil
}

func (r *Repository) UpdateRole(ctx context.Context, role *staff.Role) error {
	tag, err := r.handle.Resolve(ctx).Exec(ctx, `
		UPDATE staff_roles SET name = $2, description = $3, updated_at = now() WHERE id = $1
	`, role.ID, role.Name, role.Description)
	if platformpg.UniqueViolation(err) == "uk_staff_roles_name" {
		return staff.ErrRoleNameTaken
	}
	if err != nil {
		return fmt.Errorf("postgres: update staff role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return staff.ErrRoleNotFound
	}
	return nil
}

// DeleteRole 有三条语句，调用方（Service）把它包在 UnitOfWork 里。
func (r *Repository) DeleteRole(ctx context.Context, id string) error {
	q := r.handle.Resolve(ctx)
	tag, err := q.Exec(ctx, `DELETE FROM staff_roles WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("postgres: delete staff role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return staff.ErrRoleNotFound
	}
	if _, err := q.Exec(ctx, `DELETE FROM staff_role_permissions WHERE role_id = $1`, id); err != nil {
		return fmt.Errorf("postgres: delete staff role permissions: %w", err)
	}
	if _, err := q.Exec(ctx, `DELETE FROM staff_account_roles WHERE role_id = $1`, id); err != nil {
		return fmt.Errorf("postgres: delete staff role bindings: %w", err)
	}
	return nil
}

const roleSelect = `
	SELECT r.id, r.name, r.description, r.created_at, r.updated_at,
	       COALESCE(array_agg(p.permission ORDER BY p.permission) FILTER (WHERE p.permission IS NOT NULL), '{}')
	FROM staff_roles r LEFT JOIN staff_role_permissions p ON p.role_id = r.id`

func scanRole(row pgx.CollectableRow) (*staff.Role, error) {
	var role staff.Role
	err := row.Scan(&role.ID, &role.Name, &role.Description, &role.CreatedAt, &role.UpdatedAt, &role.Permissions)
	return &role, err
}

func (r *Repository) GetRole(ctx context.Context, id string) (*staff.Role, error) {
	rows, err := r.handle.Resolve(ctx).Query(ctx, roleSelect+` WHERE r.id = $1 GROUP BY r.id`, id)
	if err != nil {
		return nil, fmt.Errorf("postgres: get staff role: %w", err)
	}
	role, err := pgx.CollectExactlyOneRow(rows, scanRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, staff.ErrRoleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: get staff role: %w", err)
	}
	return role, nil
}

func (r *Repository) ListRoles(ctx context.Context) ([]*staff.Role, error) {
	rows, err := r.handle.Resolve(ctx).Query(ctx, roleSelect+` GROUP BY r.id ORDER BY r.created_at, r.id`)
	if err != nil {
		return nil, fmt.Errorf("postgres: list staff roles: %w", err)
	}
	roles, err := pgx.CollectRows(rows, scanRole)
	if err != nil {
		return nil, fmt.Errorf("postgres: list staff roles: %w", err)
	}
	return roles, nil
}

// SetRolePermissions 先删后插，调用方（Service）把它包在 UnitOfWork 里。
func (r *Repository) SetRolePermissions(ctx context.Context, roleID string, permissions []string) error {
	q := r.handle.Resolve(ctx)
	if _, err := q.Exec(ctx, `DELETE FROM staff_role_permissions WHERE role_id = $1`, roleID); err != nil {
		return fmt.Errorf("postgres: set staff role permissions: %w", err)
	}
	_, err := q.Exec(ctx, `
		INSERT INTO staff_role_permissions (role_id, permission, created_at)
		SELECT $1, p, now() FROM unnest($2::text[]) AS p
		ON CONFLICT DO NOTHING
	`, roleID, permissions)
	if err != nil {
		return fmt.Errorf("postgres: set staff role permissions: %w", err)
	}
	return nil
}

// SetAccountRoles 先删后插，只插入存在的角色；调用方（Service）把它包在 UnitOfWork 里。
func (r *Repository) SetAccountRoles(ctx context.Context, staffID string, roleIDs []string) (int, error) {
	q := r.handle.Resolve(ctx)
	if _, err := q.Exec(ctx, `DELETE FROM staff_account_roles WHERE staff_id = $1`, staffID); err != nil {
		return 0, fmt.Errorf("postgres: set staff account roles: %w", err)
	}
	tag, err := q.Exec(ctx, `
		INSERT INTO staff_account_roles (staff_id, role_id, created_at)
		SELECT $1, r.id, now() FROM staff_roles r WHERE r.id = ANY($2::uuid[])
	`, staffID, roleIDs)
	if err != nil {
		return 0, fmt.Errorf("postgres: set staff account roles: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// AccountRoleIDs 用一条查询取一页员工的角色（走主键前缀），员工列表不做 N+1 查询。
func (r *Repository) AccountRoleIDs(ctx context.Context, staffIDs []string) (map[string][]string, error) {
	rows, err := r.handle.Resolve(ctx).Query(ctx, `
		SELECT staff_id, role_id FROM staff_account_roles
		WHERE staff_id = ANY($1::uuid[]) ORDER BY staff_id, role_id`, staffIDs)
	if err != nil {
		return nil, fmt.Errorf("postgres: list staff account roles: %w", err)
	}
	out := map[string][]string{}
	var staffID, roleID string
	_, err = pgx.ForEachRow(rows, []any{&staffID, &roleID}, func() error {
		out[staffID] = append(out[staffID], roleID)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: list staff account roles: %w", err)
	}
	return out, nil
}

// HasPermission 是普通权限码的检查，每个请求执行一次，走两张关系表的主键。
func (r *Repository) HasPermission(ctx context.Context, staffID, permission string) (bool, error) {
	var ok bool
	err := r.handle.Resolve(ctx).QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM staff_account_roles ar
		                 JOIN staff_role_permissions rp ON rp.role_id = ar.role_id
		                WHERE ar.staff_id = $1 AND rp.permission = $2)
	`, staffID, permission).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("postgres: check staff permission: %w", err)
	}
	return ok, nil
}

func (r *Repository) Permissions(ctx context.Context, staffID string) ([]string, error) {
	rows, err := r.handle.Resolve(ctx).Query(ctx, `
		SELECT DISTINCT rp.permission FROM staff_account_roles ar
		JOIN staff_role_permissions rp ON rp.role_id = ar.role_id
		WHERE ar.staff_id = $1 ORDER BY rp.permission
	`, staffID)
	if err != nil {
		return nil, fmt.Errorf("postgres: list staff permissions: %w", err)
	}
	perms, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("postgres: list staff permissions: %w", err)
	}
	if perms == nil {
		perms = []string{}
	}
	return perms, nil
}

// RecordAudit 的 CreatedAt 为零时用数据库时间。
func (r *Repository) RecordAudit(ctx context.Context, e *staff.AuditEntry) error {
	var at *time.Time
	if !e.CreatedAt.IsZero() {
		at = &e.CreatedAt
	}
	err := r.handle.Resolve(ctx).QueryRow(ctx, `
		INSERT INTO staff_audit_logs (id, staff_id, login_key, action, target, result, ip, trace_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9, now()))
		RETURNING created_at
	`, e.ID, nullable(e.StaffID), e.LoginKey, e.Action, e.Target, e.Result, e.IP, e.TraceID, at).Scan(&e.CreatedAt)
	if err != nil {
		return fmt.Errorf("postgres: record staff audit: %w", err)
	}
	return nil
}

// QueryAudit 的筛选条件为零值时不生效（$n IS NULL）。
func (r *Repository) QueryAudit(ctx context.Context, f staff.AuditFilter) ([]*staff.AuditEntry, string, error) {
	if f.Limit <= 0 {
		f.Limit = staff.DefaultListLimit
	}
	createdAt, id, hasCursor, err := platformpg.DecodeCursor(f.Cursor)
	if err != nil {
		return nil, "", staff.ErrInvalidCursor
	}
	timeOrNil := func(t time.Time) *time.Time {
		if t.IsZero() {
			return nil
		}
		return &t
	}
	var cursorAt *time.Time
	var cursorID *string
	if hasCursor {
		cursorAt, cursorID = &createdAt, &id
	}
	rows, err := r.handle.Resolve(ctx).Query(ctx, `
		SELECT id, staff_id, action, target, result, ip, trace_id, created_at
		FROM staff_audit_logs
		WHERE ($1::uuid IS NULL OR staff_id = $1::uuid)
		  AND ($2::timestamptz IS NULL OR created_at >= $2)
		  AND ($3::timestamptz IS NULL OR created_at < $3)
		  AND ($4::timestamptz IS NULL OR (created_at, id) < ($4, $5::uuid))
		ORDER BY created_at DESC, id DESC
		LIMIT $6
	`, nullable(f.StaffID), timeOrNil(f.From), timeOrNil(f.To), cursorAt, cursorID, f.Limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("postgres: query staff audit: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*staff.AuditEntry, error) {
		var e staff.AuditEntry
		var staffID *string
		err := row.Scan(&e.ID, &staffID, &e.Action, &e.Target, &e.Result, &e.IP, &e.TraceID, &e.CreatedAt)
		e.StaffID = deref(staffID)
		return &e, err
	})
	if err != nil {
		return nil, "", fmt.Errorf("postgres: query staff audit: %w", err)
	}
	next := ""
	if len(items) > f.Limit {
		last := items[f.Limit-1]
		next = platformpg.EncodeCursor(last.CreatedAt, last.ID)
		items = items[:f.Limit]
	}
	return items, next, nil
}

// LoginFailures 的两部分都走 idx_staff_audit_logs_login_key_ip_action_created_at。
func (r *Repository) LoginFailures(ctx context.Context, loginKey, ip string, since time.Time) (int, time.Time, error) {
	var count int
	var last *time.Time
	err := r.handle.Resolve(ctx).QueryRow(ctx, `
		SELECT count(*), max(created_at) FROM staff_audit_logs
		WHERE login_key = $1 AND ip = $2 AND action = 'login_failed'
		  AND created_at > GREATEST($3::timestamptz, COALESCE((SELECT max(created_at) FROM staff_audit_logs
		       WHERE login_key = $1 AND ip = $2 AND action = 'login_succeeded'), '-infinity'))
	`, loginKey, ip, since).Scan(&count, &last)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("postgres: count staff login failures: %w", err)
	}
	if last == nil {
		return count, time.Time{}, nil
	}
	return count, *last, nil
}

func (r *Repository) PurgeAudit(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.handle.Resolve(ctx).Exec(ctx, `DELETE FROM staff_audit_logs WHERE created_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("postgres: purge staff audit: %w", err)
	}
	return tag.RowsAffected(), nil
}

var _ staff.Repository = (*Repository)(nil)
