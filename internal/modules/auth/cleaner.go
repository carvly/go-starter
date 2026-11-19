package auth

import "context"

// UserDataCleaner 由持有前台用户数据的模块实现，必须幂等。
type UserDataCleaner interface {
	DeleteUserData(ctx context.Context, userID string) error
}

// AddUserDataCleaner 在组合根完成模块构造后登记注销清理器。
func (m *Module) AddUserDataCleaner(c UserDataCleaner) {
	m.userDataCleaners = append(m.userDataCleaners, c)
}
