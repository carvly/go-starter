// Package archtest 用测试强制依赖方向。规则从包路径推导，新增/删除模块不需要
// 改这里的任何配置。只有本文件里登记的例外可以绕过规则。
package archtest

import "time"

// Exception 允许 From 包导入 To 包。Reason 必须写明为什么不能按规则改代码，
// Until 是到期日：过期后测试失败，逼着例外要么被消除、要么被重新审视。
//
// 新增或延长例外属于规则变更，需要在 PR 中说明（见 AGENTS.md）。
type Exception struct {
	From   string
	To     string
	Reason string
	Until  time.Time
}

// exceptions 应该保持为空。
var exceptions = []Exception{}
