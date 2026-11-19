package auth

import (
	"app.local/internal/platform/oauth"
)

// wechatRegistry 只登记配了凭据的入口，subject 一律是 unionid。
func wechatRegistry(cfg WeChatConfig) oauth.Registry {
	app := func(c WeChatAppCredentials) oauth.WeChatApp {
		return oauth.WeChatApp{AppID: c.AppID, Secret: c.Secret}
	}
	r := oauth.Registry{}
	if cfg.Web.Enabled() {
		r["wechat-web"] = oauth.Entry{
			Provider:      oauth.WeChatWeb(app(cfg.Web), ""),
			LoginProvider: oauth.LoginProviderWeChat,
			ClientID:      cfg.Web.AppID,
		}
	}
	if cfg.MP.Enabled() {
		r["wechat-mp"] = oauth.Entry{
			Provider:      oauth.WeChatMP(app(cfg.MP), ""),
			LoginProvider: oauth.LoginProviderWeChat,
			ClientID:      cfg.MP.AppID,
		}
	}
	if cfg.Mini.Enabled() {
		r["wechat-miniprogram"] = oauth.Entry{
			Native:        oauth.WeChatMiniprogram(app(cfg.Mini), ""),
			LoginProvider: oauth.LoginProviderWeChat,
			ClientID:      cfg.Mini.AppID,
		}
	}
	if cfg.App.Enabled() {
		r["wechat-app"] = oauth.Entry{
			Native:        oauth.WeChatAppNative(app(cfg.App), ""),
			LoginProvider: oauth.LoginProviderWeChat,
			ClientID:      cfg.App.AppID,
		}
	}
	return r
}
