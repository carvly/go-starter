package httpserver

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v4"
)

// AdminOperation 是 api/admin.yaml 里的一个后台操作。
type AdminOperation struct {
	Method string
	// Route 是 Echo 的路由模板，带 AdminAPIPrefix，如 /admin/v1/accounts/:id，
	// 与中间件里 c.Path() 的值一致。
	Route      string
	Permission string
	Tag        string
	// Summary 是操作的 summary，TagSummary 是所属 tag 的 summary（模块的中文名），
	// 用于在角色权限页上说明每个权限码管哪些操作。
	Summary    string
	TagSummary string
}

var specParamToEcho = strings.NewReplacer("{", ":", "}", "")

// ParseAdminOperations 从 admin.yaml 得到"请求方法 + 路由模板 → x-permission"。生成的
// 注册代码不会把 operationId 带进请求上下文，所以按路由模板而不是 operationId 映射。
func ParseAdminOperations(spec []byte) ([]AdminOperation, error) {
	var s struct {
		Tags []struct {
			Name    string `yaml:"name"`
			Summary string `yaml:"summary"`
		} `yaml:"tags"`
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(spec, &s); err != nil {
		return nil, fmt.Errorf("parse admin spec: %w", err)
	}
	tagSummary := map[string]string{}
	for _, t := range s.Tags {
		tagSummary[t.Name] = t.Summary
	}
	var ops []AdminOperation
	for path, items := range s.Paths {
		for method, node := range items {
			switch method {
			case "get", "post", "put", "patch", "delete":
			default:
				continue
			}
			var op struct {
				Tags       []string `yaml:"tags"`
				Summary    string   `yaml:"summary"`
				Permission string   `yaml:"x-permission"`
			}
			if err := node.Decode(&op); err != nil {
				return nil, fmt.Errorf("parse admin spec: %s %s: %w", method, path, err)
			}
			tag := ""
			if len(op.Tags) > 0 {
				tag = op.Tags[0]
			}
			ops = append(ops, AdminOperation{
				Method:     strings.ToUpper(method),
				Route:      AdminAPIPrefix + specParamToEcho.Replace(path),
				Permission: op.Permission,
				Tag:        tag,
				Summary:    op.Summary,
				TagSummary: tagSummary[tag],
			})
		}
	}
	slices.SortFunc(ops, func(a, b AdminOperation) int {
		return cmp.Or(cmp.Compare(a.Route, b.Route), cmp.Compare(a.Method, b.Method))
	})
	return ops, nil
}
