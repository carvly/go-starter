package resource

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v4"
)

// Plan 是生成之前的完整规划：推出的名字、要新建的文件、要插入的位置。写入之前先算好，任何
// 一步不成立就整体放弃，一个文件也不写。
type Plan struct {
	Names  Names     `json:"names"`
	Create []string  `json:"create"`
	Insert []Insert  `json:"insert"`
	Hints  []Problem `json:"hints,omitempty"`

	files []File
	model *Model
}

// Insert 是往共享文件里插入的一段内容：插在第 After 行之后（spec），或者按 modules.go 的约定
// 插在各自的位置（After 为 0）。
type Insert struct {
	File    string `json:"file"`
	Section string `json:"section"`
	After   int    `json:"after_line,omitempty"`
	Text    string `json:"text"`
}

// Todo 是生成之后 AI 要完成的一项。
type Todo struct {
	Kind        string `json:"kind"`
	File        string `json:"file"`
	Symbol      string `json:"symbol"`
	Instruction string `json:"instruction"`
	Test        string `json:"test,omitempty"`
	Reference   string `json:"reference,omitempty"`
}

// Result 是 apply 的结果。
type Result struct {
	*Plan
	Todo   []Todo `json:"todo"`
	Revert string `json:"revert"`
}

// MakePlan 渲染全部文件并算出插入位置（不写文件）。
func (r *Repo) MakePlan(p *Parsed, hints []Problem) (*Plan, error) {
	m := BuildModel(&p.D, r.GoMod, r.NextMigration(p.D.Module))
	files, err := Render(m)
	if err != nil {
		return nil, err
	}
	pl := &Plan{Names: m.N, Hints: hints, files: files, model: m}
	for _, f := range files {
		if exists(r.path(f.Path)) {
			return nil, fmt.Errorf("%w：%s 已经存在，一个文件也没有写", ErrEnv, f.Path)
		}
		pl.Create = append(pl.Create, f.Path)
	}
	if m.Admin {
		// 同名的手写路由文件（如 routes/_authed/catalog.tsx）会让生成的 <tag>/index.tsx 变成它的子路由，
		// 页面渲染不出来，要到运行时才发现。
		if f := AdminDir + "src/routes/_authed/" + m.Feature() + ".tsx"; exists(r.path(f)) {
			return nil, fmt.Errorf("%w：已经有路由文件 %s，和生成的 %s/ 冲突：换一个资源名（或 plural），一个文件也没有写", ErrEnv, f, m.Feature())
		}
	}
	tag, paths, schemas, err := SpecParts(m)
	if err != nil {
		return nil, err
	}
	spec, err := os.ReadFile(r.path(m.N.Spec))
	if err != nil {
		return nil, err
	}
	at, err := specSections(spec)
	if err != nil {
		return nil, fmt.Errorf("%s：%w", m.N.Spec, err)
	}
	pl.Insert = append(pl.Insert,
		Insert{File: m.N.Spec, Section: "tags", After: at["tags"], Text: tag},
		Insert{File: m.N.Spec, Section: "paths", After: at["paths"], Text: paths},
		Insert{File: m.N.Spec, Section: "components.schemas", After: at["schemas"], Text: schemas},
	)
	pl.Insert = append(pl.Insert, r.modulesInserts(m)...)
	return pl, nil
}

// specSections 返回 tags、paths、components.schemas 三段各自最后一行内容的行号：新内容插在它
// 后面。用 yaml.Node 定位，再按行插入文本，不重新序列化整份文件，已有内容的格式不变。
func specSections(spec []byte) (map[string]int, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(spec, &root); err != nil {
		return nil, err
	}
	lines := strings.Split(string(spec), "\n")
	doc := root.Content[0]
	at := map[string]int{}
	end := func(m *yaml.Node, i int) int { // 第 i 个键的值结束于下一个键之前（跳过空行和注释）
		next := len(lines) + 1
		if i+2 < len(m.Content) {
			next = m.Content[i+2].Line
		}
		l := next - 1
		for l > 0 && (strings.TrimSpace(lines[l-1]) == "" || strings.HasPrefix(strings.TrimSpace(lines[l-1]), "#")) {
			l--
		}
		return l
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		switch doc.Content[i].Value {
		case "tags", "paths":
			at[doc.Content[i].Value] = end(doc, i)
		case "components":
			c := doc.Content[i+1]
			for j := 0; j+1 < len(c.Content); j += 2 {
				if c.Content[j].Value == "schemas" {
					at["schemas"] = end(c, j)
				}
			}
		}
	}
	for _, k := range []string{"tags", "paths", "schemas"} {
		if at[k] == 0 {
			return nil, fmt.Errorf("找不到 %s 段，无法插入", k)
		}
	}
	return at, nil
}

