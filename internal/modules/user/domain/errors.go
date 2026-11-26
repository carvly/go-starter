package domain

import (
	"app.local/internal/kit/authn"
	"app.local/internal/kit/errs"
)

// message 原样显示在客户端（前台用户直接看），所以用中文；Code 供客户端分支处理。
var (
	ErrNotFound = errs.New(errs.KindNotFound, "user_not_found", "用户不存在")
	// ErrInvalidState 是动作的前置条件不满足（如已经停用的再停用）。
	ErrInvalidState = errs.New(errs.KindConflict, "user_invalid_state", "用户当前的状态不能执行这个操作，请刷新后重试")

	// 登录与会话。
	ErrAccountUnavailable = errs.New(errs.KindForbidden, "account_unavailable", "账号已被停用，请联系客服")
	ErrLoginFailed        = errs.New(errs.KindUnauthorized, "login_failed", "登录失败，请重试")
	// 前台鉴权的错误由会话中间件和网关身份中间件共同返回，定义在 kit/authn，这里引用同一个值。
	ErrInvalidToken    = authn.ErrInvalidToken
	ErrSessionReplaced = authn.ErrSessionReplaced
	ErrReauthRequired  = authn.ErrReauthRequired
	// ErrReturnToInvalid 只在 server 配置错误时才会出现，返回给客户端的是同一个文案。
	ErrReturnToInvalid = errs.New(errs.KindInvalid, "invalid_return_to", "登录跳转地址不合法")

	ErrIdentityNotFound   = errs.New(errs.KindNotFound, "identity_not_found", "登录方式不存在")
	ErrSessionNotFound    = errs.New(errs.KindNotFound, "session_not_found", "会话不存在")
	ErrAlreadyBound       = errs.New(errs.KindConflict, "identity_bound", "该微信已绑定其它账号")
	ErrLastIdentity       = errs.New(errs.KindConflict, "last_identity", "不能解绑唯一的登录方式")
	ErrReauthWrongAccount = errs.New(errs.KindUnauthorized, "reauth_wrong_account", "重新验证失败：该微信不属于当前账号")
	ErrNothingToUpdate    = errs.New(errs.KindInvalid, "invalid_request", "没有要修改的字段")
	ErrInvalidCursor      = errs.New(errs.KindInvalid, "invalid_cursor", "分页参数不合法，请从第一页重新加载")
)
