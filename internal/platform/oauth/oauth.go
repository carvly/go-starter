// Package oauth 是第三方授权登录的接入层：浏览器 OAuth 跳转和原生 SDK
// 两条路径，不认识任何业务模块。auth 模块把校验通过的 Identity 转换成自己的登录方式。
// 各 provider 的测试用 httptest 模拟对方的响应，包括错误响应。
package oauth

import (
	"context"
	"errors"
)

// LoginProviderWeChat 是 user_identities.provider 的取值。
const LoginProviderWeChat = "wechat"

// Identity 是第三方身份校验通过后的结果。Subject 一律用 provider 文档里最稳定的
// 唯一 ID（微信是 unionid），不是入口级的 openid。
type Identity struct {
	Provider  string     // 登录方式 provider（wechat），不是路由里的入口名
	Subject   string     // 该 provider 下稳定的唯一 ID
	Name      string     // 昵称；微信可能为空
	AvatarURL string     // 头像；微信可能为空
	WeChat    *WeChatIDs // 仅微信：本次登录入口的 appid + openid
}

// WeChatIDs 是微信各入口的应用标识和该入口的 openid。
type WeChatIDs struct {
	AppID  string
	OpenID string
}

// 微信登录的失败分支：auth 按这些哨兵错误决定给客户端的提示和日志级别。
var (
	// ErrNoUnionID：应用没绑定到开放平台或没获得 userinfo 授权时拿不到 unionid，
	// 登录失败并报警，不退化成用 openid。
	ErrNoUnionID = errors.New("wechat: unionid missing")
	// ErrSnapshotUser：服务号网页快照页的虚拟账号（is_snapshotuser=1），一律拒绝。
	ErrSnapshotUser = errors.New("wechat: snapshot user")
	// ErrHighRiskUser：code2session 返回 40226，高风险用户被微信拦截。
	ErrHighRiskUser = errors.New("wechat: high risk user intercepted")
)

// Provider 是浏览器跳转路径：拼授权链接、code 换身份。微信不支持 PKCE，流程不生成
// verifier；PKCE 本来也防不住登录 CSRF，防登录 CSRF 靠一次性 state + 绑定 cookie。
type Provider interface {
	SupportsPKCE() bool
	AuthorizeURL(p AuthorizeParams) string
	Exchange(ctx context.Context, code, pkceVerifier, nonce string) (Identity, error)
}

// AuthorizeParams 是发起授权所需的参数。
type AuthorizeParams struct {
	State       string
	RedirectURI string
}

// Native 是 App / 小程序原生 SDK 路径：客户端提交 SDK 返回的凭证，服务端校验。
type Native interface {
	VerifyNative(ctx context.Context, cred NativeCredential) (Identity, error)
}

// NativeCredential 是原生 SDK 登录提交的凭证。Code 是微信的 code（wx.login 或
// SDK 授权返回）；IDToken 留给 Google/Apple（P2/P3），P1a 为空。
type NativeCredential struct {
	Code    string
	IDToken string
}

// Entry 是路由里的 provider 名（wechat-web、wechat-miniprogram……）对应的实现和
// 身份元数据。Provider 与 Native 二选一。
type Entry struct {
	Provider      Provider // 浏览器路径的实现；nil 表示走 Native
	Native        Native   // 原生路径的实现；nil 表示走浏览器
	LoginProvider string   // user_identities.provider 的取值
	ClientID      string   // 本次校验用的应用标识（微信是 appid）
}

// Registry 是路由 provider 名到实现的注册表，由组合方（auth 的 module.go）用配置构造。
type Registry map[string]Entry

func (r Registry) Browser(name string) (Entry, bool) {
	e, ok := r[name]
	return e, ok && e.Provider != nil
}

func (r Registry) NativeEntry(name string) (Entry, bool) {
	e, ok := r[name]
	return e, ok && e.Native != nil
}
