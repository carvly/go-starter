package resource

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"q":      strconv.Quote,
	"pascal": pascal,
	"camel":  camel,
	"js":     jsString,
	"sqlq":   func(s string) string { return strings.ReplaceAll(s, "'", "''") }, // SQL 字符串字面量里的内容
	"yq": func(s string) string { // YAML 双引号字符串（同 JSON 的写法）
		b, _ := json.Marshal(s)
		return string(b)
	},
	"ternary": func(cond bool, a, b string) string {
		if cond {
			return a
		}
		return b
	},
	"has":             func(list []string, v string) bool { return slices.Contains(list, v) },
	"MetaVar":         MetaVar,
	"FieldErrorsVar":  FieldErrorsVar,
	"FormFields":      FormFields,
	"UsesEnumOptions": UsesEnumOptions,
	"titles": func(fs []*FieldM) string {
		var ts []string
		for _, f := range fs {
			ts = append(ts, f.Title)
		}
		return strings.Join(ts, "、")
	},
	"comment": func(s string) string { // 多行说明写成 Go 的文档注释，每行前加 "//\t"
		var lines []string
		for l := range strings.SplitSeq(strings.TrimSpace(s), "\n") {
			lines = append(lines, "//\t"+strings.TrimSpace(l))
		}
		return strings.Join(lines, "\n")
	},
}).ParseFS(templateFS, "templates/*.tmpl"))

// File 是一个要新建的文件。
type File struct {
	Path    string `json:"path"`
	Content string `json:"-"`
}

// Render 生成全部新文件（路径相对仓库根目录），Go 文件去掉没用到的 import 并格式化。
func Render(m *Model) ([]File, error) {
	mod := "internal/modules/" + m.N.Module + "/"
	api := mod + m.N.APIPackage + "/"
	plan := []struct{ path, tmpl string }{
		{fmt.Sprintf("%smigrations/%s_create_%s.up.sql", mod, m.Migration, m.N.Table), "migration.up.sql.tmpl"},
		{fmt.Sprintf("%smigrations/%s_create_%s.down.sql", mod, m.Migration, m.N.Table), "migration.down.sql.tmpl"},
		{mod + "migrations.go", "migrations.go.tmpl"},
		{mod + "module.yaml", "module.yaml.tmpl"},
		{mod + "module.go", "module.go.tmpl"},
		{mod + "domain/entity.go", "entity.go.tmpl"},
		{mod + "domain/errors.go", "errors.go.tmpl"},
		{mod + "domain/ports.go", "ports.go.tmpl"},
		{mod + "domain/service.go", "service.go.tmpl"},
		{mod + "domain/service_test.go", "service_test.go.tmpl"},
		{mod + "domain/domaintest/repository.go", "fake.go.tmpl"},
		{mod + "domain/domaintest/sample.go", "sample.go.tmpl"},
		{mod + "domain/domaintest/contract.go", "contract.go.tmpl"},
		{mod + "domain/domaintest/contract_test.go", "contract_test.go.tmpl"},
		{mod + "postgres/tables.go", "tables.go.tmpl"},
		{mod + "postgres/repository.go", "repository.go.tmpl"},
		{mod + "postgres/repository_integration_test.go", "repository_integration_test.go.tmpl"},
		{api + "handler.go", "handler.go.tmpl"},
		{api + "routes.go", "routes.go.tmpl"},
		{api + "http_test.go", "http_test.go.tmpl"},
		{api + "oapi/config.yaml", "oapi_config.yaml.tmpl"},
		{api + "oapi/generate.go", "oapi_generate.go.tmpl"},
	}
	if m.Admin {
		feature := AdminDir + "src/features/" + m.Feature() + "/"
		routes := AdminDir + "src/routes/_authed/" + m.Feature() + "/"
		plan = append(plan,
			struct{ path, tmpl string }{feature + "queries.ts", "web_queries.ts.tmpl"},
			struct{ path, tmpl string }{feature + "columns.tsx", "web_columns.tsx.tmpl"},
			struct{ path, tmpl string }{feature + "list-page.tsx", "web_list_page.tsx.tmpl"},
			struct{ path, tmpl string }{feature + "detail.tsx", "web_detail.tsx.tmpl"},
			struct{ path, tmpl string }{routes + "index.tsx", "web_route_index.tsx.tmpl"},
			struct{ path, tmpl string }{routes + "$id.tsx", "web_route_detail.tsx.tmpl"},
		)
		if m.HasForm() {
			plan = append(plan, struct{ path, tmpl string }{feature + "form.tsx", "web_form.tsx.tmpl"})
		}
		if len(m.Actions) > 0 || m.HasDelete {
			plan = append(plan, struct{ path, tmpl string }{feature + "actions.tsx", "web_actions.tsx.tmpl"})
		}
	}
	var files []File
	for _, p := range plan {
		out, err := execute(p.tmpl, m)
		if err != nil {
			return nil, err
		}
		if path.Ext(p.path) == ".go" {
			if out, err = tidyGo(p.path, out); err != nil {
				return nil, err
			}
		}
		files = append(files, File{Path: p.path, Content: out})
	}
	return files, nil
}

