package domain

import (
	"cmp"
	"context"
	"slices"
	"time"
)

// SessionPolicy 决定新登录要顶掉哪些现有会话（如"每类设备只能登录一个"）。没有登记策略时登录不做这一步。
//
// Prepare 在登录事务之前调用，可以读其它模块（经组合根适配器）准备判断需要的数据；返回的 Decide 在登录事务里调用，
// 只用参数和闭包里已准备好的数据：它拿不到 ctx，也就拿不到登录事务；约定不做任何 IO（调用时持有用户行锁）。
// 吊销、记录原因、并发控制都由 Service 完成，策略只做决定。Prepare 或 Decide 返回错误时登录失败，错误按 errs 的
// 规则返回给客户端（如拒绝新登录）。
type SessionPolicy interface {
	Prepare(ctx context.Context, c SessionCandidate) (Decide, error)
}

// Decide 返回要吊销的会话 ID，必须取自 active；返回错误表示拒绝这次登录。
type Decide func(c SessionCandidate, active []ActiveSession) (revoke []string, err error)

// SessionCandidate 是这次登录要建立的会话。
type SessionCandidate struct {
	UserID      string
	Client      string
	DeviceClass string
	DeviceName  string
}

// ActiveSession 是账号现有的一个有效会话（未吊销、未过期、未空闲超时），只含做判断需要的字段。
type ActiveSession struct {
	ID          string
	Client      string
	DeviceClass string
	DeviceName  string
	CreatedAt   time.Time
	// LastUsedAt 是最近一次刷新令牌的时间，精度约等于 access token 的有效期。
	LastUsedAt time.Time
}

// SlotLimit 是 starter 自带的策略：同一设备类别（DeviceSlot）最多保留 limit 个会话，新登录顶掉最久没用的。
type SlotLimit struct {
	limit int
}

// NewSlotLimit 的 limit 必须 > 0。
func NewSlotLimit(limit int) SlotLimit {
	if limit <= 0 {
		panic("user: NewSlotLimit: limit must be > 0")
	}
	return SlotLimit{limit: limit}
}

// Prepare 不需要事务外的数据。
func (p SlotLimit) Prepare(context.Context, SessionCandidate) (Decide, error) {
	return p.decide, nil
}

func (p SlotLimit) decide(c SessionCandidate, active []ActiveSession) ([]string, error) {
	slot := DeviceSlot(c.Client, c.DeviceClass)
	var same []ActiveSession
	for _, v := range active {
		if DeviceSlot(v.Client, v.DeviceClass) == slot {
			same = append(same, v)
		}
	}
	excess := len(same) + 1 - p.limit
	if excess <= 0 {
		return nil, nil
	}
	slices.SortFunc(same, func(a, b ActiveSession) int {
		return cmp.Or(a.LastUsedAt.Compare(b.LastUsedAt), cmp.Compare(a.ID, b.ID))
	})
	revoke := make([]string, 0, excess)
	for _, v := range same[:excess] {
		revoke = append(revoke, v.ID)
	}
	return revoke, nil
}

// DeviceSlot 是 SlotLimit 的设备类别：App 按设备形态分成 phone、tablet（不分系统），其余（web、各小程序）就是客户端本身。
func DeviceSlot(client, deviceClass string) string {
	if IsAppClient(client) {
		return deviceClass
	}
	return client
}
