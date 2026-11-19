package httpserver

import "app.local/internal/kit/page"

// PageRequest 把后台列表的 page / size 查询参数（可选）转成规范化的分页请求，
// 各模块的 adminapi 共用，规则见 page.New。
func PageRequest(number, size *int) (page.Request, error) {
	var n, s int
	if number != nil {
		n = *number
	}
	if size != nil {
		s = *size
	}
	return page.New(n, s)
}
