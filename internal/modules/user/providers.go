package user

import (
	"app.local/internal/modules/user/domain"
	"app.local/internal/platform/oauth"
)

// nativeClients 登记原生入口建立的会话属于哪个客户端（见 httpapi.Deps.NativeClients）：小程序、小游戏各自一个 AppID，
// 入口就决定了客户端；移动应用在 Android、iOS、鸿蒙上共用一组 AppID，值为空，按 X-Client-Platform 区分。
var nativeClients = map[string]string{
	"wechat-miniprogram": domain.ClientWeChatMiniprogram,
	"wechat-app":         "",
}

// wechatRegistry 只登记配了凭据的入口，subject 一律是 unionid。user_identities.provider 取 Identity.Provider
// （微信各入口都是 oauth.ProviderWeChat）。
func wechatRegistry(cfg WeChatConfig) oauth.Registry {
	app := func(c WeChatAppCredentials) oauth.WeChatApp {
		return oauth.WeChatApp{AppID: c.AppID, Secret: c.Secret}
	}
	r := oauth.Registry{}
	if cfg.Web.Enabled() {
		r["wechat-web"] = oauth.Entry{
			Provider: oauth.WeChatWeb(app(cfg.Web), ""),
		}
	}
	if cfg.MP.Enabled() {
		r["wechat-mp"] = oauth.Entry{
			Provider: oauth.WeChatMP(app(cfg.MP), ""),
		}
	}
	if cfg.Mini.Enabled() {
		r["wechat-miniprogram"] = oauth.Entry{
			Native: oauth.WeChatMiniprogram(app(cfg.Mini), ""),
		}
	}
	if cfg.App.Enabled() {
		r["wechat-app"] = oauth.Entry{
			Native: oauth.WeChatAppNative(app(cfg.App), ""),
		}
	}
	return r
}
