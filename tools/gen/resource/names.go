package resource

import (
	"fmt"
	"regexp"
	"strings"
)

// Names 是由 module、resource、plural 推出的名字，描述文件里没有对应的键，也就不会写得和约定
// 不一致。gen plan 的第一段列出它们。
type Names struct {
	Module      string `json:"module"`
	Resource    string `json:"resource"`
	Plural      string `json:"plural"`
	Table       string `json:"table"`
	Tag         string `json:"tag"`
	Path        string `json:"path"`
	Type        string `json:"go_type"`
	ModuleField string `json:"module_field"`
	ModuleVar   string `json:"module_var"`
	Permission  string `json:"permission_prefix"`
	Spec        string `json:"spec"`
	APIPackage  string `json:"api_package"`
	OpList      string `json:"op_list"`
	OpGet       string `json:"op_get"`
	OpCreate    string `json:"op_create"`
	OpUpdate    string `json:"op_update,omitempty"`
	OpDelete    string `json:"op_delete,omitempty"`
}

// DeriveNames 推出名字。admin 为 true 时是后台接入面。
func DeriveNames(d *Descriptor) Names {
	plural := d.Plural
	if plural == "" {
		plural = pluralize(d.Resource)
	}
	t, pt := pascal(d.Resource), pascal(plural)
	n := Names{
		Module: d.Module, Resource: d.Resource, Plural: plural,
		Table: plural, Tag: strings.ReplaceAll(plural, "_", "-"),
		Type: t, ModuleField: pascal(d.Module), ModuleVar: d.Module + "M",
		Permission: d.Resource,
		OpList:     "list" + pt, OpGet: "get" + t, OpCreate: "create" + t,
	}
	n.Path = "/" + n.Tag
	if d.Admin != nil {
		n.Spec, n.APIPackage = "api/admin.yaml", "adminapi"
		if hasOp(d.Admin.Operations, "update") {
			n.OpUpdate = "update" + t
		}
		if hasOp(d.Admin.Operations, "delete") {
			n.OpDelete = "delete" + t
		}
	} else {
		n.Spec, n.APIPackage = "api/openapi.yaml", "httpapi"
	}
	return n
}

// ActionOp 是动作的 operationId，如 disableCategory、adjustStockCategory。
func (n Names) ActionOp(action string) string { return camel(action) + n.Type }

// ActionPath 是动作的路径，如 /categories/{id}/adjust-stock。
func (n Names) ActionPath(action string) string {
	return fmt.Sprintf("%s/{id}/%s", n.Path, strings.ReplaceAll(action, "_", "-"))
}

func hasOp(ops []string, op string) bool {
	for _, o := range ops {
		if o == op {
			return true
		}
	}
	return false
}

func pluralize(s string) string {
	switch {
	case strings.HasSuffix(s, "y") && len(s) > 1 && !strings.ContainsRune("aeiou", rune(s[len(s)-2])):
		return s[:len(s)-1] + "ies"
	case strings.HasSuffix(s, "s"), strings.HasSuffix(s, "x"), strings.HasSuffix(s, "z"),
		strings.HasSuffix(s, "ch"), strings.HasSuffix(s, "sh"):
		return s + "es"
	}
	return s + "s"
}

// pascal 把 snake_case 换成 PascalCase，和 oapi-codegen 给属性、参数起的 Go 名字一致
// （price_cents → PriceCents，id → Id）。
func pascal(s string) string {
	var b strings.Builder
	for part := range strings.SplitSeq(s, "_") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return b.String()
}

func camel(s string) string {
	p := pascal(s)
	if p == "" {
		return ""
	}
	return strings.ToLower(p[:1]) + p[1:]
}

var reTopName = regexp.MustCompile(`(?m)^(module|resource|plural):(\s*)([a-z][a-z0-9_]*)`)

// FixtureNames 给描述文件的 module、resource、plural 加上前缀 fx，存为回归夹具用：真实使用过的
// 描述文件，模块、表和接口在仓库里都已经存在，原样重新生成会和它们冲突。夹具检查的是生成结果的
// 形状，换名字不影响。
func FixtureNames(data []byte) []byte {
	return reTopName.ReplaceAllFunc(data, func(m []byte) []byte {
		g := reTopName.FindSubmatch(m)
		prefix := "fx_"
		if string(g[1]) == "module" {
			prefix = "fx"
		}
		if strings.HasPrefix(string(g[3]), prefix) {
			return m
		}
		return []byte(string(g[1]) + ":" + string(g[2]) + prefix + string(g[3]))
	})
}
