package domain

import "app.local/internal/kit/errs"

var (
	ErrInvalidInput = errs.New(errs.KindInvalid, "invalid_input", "invalid input")

	// ErrInvalidCredentials 不区分"用户名不存在""密码错误""账号已停用"，防止枚举账号。
	ErrInvalidCredentials = errs.New(errs.KindUnauthorized, "invalid_credentials", "invalid username or password")
	// ErrLoginThrottled 是连续失败后的等待期，用 WithRetryAfter 带上剩余时间。不存在的用户名
	// 一样计数、一样等待，单独返回不会暴露账号是否存在。
	ErrLoginThrottled = errs.New(errs.KindTooManyRequests, "login_throttled", "too many failed attempts, try again later")
	// ErrTemporaryPasswordExpired 只在密码正确之后返回，不泄露账号是否存在。
	ErrTemporaryPasswordExpired = errs.New(errs.KindUnauthorized, "temporary_password_expired", "temporary password expired, ask an administrator to reset it")

	// ErrSessionInvalid 不区分不存在、过期、空闲超时、已吊销、账号停用。
	ErrSessionInvalid         = errs.New(errs.KindUnauthorized, "session_invalid", "not logged in or session expired")
	ErrPasswordChangeRequired = errs.New(errs.KindForbidden, "password_change_required", "change your password before continuing")
	ErrPermissionDenied       = errs.New(errs.KindForbidden, "permission_denied", "permission denied")
	ErrWrongPassword          = errs.New(errs.KindInvalid, "wrong_password", "current password is incorrect")
	ErrLastSuperAdmin         = errs.New(errs.KindInvalid, "last_super_admin", "at least one active super admin must remain")
	ErrCannotModifySelf       = errs.New(errs.KindInvalid, "cannot_modify_self", "cannot disable yourself or remove your own super admin flag")
	ErrUnknownPermission      = errs.New(errs.KindInvalid, "unknown_permission", "unknown permission code")
	ErrInvalidCursor          = errs.New(errs.KindInvalid, "invalid_cursor", "invalid cursor")
	ErrAccountNotFound        = errs.New(errs.KindNotFound, "staff_account_not_found", "staff account not found")
	ErrRoleNotFound           = errs.New(errs.KindNotFound, "staff_role_not_found", "role not found")
	ErrSessionNotFound        = errs.New(errs.KindNotFound, "staff_session_not_found", "session not found")
	ErrUsernameTaken          = errs.New(errs.KindConflict, "username_taken", "username already taken")
	ErrRoleNameTaken          = errs.New(errs.KindConflict, "role_name_taken", "role name already taken")
)