// modulesInserts 是 internal/app/modules.go 里按硬规则 3 的固定几行。
func (r *Repo) modulesInserts(m *Model) []Insert {
	n := m.N
	deps := "DB: in.DBTx"
	if len(m.WebAuth) > 0 {
		deps += ", Auth: authM.Middleware()"
	}
	reg, list := "m."+n.ModuleField+".RegisterAdminHTTP,", "adminRegistrars"
	if !m.Admin {
		reg, list = "m."+n.ModuleField+".RegisterHTTP,", "httpRegistrars"
	}
	const f = "internal/app/modules.go"
	return []Insert{
		{File: f, Section: "import", Text: fmt.Sprintf("\t%q", r.GoMod+"/internal/modules/"+n.Module)},
		{File: f, Section: "Modules", Text: fmt.Sprintf("\t%s *%s.Module", n.ModuleField, n.Module)},
		{File: f, Section: "buildModules", Text: fmt.Sprintf("\t%s := %s.New(%s.Deps{%s})", n.ModuleVar, n.Module, n.Module, deps)},
		{File: f, Section: "return", Text: fmt.Sprintf("\t\t%s: %s,", n.ModuleField, n.ModuleVar)},
		{File: f, Section: list, Text: "\t\t" + reg},
		{File: "internal/app/migrate.go", Section: "import", Text: fmt.Sprintf("\t%q", r.GoMod+"/internal/modules/"+n.Module)},
		{File: "internal/app/migrate.go", Section: "migrationSources", Text: fmt.Sprintf("\t\t%s.Migrations,", n.Module)},
	}
}

// insertModules 按 modules.go 的约定找到每一行的位置并插入，然后 gofmt。
func insertModules(src string, ins []Insert) (string, error) {
	lines := strings.Split(src, "\n")
	find := func(section string) (int, error) {
		last := func(re *regexp.Regexp, from, to int) int {
			at := -1
			for i := from; i < to && i < len(lines); i++ {
				if re.MatchString(lines[i]) {
					at = i
				}
			}
			return at
		}
		block := func(open string) (int, int) { // open 所在行，以及它的闭合行
			for i, l := range lines {
				if strings.HasPrefix(strings.TrimSpace(l), open) {
					indent := len(l) - len(strings.TrimLeft(l, "\t"))
					for j := i + 1; j < len(lines); j++ {
						if lines[j] == strings.Repeat("\t", indent)+"}" {
							return i, j
						}
					}
				}
			}
			return -1, -1
		}
		switch section {
		case "import":
			if i := last(regexp.MustCompile(`^\t".*/internal/modules/`), 0, len(lines)); i >= 0 {
				return i + 1, nil
			}
		case "Modules":
			if _, j := block("type Modules struct {"); j >= 0 {
				return j, nil
			}
		case "buildModules":
			if i := last(regexp.MustCompile(`^\t\w+M := `), 0, len(lines)); i >= 0 {
				return i + 1, nil
			}
		case "return":
			if _, j := block("return &Modules{"); j >= 0 {
				return j, nil
			}
		case "migrationSources":
			if _, j := block("func migrationSources()"); j >= 0 {
				return j - 1, nil
			}
		default: // 注册切片
			if s, e := block("func (m *Modules) " + section + "()"); s >= 0 {
				for k := e - 1; k > s; k-- {
					if lines[k] == "\t}" {
						return k, nil
					}
				}
			}
		}
		return -1, fmt.Errorf("modules.go 里找不到 %s 的位置：它不再符合硬规则 3 的约定", section)
	}
	for _, in := range ins {
		at, err := find(in.Section)
		if err != nil {
			return "", err
		}
		lines = slices.Insert(lines, at, in.Text)
	}
	out, err := format.Source([]byte(strings.Join(lines, "\n")))
	if err != nil {
		return "", fmt.Errorf("插入后 modules.go 不能编译：%w", err)
	}
	return string(out), nil
}

// insertLines 把多段文本按行号插入（从后往前插，行号不会错位）。
func insertLines(src string, ins []Insert) string {
	lines := strings.Split(src, "\n")
	sorted := slices.Clone(ins)
	slices.SortStableFunc(sorted, func(a, b Insert) int { return b.After - a.After })
	for _, in := range sorted {
		text := strings.Split(strings.TrimRight(in.Text, "\n"), "\n")
		lines = slices.Insert(lines, in.After, text...)
	}
	return strings.Join(lines, "\n")
}

