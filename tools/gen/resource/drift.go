package resource

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// DriftItem 是生成之后改过的一处。Expected 为 true 的是待办清单里的（补足 custom 和 rules、
// 替换 NotImplemented 测试、为 custom 动作新增的代码），其余是生成器缺什么的直接信号。
type DriftItem struct {
	File     string `json:"file"`
	Symbol   string `json:"symbol,omitempty"`
	Kind     string `json:"kind"` // changed、removed、added、file_changed、file_removed、insert_changed
	Expected bool   `json:"expected"`
	Detail   string `json:"detail,omitempty"`
}

// Drift 比较快照和现在的代码。
func (r *Repo) Drift(resource string) ([]DriftItem, error) {
	pl, err := ReadSnapshotPlan(r.Root, resource)
	if err != nil {
		return nil, err
	}
	dir := SnapshotDir(r.Root, resource)
	desc, err := os.ReadFile(filepath.Join(dir, "descriptor.yaml"))
	if err != nil {
		return nil, err
	}
	p, _ := Parse("descriptor.yaml", desc)
	m := BuildModel(&p.D, r.GoMod, "0000")
	expected := expectedSymbols(m)

	var items []DriftItem
	for _, f := range pl.Create {
		old, err := os.ReadFile(filepath.Join(dir, "files", f))
		if err != nil {
			return nil, err
		}
		cur, err := os.ReadFile(r.path(f))
		if os.IsNotExist(err) {
			items = append(items, DriftItem{File: f, Kind: "file_removed"})
			continue
		}
		if err != nil {
			return nil, err
		}
		if bytes.Equal(old, cur) {
			continue
		}
		if filepath.Ext(f) != ".go" {
			items = append(items, DriftItem{File: f, Kind: "file_changed", Detail: lineDiff(string(old), string(cur))})
			continue
		}
		a, errA := decls(f, old)
		b, errB := decls(f, cur)
		if errA != nil || errB != nil {
			items = append(items, DriftItem{File: f, Kind: "file_changed", Detail: "解析失败，按整个文件比较"})
			continue
		}
		for _, k := range sortedKeys(a) {
			switch v, ok := b[k]; {
			case !ok:
				items = append(items, DriftItem{File: f, Symbol: k, Kind: "removed", Expected: expected(k)})
			case v != a[k]:
				items = append(items, DriftItem{File: f, Symbol: k, Kind: "changed", Expected: expected(k), Detail: lineDiff(a[k], v)})
			}
		}
		for _, k := range sortedKeys(b) {
			if _, ok := a[k]; !ok {
				// 新增的声明不是改生成的代码：为 custom 动作加的 Repository 方法、新的测试都在这里。
				items = append(items, DriftItem{File: f, Symbol: k, Kind: "added", Expected: true})
			}
		}
	}
	space := regexp.MustCompile(`\s+`)
	for _, in := range pl.Insert {
		cur, err := os.ReadFile(r.path(in.File))
		if err != nil {
			return nil, err
		}
		norm := func(s string) string { return space.ReplaceAllString(strings.TrimSpace(s), " ") }
		if !strings.Contains(norm(string(cur)), norm(in.Text)) {
			items = append(items, DriftItem{File: in.File, Symbol: in.Section, Kind: "insert_changed", Detail: "插入的内容被改过"})
		}
	}
	return items, nil
}

// expectedSymbols 是待办清单里会改的声明：custom 动作和 rules 的桩、它们的 NotImplemented 测试，
// 以及 custom 动作要用到的错误（AI 可能补充说明）。
func expectedSymbols(m *Model) func(string) bool {
	set := map[string]bool{}
	for _, a := range m.Actions {
		if !a.Declarative {
			set["Service."+a.Go] = true
			set["Test"+m.N.Type+"_"+a.Go+"_NotImplemented"] = true
		}
	}
	for _, r := range m.Rules {
		set[fmt.Sprintf("Service.checkRule%d", r.N)] = true
		set[fmt.Sprintf("Service.checkRule%dDelete", r.N)] = true
		set[fmt.Sprintf("Test%s_Rule%d_NotImplemented", m.N.Type, r.N)] = true
	}
	if m.HasNotImplemented {
		set["ErrNotImplemented"] = true
	}
	return func(k string) bool { return set[k] }
}

// decls 把 Go 文件拆成"声明名 → 源码"：函数按 接收者.名字，类型、变量、常量按各自的名字，
// import 合成一项。
func decls(name string, src []byte) (map[string]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	text := func(n ast.Node) string {
		var b bytes.Buffer
		_ = printer.Fprint(&b, fset, n)
		return b.String()
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			key := d.Name.Name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				t := d.Recv.List[0].Type
				if s, ok := t.(*ast.StarExpr); ok {
					t = s.X
				}
				if id, ok := t.(*ast.Ident); ok {
					key = id.Name + "." + key
				}
			}
			out[key] = text(d)
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				out["import"] = text(d)
				continue
			}
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					out[s.Name.Name] = text(s)
				case *ast.ValueSpec:
					for _, n := range s.Names {
						out[n.Name] = text(s)
					}
				}
			}
		}
	}
	return out, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// lineDiff 是逐行的简单差异：- 生成时的行，+ 现在的行（只列不同的行）。
func lineDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	inB := map[string]int{}
	for _, l := range bl {
		inB[l]++
	}
	inA := map[string]int{}
	for _, l := range al {
		inA[l]++
	}
	var out []string
	for _, l := range al {
		if inB[l] > 0 {
			inB[l]--
			continue
		}
		out = append(out, "- "+l)
	}
	for _, l := range bl {
		if inA[l] > 0 {
			inA[l]--
			continue
		}
		out = append(out, "+ "+l)
	}
	if len(out) > 40 {
		out = append(out[:40], fmt.Sprintf("……还有 %d 行", len(out)-40))
	}
	return strings.Join(out, "\n")
}
