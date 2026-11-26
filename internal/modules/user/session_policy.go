package user

import "app.local/internal/modules/user/domain"

// 会话策略的类型，供组合根适配器实现自定义策略（规则见 domain.SessionPolicy）。
type (
	SessionPolicy    = domain.SessionPolicy
	SessionCandidate = domain.SessionCandidate
	ActiveSession    = domain.ActiveSession
	Decide           = domain.Decide
)

// SetSessionPolicy 在组合根完成模块构造后登记自定义会话策略（如按会员等级限制设备数），用独立一行登记、不带条件。
// 只能有一个策略：已经由 USER_SESSION_SLOT_LIMIT 启用了内置策略、或重复登记时 panic。
func (m *Module) SetSessionPolicy(p SessionPolicy) {
	m.Service.SetSessionPolicy(p)
}

// configuredSessionPolicy 按配置装配内置策略；没有启用时返回 nil，登录不顶掉任何会话。
func configuredSessionPolicy(cfg Config) domain.SessionPolicy {
	if cfg.SessionSlotLimit <= 0 {
		return nil
	}
	return domain.NewSlotLimit(int(cfg.SessionSlotLimit))
}
