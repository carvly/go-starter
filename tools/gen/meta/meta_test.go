package meta_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"app.local/api"
	"app.local/tools/gen/meta"
)

// 提交的 meta.gen.ts 必须和 admin.yaml 一致：改了 spec 忘记重新生成，前端的文案和校验会和接口
// 悄悄脱节。admin.yaml 不符合约定（缺 title、缺枚举文案……）时这里也会失败并列出改法。
func TestGenerate_CommittedFileUpToDate(t *testing.T) {
	got, err := meta.Generate(api.AdminSpec)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../../web/admin/src/api/meta.gen.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("web/admin/src/api/meta.gen.ts 与 api/admin.yaml 不一致：跑 make generate 后一起提交")
	}
}

const header = `
openapi: 3.2.0
paths: {}
components:
  schemas:
`

func generate(t *testing.T, spec string) (string, error) {
	t.Helper()
	out, err := meta.Generate([]byte(spec))
	return string(out), err
}

func TestGenerate_ReportsAllProblemsWithFix(t *testing.T) {
	_, err := generate(t, header+`
    Status:
      type: string
      enum: [on, off]
      x-enum-labels:
        on: 开
        gone: 没了
    Thing:
      type: object
      properties:
        name:
          type: string
        price_cents:
          title: 价格
          type: integer
        state:
          title: 状态
          type: string
          enum: [a]
        remark:
          title: 备注
          type: integer
          x-field: text
`)
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{
		"components.schemas.Status：x-enum-labels 里的 gone 不是枚举取值",
		"components.schemas.Status：枚举取值 off 在 x-enum-labels 里没有文案",
		"components.schemas.Thing.properties.name：缺少 title",
		"components.schemas.Thing.properties.price_cents：金额字段要同时满足两条",
		"components.schemas.Thing.properties.state：枚举缺少 x-enum-labels",
		"components.schemas.Thing.properties.remark：x-field: text 只能用于 type: string",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报错里没有 %q：\n%v", want, err)
		}
	}
	for line := range strings.SplitSeq(err.Error(), "\n") {
		if !strings.Contains(line, "改法：") {
			t.Errorf("报错没有写改法：%s", line)
		}
	}
}

func TestGenerate_FieldsFormsAndSearch(t *testing.T) {
	out, err := generate(t, `
openapi: 3.2.0
paths:
  /things:
    get:
      operationId: listThings
      parameters:
        - name: status
          in: query
          schema:
            type: array
            items:
              $ref: "#/components/schemas/Status"
        - name: from
          in: query
          schema: { type: string, format: date-time }
        - name: q
          in: query
          schema: { type: string }
        - name: sort
          in: query
          schema: { type: string, enum: [name, -name] }
        - name: flag
          in: query
          schema: { type: boolean }
        - $ref: "#/components/parameters/Page"
        - $ref: "#/components/parameters/Size"
    post:
      operationId: createThing
      requestBody:
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/ThingRequest"
components:
  parameters:
    Page:
      name: page
      in: query
      schema: { type: integer, default: 1 }
    Size:
      name: size
      in: query
      schema: { type: integer, default: 50 }
  schemas:
    Status:
      type: string
      enum: [on, off]
      x-enum-labels: { on: 开, off: 关 }
    Thing:
      type: object
      properties:
        status:
          $ref: "#/components/schemas/Status"
          title: 状态
    ThingRequest:
      type: object
      required: [name, price_cents, secret, version, due]
      properties:
        name: { title: 名称, type: string, maxLength: 20 }
        price_cents: { title: 价格, type: integer, minimum: 1, x-field: money }
        count: { title: 数量, type: integer, minimum: 0 }
        secret: { title: 口令, type: string, format: password, minLength: 8 }
        status: { $ref: "#/components/schemas/Status", title: 状态 }
        version: { title: 版本号, type: integer }
        due: { title: 截止日期, type: string, format: date }
        at: { title: 时间, type: string, format: date-time }
`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`labels: { on: "开", off: "关" }`,
		`price_cents: { title: "价格", min: 1 }`,
		`secret: { title: "口令", min: 8 }`,
		`status: { title: "状态", labels: statusMeta.labels }`,
		`name: textInput({ title: "名称", required: true, max: 20 })`,
		`price_cents: moneyInput({ title: "价格", required: true, min: 1 })`,
		`count: intInput({ title: "数量", min: 0 })`,
		`secret: textInput({ title: "口令", required: true, min: 8, trim: false })`,
		`status: enumInput(statusMeta.values, { title: "状态" })`,
		`due: dateInput({ title: "截止日期", required: true })`,
		`at: dateTimeInput({ title: "时间" })`,
		`sort: enumSearch(["name", "-name"])`,
		`flag: boolSearch`,
		`version: z.number().int()`,
		`status: enumListSearch(statusMeta.values)`,
		`from: daySearch`,
		`q: textSearch`,
		`page: pageSearch(50)`,
		`pageSize: 50`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("输出里没有 %q：\n%s", want, out)
		}
	}
	if strings.Contains(out, "size:") {
		t.Errorf("每页条数不应该进 URL：\n%s", out)
	}
}

func TestGenerate_UnsupportedWritingFails(t *testing.T) {
	_, err := generate(t, `
openapi: 3.2.0
paths:
  /things:
    get:
      operationId: listThings
      parameters:
        - name: n
          in: query
          schema: { type: integer }
    post:
      requestBody:
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/ThingRequest"
components:
  schemas:
    ThingRequest:
      type: object
      properties:
        due: { title: 截止时间, type: object }
`)
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{
		"ThingRequest.properties.due：表单校验不支持 type: object、format: ",
		"paths./things.get.parameters.n：列表的查询参数不支持 type: integer",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报错里没有 %q：\n%v", want, err)
		}
	}
}

// 输出必须确定：同样的 spec 生成的内容逐字节相同，否则 make generate 会产生无意义的 diff。
func TestGenerate_Deterministic(t *testing.T) {
	a, err := meta.Generate(api.AdminSpec)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		b, _ := meta.Generate(api.AdminSpec)
		if !bytes.Equal(a, b) {
			t.Fatal("两次生成的内容不同")
		}
	}
}
