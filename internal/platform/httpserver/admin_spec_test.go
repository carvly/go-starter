package httpserver_test

import (
	"testing"

	"app.local/api"
	"app.local/internal/platform/httpserver"
)

func TestParseAdminOperations(t *testing.T) {
	ops, err := httpserver.ParseAdminOperations([]byte(`
tags:
  - name: things
    summary: 东西
paths:
  /things/{id}:
    parameters: []
    put:
      tags: [things]
      summary: 修改东西
      x-permission: thing:update
  /auth/login:
    post:
      tags: [staff]
      x-permission: public
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []httpserver.AdminOperation{
		{Method: "POST", Route: "/admin/v1/auth/login", Permission: "public", Tag: "staff"},
		{Method: "PUT", Route: "/admin/v1/things/:id", Permission: "thing:update", Tag: "things", Summary: "修改东西", TagSummary: "东西"},
	}
	if len(ops) != len(want) {
		t.Fatalf("ops = %+v", ops)
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Errorf("ops[%d] = %+v, want %+v", i, ops[i], want[i])
		}
	}
}

// 内嵌的 admin.yaml 必须能解析：组合根启动时依赖它构造权限映射。
func TestParseAdminOperations_EmbeddedSpec(t *testing.T) {
	ops, err := httpserver.ParseAdminOperations(api.AdminSpec)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Permission == "" || op.Tag == "" {
			t.Errorf("%s %s 缺少 x-permission 或 tag", op.Method, op.Route)
		}
		// 角色权限页用 tag 和操作的 summary 说明权限码，缺了就只剩原始的代码。
		if op.Summary == "" || op.TagSummary == "" {
			t.Errorf("%s %s 缺少 summary，或 tag %q 没有在顶层 tags 里写 summary", op.Method, op.Route, op.Tag)
		}
	}
}
