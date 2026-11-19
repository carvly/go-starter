package domain

import (
	"context"
	"fmt"
	"strings"
	"time"

	"app.local/internal/kit/secrand"
)

const (
	// OAuthStateTTL 是浏览器授权流程 state 的有效期。
	OAuthStateTTL = 10 * time.Minute
	// OAuthResultTTL 是登录兑换结果的有效期（回调跳回前端后要尽快兑换）。
	OAuthResultTTL = 60 * time.Second
)

// ValidReturnTo 报告 return_to 是否是合法的站内相对路径：以单个 / 开头，
// 拒绝 //host、/\host 和含协议的值（见 docs/agents/auth.md）。
func ValidReturnTo(returnTo string) bool {
	return strings.HasPrefix(returnTo, "/") &&
		!strings.HasPrefix(returnTo, "//") &&
		!strings.HasPrefix(returnTo, `/\`) &&
		!strings.Contains(returnTo, "://")
}

// StartOAuthFlow 生成 state 并写入 oauth_states（10 分钟），返回明文 state。
// bindingHash 是浏览器绑定 cookie 值的摘要（发起方生成 cookie 值并传入它的摘要），
// 防登录 CSRF；发起方负责写 cookie 和拼授权链接。
func (s *Service) StartOAuthFlow(ctx context.Context, provider string, intent OAuthFlowIntent, sid, returnTo, bindingHash string) (state string, err error) {
	if returnTo != "" && !ValidReturnTo(returnTo) {
		return "", fmt.Errorf("start oauth flow: %w", ErrReturnToInvalid)
	}
	raw := secrand.Hex(32)
	row := &OAuthState{
		IDHash:      secrand.Hash(raw),
		Kind:        OAuthStateKindState,
		Provider:    provider,
		Intent:      intent,
		SID:         sid,
		ReturnTo:    returnTo,
		BindingHash: strPtr(bindingHash),
		ExpiresAt:   time.Now().Add(OAuthStateTTL),
	}
	if err := s.states.Create(ctx, row); err != nil {
		return "", fmt.Errorf("start oauth flow: %w", err)
	}
	return raw, nil
}

// TakeOAuthState 一次性取出 state 行（不存在或已过期返回 ErrNotFound，调用方按
// 400/登录失败处理）；发起方校验绑定 cookie 的摘要和行里的 BindingHash 一致。
func (s *Service) TakeOAuthState(ctx context.Context, rawState string) (*OAuthState, error) {
	st, err := s.states.Take(ctx, secrand.Hash(rawState))
	if err != nil {
		return nil, fmt.Errorf("take oauth state: %w", err)
	}
	return st, nil
}

// NewExchangeCode 生成登录兑换码：state 前 8 位 + 新随机串，兑换时按前 8 位
// 找到绑定 cookie（见 docs/agents/auth.md 的"必须保持的安全性质"）。
func NewExchangeCode(state string) string {
	prefix := state
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	return prefix + secrand.Hex(24)
}

// ExchangeCodePrefix 返回兑换码的前 8 位（定位绑定 cookie 用）。
func ExchangeCodePrefix(code string) string {
	if len(code) <= 8 {
		return code
	}
	return code[:8]
}

// StoreOAuthResult 把登录结果写成一次性兑换（60 秒）：identity 在回调里已经换好，
// 和绑定摘要、return_to 一起暂存；兑换码明文由调用方放进跳转 URL。
func (s *Service) StoreOAuthResult(ctx context.Context, st *OAuthState, identity *ExternalIdentity, exchangeCode string) error {
	row := &OAuthState{
		IDHash:      secrand.Hash(exchangeCode),
		Kind:        OAuthStateKindResult,
		Provider:    st.Provider,
		Intent:      IntentLogin,
		ReturnTo:    st.ReturnTo,
		Identity:    identity,
		BindingHash: st.BindingHash,
		ExpiresAt:   time.Now().Add(OAuthResultTTL),
	}
	if err := s.states.Create(ctx, row); err != nil {
		return fmt.Errorf("store oauth result: %w", err)
	}
	return nil
}

// TakeOAuthResult 是兑换：一次性取出 result 行。
func (s *Service) TakeOAuthResult(ctx context.Context, code string) (*OAuthState, error) {
	st, err := s.states.Take(ctx, secrand.Hash(code))
	if err != nil {
		return nil, fmt.Errorf("take oauth result: %w", err)
	}
	return st, nil
}

func strPtr(v string) *string { return &v }
