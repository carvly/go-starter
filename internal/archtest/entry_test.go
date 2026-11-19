package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 接入包只能经 Service 写数据。TestBoundaries 禁止它们 import 本模块的 postgres，
// 但 Repository 接口定义在 domain 里，module.go 仍可以把 Repository 当作
// domain.Repository 注入给 handler；这里再禁止接入包的源码引用 domain 的 Repository 接口，
// 两项合起来，接入包拿得到的写入口就只剩 Service。
func TestEntryPackages_DoNotReferenceRepository(t *testing.T) {
	modsDir := filepath.Join(repoRoot(), "internal", "modules")
	mods, err := os.ReadDir(modsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mods {
		if !m.IsDir() {
			continue
		}
		repos := repositoryInterfaces(t, filepath.Join(modsDir, m.Name(), "domain"))
		if len(repos) == 0 {
			continue
		}
		domainPath := modulePath + "/internal/modules/" + m.Name() + "/domain"
		for _, sub := range entrySubs {
			dir := filepath.Join(modsDir, m.Name(), sub)
			if _, err := os.Stat(dir); err != nil {
				continue
			}
			walkGoFiles(t, dir, func(path string) {
				for _, v := range repositoryRefs(t, path, domainPath, repos) {
					rel, _ := filepath.Rel(repoRoot(), path)
					t.Errorf("%s:%d 引用了 %s：接入包只能经本模块的 Service 读写数据，"+
						"绕过它就绕过了校验、缓存失效和 AfterCommit。需要的能力加到 Service 上，handler/job 只接收 *Service", rel, v.line, v.name)
				}
			})
		}
	}
}

// repositoryInterfaces 返回 dir（模块的 domain 包）里名字以 Repository 结尾的接口。
func repositoryInterfaces(t *testing.T, dir string) map[string]bool {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	repos := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts := spec.(*ast.TypeSpec)
				if _, ok := ts.Type.(*ast.InterfaceType); ok && strings.HasSuffix(ts.Name.Name, "Repository") {
					repos[ts.Name.Name] = true
				}
			}
		}
	}
	return repos
}

type repoRef struct {
	line int
	name string
}

// repositoryRefs 找出 file 里形如 <domain 的 import 名>.<Repository 接口> 的引用。
func repositoryRefs(t *testing.T, file, domainPath string, repos map[string]bool) []repoRef {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	local := ""
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == domainPath {
			local = "domain"
			if imp.Name != nil {
				local = imp.Name.Name
			}
		}
	}
	if local == "" || local == "_" {
		return nil
	}
	if local == "." {
		return []repoRef{{fset.Position(f.Package).Line, "以点导入的 domain 包（无法静态区分 Repository 引用，改用具名导入）"}}
	}
	var refs []repoRef
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == local && repos[sel.Sel.Name] {
			refs = append(refs, repoRef{fset.Position(sel.Pos()).Line, local + "." + sel.Sel.Name})
		}
		return true
	})
	return refs
}

func TestRepositoryRefs_FindsAliasedReference(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "handler.go")
	src := `package httpapi

import alpha "app.local/internal/modules/alpha/domain"

type Handler struct {
	svc  *alpha.Service
	repo alpha.Repository
}
`
	if err := os.WriteFile(file, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	refs := repositoryRefs(t, file, modulePath+"/internal/modules/alpha/domain", map[string]bool{"Repository": true})
	if len(refs) != 1 || refs[0].line != 7 || refs[0].name != "alpha.Repository" {
		t.Errorf("refs = %+v, want 第 7 行的 alpha.Repository", refs)
	}
}

func TestCheck_EntryPackagesCannotImportOwnPostgres(t *testing.T) {
	m := modulePath + "/internal/modules/"
	tests := []struct {
		from, to string
		testOnly bool
		denied   bool
	}{
		{m + "alpha/httpapi", m + "alpha/postgres", false, true},
		{m + "alpha/httpapi", m + "alpha/postgres", true, true},
		{m + "alpha/httpapi/oapi", m + "alpha/postgres", false, true},
		{m + "alpha/adminapi", m + "alpha/postgres", false, true},
		{m + "gamma/jobs", m + "gamma/postgres", false, true},
		{m + "alpha", m + "alpha/postgres", false, false},
		{m + "alpha/postgres", m + "alpha/postgres/pgtest", true, false},
		{m + "alpha/httpapi", m + "alpha/domain", false, false},
	}
	for _, tt := range tests {
		if got := check(tt.from, tt.to, tt.testOnly) != ""; got != tt.denied {
			t.Errorf("check(%s → %s, testOnly=%v) denied = %v, want %v", trim(tt.from), trim(tt.to), tt.testOnly, got, tt.denied)
		}
	}
}
