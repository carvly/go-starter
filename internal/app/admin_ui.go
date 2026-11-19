package app

import admin "app.local/web/admin"

// 后台前端的挂载在这个文件里。目前不支持单独去掉后台前端：api/embed.go 里的生成
// 指令会写 web/admin/src/api/meta.gen.ts，tools/gen/meta 的测试读它，
// admin_ui_test.go 也要求 adminStatic 不为 nil。
func init() { adminStatic = admin.Dist() }