// Apply 写入文件和插入内容，然后跑生成和基本检查，最后留下快照。
func (r *Repo) Apply(pl *Plan, descriptor []byte, log func(string)) (*Result, error) {
	m := pl.model
	if err := r.CheckWorkspace(createdRoots(pl)); err != nil {
		return nil, err
	}
	res := &Result{Plan: pl, Revert: r.revert(pl)}
	// 先算好所有共享文件的新内容，全部成功再写。
	shared := map[string]string{}
	var specIns []Insert
	goIns := map[string][]Insert{}
	for _, in := range pl.Insert {
		if in.File == m.N.Spec {
			specIns = append(specIns, in)
		} else {
			goIns[in.File] = append(goIns[in.File], in)
		}
	}
	spec, err := os.ReadFile(r.path(m.N.Spec))
	if err != nil {
		return nil, err
	}
	shared[m.N.Spec] = insertLines(string(spec), specIns)
	for file, inserts := range goIns {
		content, err := os.ReadFile(r.path(file))
		if err != nil {
			return nil, err
		}
		if shared[file], err = insertModules(string(content), inserts); err != nil {
			return nil, err
		}
	}
	for _, f := range pl.files {
		if err := os.MkdirAll(filepath.Dir(r.path(f.Path)), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(r.path(f.Path), []byte(f.Content), 0o644); err != nil {
			return nil, err
		}
	}
	for p, content := range shared {
		if err := os.WriteFile(r.path(p), []byte(content), 0o644); err != nil {
			return nil, err
		}
	}
	if err := r.snapshot(pl, descriptor); err != nil {
		return nil, err
	}
	steps := [][]string{
		{"go", "generate", "./..."},
		{"go", "build", "./..."},
		{"go", "test", "./internal/archtest/", "./internal/app/"},
	}
	for _, s := range steps {
		log("$ " + strings.Join(s, " "))
		if out, err := r.run(s...); err != nil {
			return res, fmt.Errorf("%s 失败（文件已经写入，回退：%s）：\n%s", strings.Join(s, " "), res.Revert, out)
		}
	}
	if m.Admin {
		if exists(r.path(AdminDir + "node_modules")) {
			if err := r.frontend(pl, descriptor, log); err != nil {
				return res, fmt.Errorf("%w（文件已经写入，回退：%s）", err, res.Revert)
			}
		} else {
			res.Todo = append(res.Todo, Todo{Kind: "frontend", File: AdminDir + "src/api/schema.d.ts", Instruction: "web/admin 还没有 pnpm install，没能重新生成前端的接口类型和路由树、格式化生成的页面：cd web/admin && pnpm install && pnpm gen:api && pnpm gen:routes && pnpm format，再 pnpm typecheck"})
		}
	}
	res.Todo = append(res.Todo, todos(m)...)
	return res, nil
}

// frontend 重新生成接口类型和路由树，格式化生成的页面（快照随之更新为格式化后的内容），再做类型检查。
func (r *Repo) frontend(pl *Plan, descriptor []byte, log func(string)) error {
	var ts []string
	for _, f := range pl.files {
		if rel, ok := strings.CutPrefix(f.Path, AdminDir); ok {
			ts = append(ts, rel)
		}
	}
	steps := [][]string{
		{"pnpm", "gen:api"},
		{"pnpm", "gen:routes"},
		append([]string{"pnpm", "exec", "oxfmt"}, ts...),
	}
	for _, s := range steps {
		log("$ " + strings.Join(s, " ") + "（web/admin）")
		if out, err := r.runIn(AdminDir, s...); err != nil {
			return fmt.Errorf("%s 失败：\n%s", strings.Join(s, " "), out)
		}
	}
	for i, f := range pl.files {
		if strings.HasPrefix(f.Path, AdminDir) {
			data, err := os.ReadFile(r.path(f.Path))
			if err != nil {
				return err
			}
			pl.files[i].Content = string(data)
		}
	}
	if err := r.snapshot(pl, descriptor); err != nil {
		return err
	}
	log("$ pnpm typecheck（web/admin）")
	if out, err := r.runIn(AdminDir, "pnpm", "typecheck"); err != nil {
		return fmt.Errorf("pnpm typecheck 失败：生成的页面有类型错误，是生成器的 bug：\n%s", out)
	}
	return nil
}

// createdRoots 是这次新建的目录和文件：工作区检查只看它们下面有没有未跟踪的文件，回退时整个删掉。
func createdRoots(pl *Plan) []string {
	roots := []string{"internal/modules/" + pl.Names.Module}
	if pl.model.Admin {
		roots = append(roots, AdminDir+"src/features/"+pl.model.Feature(), AdminDir+"src/routes/_authed/"+pl.model.Feature())
	}
	return roots
}

func (r *Repo) runIn(dir string, args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = r.path(dir)
	// 生成器不装依赖：pnpm 在运行脚本前检查依赖是否最新，不是最新时会自动安装。
	cmd.Env = append(os.Environ(), "pnpm_config_verify_deps_before_run=false")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (r *Repo) run(args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = r.Root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// revert 是这一次的精确回退命令：删掉新建的文件，还原被插入的共享文件。不用 git clean：
// 它会删掉与这次生成无关的未跟踪文件。
func (r *Repo) revert(pl *Plan) string {
	rm := createdRoots(pl)
	co := []string{pl.Names.Spec, "internal/app/modules.go", "internal/app/migrate.go"}
	if pl.model.Admin {
		co = append(co, AdminDir+"src/api/meta.gen.ts", AdminDir+"src/api/schema.d.ts", AdminDir+"src/routeTree.gen.ts")
	}
	return "rm -r " + strings.Join(rm, " ") + " && git checkout -- " + strings.Join(co, " ")
}

// SnapshotDir 是一次生成的快照目录：gen drift 拿它和现在的代码比较。
func SnapshotDir(root, resource string) string {
	return filepath.Join(root, ".gen", resource, "snapshot")
}

func (r *Repo) snapshot(pl *Plan, descriptor []byte) error {
	dir := SnapshotDir(r.Root, pl.Names.Resource)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for _, f := range pl.files {
		p := filepath.Join(dir, "files", f.Path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(f.Content), 0o644); err != nil {
			return err
		}
	}
	meta, _ := json.MarshalIndent(pl, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "plan.json"), meta, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "descriptor.yaml"), descriptor, 0o644)
}

const customReference = "条件和更新写在同一条 UPDATE 里，没更新到行时再查一次区分不存在和条件不满足；Repository 接口、Postgres 实现、内存 fake 和契约用例一起加"

func todos(m *Model) []Todo {
	var out []Todo
	svc := "internal/modules/" + m.N.Module + "/domain/service.go"
	test := "internal/modules/" + m.N.Module + "/domain/service_test.go"
	for _, a := range m.Actions {
		if a.Declarative {
			continue
		}
		ins := a.Custom
		if len(a.Errors) > 0 {
			var es []string
			for _, e := range a.Errors {
				es = append(es, e.Var+"（"+e.Code+"）")
			}
			ins += "。会返回的错误：" + strings.Join(es, "、")
		}
		if len(a.Whens) > 0 {
			ins += "。前置条件写进同一条 SQL，不满足时返回 ErrInvalidState"
		}
		out = append(out, Todo{Kind: "custom_action", File: svc, Symbol: "(*Service)." + a.Go, Instruction: ins,
			Test: test + ": Test" + m.N.Type + "_" + a.Go + "_NotImplemented", Reference: customReference})
	}
	for _, r := range m.Rules {
		sym := "(*Service)." + r.Symbol()
		ins := r.Custom
		if len(r.Errors) > 0 {
			var es []string
			for _, e := range r.Errors {
				es = append(es, e.Var+"（"+e.Code+"）")
			}
			ins += "。会返回的错误：" + strings.Join(es, "、")
		}
		out = append(out, Todo{Kind: "rule", File: svc, Symbol: sym, Instruction: ins,
			Test:      fmt.Sprintf("%s: Test%s_Rule%d_NotImplemented", test, m.N.Type, r.N),
			Reference: "需要和写入原子完成的检查（如删除时没有引用）放进同一个事务并加行锁，见 docs/agents/transactions.md"})
	}
	return out
}

// ReadSnapshotPlan 读出快照里的规划（gen drift 用）。
func ReadSnapshotPlan(root, resource string) (*Plan, error) {
	data, err := os.ReadFile(filepath.Join(SnapshotDir(root, resource), "plan.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w：没有 %s 的快照（.gen/%s/snapshot/）：只有在这台机器上 gen apply 过才有；跨机器审核时用 tools/gen/testdata 里的描述文件在临时副本里重新生成", ErrEnv, resource, resource)
	}
	if err != nil {
		return nil, err
	}
	var pl Plan
	return &pl, json.Unmarshal(data, &pl)
}
