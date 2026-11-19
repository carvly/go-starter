package app

import (
	"fmt"
	"sync"

	"app.local/api"
	"app.local/internal/platform/httpserver"
)

// adminOperations 是内嵌 api/admin.yaml 里的后台操作（路由模板 → x-permission），
// 交给提供后台鉴权的模块做权限映射。spec 编进二进制、由测试保证能解析，解析失败
// 是构建产物本身坏了，直接 panic。
var adminOperations = sync.OnceValue(func() []httpserver.AdminOperation {
	ops, err := httpserver.ParseAdminOperations(api.AdminSpec)
	if err != nil {
		panic(fmt.Sprintf("app: embedded api/admin.yaml: %v", err))
	}
	return ops
})
