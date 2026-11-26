// Package authn 是前台鉴权的错误契约：会话中间件（user 模块）和网关身份中间件（platform）返回同一组错误值，
// 放在两边都能依赖的 kit 里，只有一份定义。Code/Message 原样返回给客户端，客户端按 Code 处理令牌
// （见 docs/agents/http.md 的"客户端约定"）。
package authn

import "app.local/internal/kit/errs"

var (
	// ErrInvalidToken：没带令牌、令牌无效或会话已失效。业务接口返回时客户端先刷新令牌再重试。
	ErrInvalidToken = errs.New(errs.KindUnauthorized, "invalid_token", "登录状态已失效，请重新登录")
	// ErrSessionReplaced：会话被新登录按登录策略顶掉。客户端提示后由用户确认再登录，不能自动重新登录，
	// 否则两台设备会互相顶下线。
	ErrSessionReplaced = errs.New(errs.KindUnauthorized, "session_replaced", "账号已在其它设备登录，请重新登录")
	// ErrReauthRequired：† 操作要求最近验证过身份。
	ErrReauthRequired = errs.New(errs.KindForbidden, "recent_auth_required", "该操作需要最近验证过身份，请重新验证后再试")
	// ErrAuthUnavailable：服务端暂时无法确认登录状态（会话校验不可用、身份断言因服务端原因验不过）。不返回 401：
	// 客户端收到 401 会清令牌，服务端的问题不能把用户登出。
	ErrAuthUnavailable = errs.New(errs.KindUnavailable, "auth_unavailable", "登录状态校验暂时不可用，请稍后重试")
)