// SpecParts 是插进 spec 的三段 YAML：tags 的一项、paths、components.schemas。
func SpecParts(m *Model) (tag, paths, schemas string, err error) {
	if tag, err = execute("spec_tag.yaml.tmpl", m); err != nil {
		return
	}
	if paths, err = execute("spec_paths.yaml.tmpl", m); err != nil {
		return
	}
	schemas, err = execute("spec_schemas.yaml.tmpl", m)
	return
}

func execute(name string, m *Model) (string, error) {
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, name, m); err != nil {
		return "", fmt.Errorf("渲染 %s：%w", name, err)
	}
	return b.String(), nil
}

// tidyGo 去掉没用到的 import（模板按最全的情况写 import），再 gofmt。生成的代码格式不对
// 是生成器的 bug，带着文件内容报错方便定位。
func tidyGo(name, src string) (string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		return "", fmt.Errorf("生成器的 bug：%s 不是合法的 Go 代码：%w\n%s", name, err, numbered(src))
	}
	used := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				used[id.Name] = true
			}
		}
		return true
	})
	for _, imp := range slices.Clone(f.Imports) {
		p, _ := strconv.Unquote(imp.Path.Value)
		name := path.Base(p)
		if regexp.MustCompile(`^v[0-9]+$`).MatchString(name) { // github.com/jackc/pgx/v5 的包名是 pgx
			name = path.Base(path.Dir(p))
		}
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name == "_" || used[name] {
			continue
		}
		deleteImport(f, imp)
	}
	var b bytes.Buffer
	if err := format.Node(&b, fset, f); err != nil {
		return "", fmt.Errorf("生成器的 bug：格式化 %s：%w", name, err)
	}
	out, err := format.Source(b.Bytes())
	if err != nil {
		return "", fmt.Errorf("生成器的 bug：格式化 %s：%w", name, err)
	}
	return string(out), nil
}

func deleteImport(f *ast.File, imp *ast.ImportSpec) {
	for i, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.IMPORT {
			continue
		}
		g.Specs = slices.DeleteFunc(g.Specs, func(s ast.Spec) bool { return s == imp })
		if len(g.Specs) == 0 {
			f.Decls = slices.Delete(f.Decls, i, i+1)
		}
		break
	}
	f.Imports = slices.DeleteFunc(f.Imports, func(s *ast.ImportSpec) bool { return s == imp })
}

func numbered(src string) string {
	var b strings.Builder
	for i, l := range strings.Split(src, "\n") {
		fmt.Fprintf(&b, "%4d  %s\n", i+1, l)
	}
	return b.String()
}
