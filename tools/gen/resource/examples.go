package resource

import _ "embed"

// 两份带注释的完整描述文件：gen example 打印它们，测试也用它们（必须通过校验并能生成）。
var (
	//go:embed examples/admin.yaml
	ExampleAdmin []byte
	//go:embed examples/web.yaml
	ExampleWeb []byte
)
