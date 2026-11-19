// Package api 把两份 spec 嵌进二进制：前台 server 返回 openapi.yaml，后台 server
// 返回 admin.yaml。embed 指令只能嵌入本包目录树内的文件，所以需要这个专门的小包。
package api

import _ "embed"

// 前端的字段文案与校验从 admin.yaml 派生，和 oapi-codegen 一样由 make generate 重新生成。
//go:generate go run ../tools/gen meta admin.yaml ../web/admin/src/api/meta.gen.ts

//go:embed openapi.yaml
var OpenAPISpec []byte

//go:embed admin.yaml
var AdminSpec []byte
