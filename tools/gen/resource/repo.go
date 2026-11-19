package resource

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v4"
)

// Repo 是仓库现状：生成之前要检查的冲突，以及推出 migration 序号、go module 路径。
type Repo struct {
	Root  string
	GoMod string
}

// ErrEnv 是环境问题（不在仓库根目录、工作区不干净……），退出码 2。
var ErrEnv = errors.New("环境问题")

// OpenRepo 确认 root 是本仓库的根目录。
func OpenRepo(root string) (*Repo, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || !exists(filepath.Join(root, "tools/gen")) {
		return nil, fmt.Errorf("%w：必须在仓库根目录运行（找不到 go.mod 或 tools/gen）", ErrEnv)
	}
	m := regexp.MustCompile(`(?m)^module\s+(\S+)`).FindSubmatch(data)
	if m == nil {
		return nil, fmt.Errorf("%w：go.mod 里没有 module", ErrEnv)
	}
	return &Repo{Root: root, GoMod: string(m[1])}, nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (r *Repo) path(p string) string { return filepath.Join(r.Root, p) }

// NextMigration 是模块内下一个 migration 的序号，如 0008。
func (r *Repo) NextMigration(module string) string {
	files, _ := filepath.Glob(r.path("internal/modules/" + module + "/migrations/*.up.sql"))
	n := 0
	for _, f := range files {
		base := filepath.Base(f)
		if i := strings.IndexByte(base, '_'); i > 0 {
			if v, err := strconv.Atoi(base[:i]); err == nil {
				n = max(n, v)
			}
		}
	}
	return fmt.Sprintf("%04d", n+1)
}

// Check 检查描述文件和仓库现状是否冲突。
func (r *Repo) Check(p *Parsed) []Problem {
	c := &checker{p: p}
	d := &p.D
	if d.Module == "" || d.Resource == "" || (d.Admin == nil && d.Web == nil) {
		return nil
	}
	n := DeriveNames(d)
	if exists(r.path("internal/modules/" + d.Module)) {
		c.add("REPO-MODULE-EXISTS", "module", "模块 "+d.Module+" 已经存在", "第一版只支持新模块：换一个模块名；要在已有模块里加资源，目前只能手写（规划见 docs/proposals/0005）")
	}
	if owner := r.tableOwner(n.Table); owner != "" {
		c.add("REPO-TABLE-EXISTS", "resource", "表 "+n.Table+" 已经存在（"+owner+"）", "换一个资源名，或者写 plural 换一个表名；已有的表不经生成器（见 generator.md 的\"什么时候不用\"）")
	}
	spec, err := os.ReadFile(r.path(n.Spec))
	if err == nil {
		for _, msg := range specConflicts(spec, r.specNames(p)) {
			c.add("REPO-SPEC-CONFLICT", "resource", msg, "换一个资源名（或 plural、动作名），让推出的名字和 "+n.Spec+" 里已有的不重复")
		}
	}
	if d.Web != nil && len(d.Web.Auth) > 0 {
		modules, _ := os.ReadFile(r.path("internal/app/modules.go"))
		module, _ := os.ReadFile(r.path("internal/modules/auth/module.go"))
		switch {
		case !bytes.Contains(modules, []byte("authM := ")):
			c.add("REPO-AUTH-MISSING", "web.auth", "web.auth 需要 auth 模块提供的登录检查，但组合根里没有 auth 模块", "去掉 web.auth，或者先恢复 auth 模块")
		case !bytes.Contains(module, []byte("func (m *Module) Middleware() echo.MiddlewareFunc")):
			c.add("REPO-AUTH-MISSING", "web.auth", "web.auth 需要 auth 模块的 Middleware()（生成的代码调用 authM.Middleware()），但 internal/modules/auth/module.go 里没有它", "去掉 web.auth，或者先给 auth 模块恢复 Middleware()")
		}
	}
	return c.problems
}

func (r *Repo) tableOwner(table string) string {
	files, _ := filepath.Glob(r.path("internal/modules/*/postgres/tables.go"))
	for _, f := range files {
		data, _ := os.ReadFile(f)
		if bytes.Contains(data, []byte(strconv.Quote(table))) {
			rel, _ := filepath.Rel(r.Root, f)
			return "登记在 " + rel
		}
	}
	re := regexp.MustCompile(`(?i)create\s+table\s+(if\s+not\s+exists\s+)?"?` + regexp.QuoteMeta(table) + `"?[\s(]`)
	migs, _ := filepath.Glob(r.path("internal/modules/*/migrations/*.up.sql"))
	for _, f := range migs {
		data, _ := os.ReadFile(f)
		if re.Match(data) {
			rel, _ := filepath.Rel(r.Root, f)
			return "在 " + rel + " 里创建"
		}
	}
	return ""
}

type specNames struct {
	tag, permission string
	paths, ops      []string
	schemas         []string
}

func (r *Repo) specNames(p *Parsed) specNames {
	m := BuildModel(&p.D, r.GoMod, "0000")
	n := m.N
	s := specNames{tag: n.Tag, permission: n.Permission + ":", paths: []string{n.Path, n.Path + "/{id}"},
		ops: []string{n.OpList, n.OpGet, n.OpCreate}, schemas: []string{n.Type, n.Type + "Page", n.Type + "List", "Create" + n.Type + "Request", "Update" + n.Type + "Request"}}
	if m.HasUpdate {
		s.ops = append(s.ops, n.OpUpdate)
	}
	if m.HasDelete {
		s.ops = append(s.ops, n.OpDelete)
	}
	for _, f := range m.Enums {
		s.schemas = append(s.schemas, f.EnumType)
	}
	for _, a := range m.Actions {
		s.paths = append(s.paths, a.Path)
		s.ops = append(s.ops, a.Op)
		s.schemas = append(s.schemas, pascal(a.Name)+n.Type+"Request")
	}
	return s
}

func specConflicts(spec []byte, want specNames) []string {
	var doc struct {
		Tags []struct {
			Name string `yaml:"name"`
		} `yaml:"tags"`
		Paths      map[string]map[string]yaml.Node `yaml:"paths"`
		Components struct {
			Schemas map[string]yaml.Node `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return []string{"spec 解析失败：" + err.Error()}
	}
	var out []string
	for _, t := range doc.Tags {
		if t.Name == want.tag {
			out = append(out, "tag "+want.tag+" 已经存在")
		}
	}
	for p, ops := range doc.Paths {
		if slices.Contains(want.paths, p) {
			out = append(out, "路径 "+p+" 已经存在")
		}
		for _, node := range ops {
			var op struct {
				ID   string `yaml:"operationId"`
				Perm string `yaml:"x-permission"`
			}
			_ = node.Decode(&op)
			if slices.Contains(want.ops, op.ID) {
				out = append(out, "operationId "+op.ID+" 已经存在")
			}
			if want.permission != "" && strings.HasPrefix(op.Perm, want.permission) {
				out = append(out, "权限码前缀 "+want.permission+" 已经被 "+op.Perm+" 使用")
			}
		}
	}
	for _, s := range want.schemas {
		if _, ok := doc.Components.Schemas[s]; ok {
			out = append(out, "schema "+s+" 已经存在")
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// CheckWorkspace 检查工作区：已跟踪的文件不能有修改，要写的路径下不能有未跟踪的文件（回退时
// 会删掉这次新建的文件，不能误删别的）。其它未跟踪的文件（如个人笔记）不影响。
func (r *Repo) CheckWorkspace(paths []string) error {
	out, err := r.git("status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return fmt.Errorf("%w：%v", ErrEnv, err)
	}
	if strings.TrimSpace(out) != "" {
		return fmt.Errorf("%w：已跟踪的文件有修改，先提交或暂存（git stash）再生成，回退时才不会丢掉它们：\n%s", ErrEnv, out)
	}
	args := append([]string{"status", "--porcelain", "--"}, paths...)
	out, err = r.git(args...)
	if err != nil {
		return fmt.Errorf("%w：%v", ErrEnv, err)
	}
	if strings.TrimSpace(out) != "" {
		return fmt.Errorf("%w：要写的路径下有未跟踪的文件，先移走或删掉：\n%s", ErrEnv, out)
	}
	return nil
}

func (r *Repo) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s：%v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out), nil
}
