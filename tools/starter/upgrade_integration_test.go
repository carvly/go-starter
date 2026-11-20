//go:build integration

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func setupUpgradeRepos(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	if err := copyWorktree("../..", root); err != nil {
		t.Fatal(err)
	}
	// 升级说明由夹具决定，不跟随仓库真实的 starter-changelog.md：v0.1.0 有三个确认点的小节，测试自己打的后续 tag
	// 没有升级说明，除非测试另行写入。
	if err := os.WriteFile(filepath.Join(root, "starter-changelog.md"), []byte(upgradeChangelogFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "-qm", "base"}, {"tag", "v0.1.0"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	project := filepath.Join(t.TempDir(), "project")
	if out, err := exec.Command("git", "clone", "-q", "--no-hardlinks", root, project).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", project, "remote", "rename", "origin", "starter").CombinedOutput(); err != nil {
		t.Fatalf("rename remote: %v\n%s", err, out)
	}
	// 升级工具会 git merge，需要提交者身份；真实项目里开发者都有，CI runner 上没有全局配置。
	for _, kv := range [][2]string{{"user.email", "test@example.com"}, {"user.name", "test"}} {
		if out, err := exec.Command("git", "-C", project, "config", kv[0], kv[1]).CombinedOutput(); err != nil {
			t.Fatalf("git config %s: %v\n%s", kv[0], err, out)
		}
	}
	return root, project
}

const upgradeChangelogFixture = `# 更新日志

## v0.1.0 — 2026-11-20

### 升级说明

#### 合并前

- 夹具：合并前事项

#### 合并后迁移前

- 夹具：合并后迁移前事项

#### 迁移后

- 夹具：迁移后事项
`

func setupUpgradePostgres(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("app"),
		tcpostgres.WithUsername("app"),
		tcpostgres.WithPassword("app"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DB_DSN", dsn)
}

func commitUpgradeFixture(t *testing.T, root, message, tag string) {
	t.Helper()
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "-qm", message}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if tag != "" {
		cmd := exec.Command("git", "tag", tag)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git tag: %v\n%s", err, out)
		}
	}
}

func runUpgradeMake(project, version string) (string, error) {
	cmd := exec.Command("make", "starter-upgrade", "VERSION="+version, "CONTINUE=1")
	cmd.Dir = project
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// 每个确认点只被它自己那一次 CONTINUE=1 确认：v0.1.0 的升级说明有"合并前""合并后迁移前""迁移后"三节，
// 升级必须依次在这三处停下，各续跑一次后才完成。改动前一次 CONTINUE=1 会放行全部确认点。
func TestUpgradeStopsAtEachConfirmationOnce(t *testing.T) {
	_, project := setupUpgradeRepos(t)
	setupUpgradePostgres(t)
	statePath, err := gitPath(project, "starter-upgrade/state.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{pointPrecheck, pointBeforeMigrate, pointAfterMigrate} {
		out, err := runUpgradeMake(project, "v0.1.0")
		if err != nil {
			t.Fatalf("upgrade: %v\n%s", err, out)
		}
		state, err := loadUpgradeState(statePath)
		if err != nil {
			t.Fatal(err)
		}
		if state.Pending != want {
			t.Fatalf("应停在 %q，实际 state = %+v\n%s", want, state, out)
		}
	}
	out, err := runUpgradeMake(project, "v0.1.0")
	if err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	state, err := loadUpgradeState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Stage != "complete" || state.Target != "v0.1.0" || state.Pending != "" {
		t.Fatalf("state = %+v\n%s", state, out)
	}
}

func TestUpgradeUsesTargetVersionTool(t *testing.T) {
	root, project := setupUpgradeRepos(t)
	setupUpgradePostgres(t)
	path := filepath.Join(root, "tools/starter/upgrade.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), "for {\n\t\tfmt.Printf", "for {\n\t\tfmt.Println(\"TARGET_TOOL_MARKER\")\n\t\tfmt.Printf", 1)
	if updated == string(data) {
		t.Fatal("failed to instrument target tool")
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	commitUpgradeFixture(t, root, "instrument target", "v0.2.0")
	out, err := runUpgradeMake(project, "v0.2.0")
	if err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	if !strings.Contains(out, "TARGET_TOOL_MARKER") {
		t.Fatalf("target tool was not used:\n%s", out)
	}
}

func TestUpgradeSharedConflictContinues(t *testing.T) {
	root, project := setupUpgradeRepos(t)
	setupUpgradePostgres(t)
	if err := run(project, "go", "run", "./tools/starter", "remove", "-removed", "user"); err != nil {
		t.Fatal(err)
	}
	commitUpgradeFixture(t, project, "remove user", "")
	for name, content := range map[string]string{
		"internal/app/adapters_user_example.go":         "package app\nvar _ = struct{}{}\n",
		"web/admin/src/features/user-profiles/index.ts": "export {};\n",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(root, "internal/modules/user/module.yaml")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("  - web/admin/src/features/user-profiles\n")...)
	if err := os.WriteFile(manifest, data, 0o644); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(root, "api/openapi.yaml")
	data, err = os.ReadFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), "summary: 原生 SDK 登录", "summary: 目标版本原生 SDK 登录", 1)
	if err := os.WriteFile(spec, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	commitUpgradeFixture(t, root, "extend user", "v0.2.0")

	out, err := runUpgradeMake(project, "v0.2.0")
	if err != nil {
		t.Fatalf("expected conflict stop: %v\n%s", err, out)
	}
	if !strings.Contains(out, "api/openapi.yaml") {
		t.Fatalf("expected shared conflict: %s", out)
	}
	for _, args := range [][]string{{"checkout", "v0.2.0", "--", "api/openapi.yaml", "api/admin.yaml"}, {"add", "api/openapi.yaml", "api/admin.yaml"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = project
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	out, err = runUpgradeMake(project, "v0.2.0")
	if err != nil {
		t.Fatalf("continue: %v\n%s", err, out)
	}
	for _, path := range []string{"internal/modules/user", "internal/app/adapters_user_example.go", "web/admin/src/features/user-profiles"} {
		if _, err := os.Stat(filepath.Join(project, path)); !os.IsNotExist(err) {
			t.Errorf("%s still exists", path)
		}
	}
	statePath, err := gitPath(project, "starter-upgrade/state.json")
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadUpgradeState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Stage != "complete" {
		t.Fatalf("stage = %s", state.Stage)
	}
}

// 一次升级完成并提交后，下一次升级到更新的版本要能直接开始，基线是上一次的目标。
func TestUpgradeTwiceInSequence(t *testing.T) {
	root, project := setupUpgradeRepos(t)
	setupUpgradePostgres(t)
	statePath, err := gitPath(project, "starter-upgrade/state.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"v0.2.0", "v0.3.0"} {
		appendFile(t, filepath.Join(root, "docs/STARTER.md"), "\n"+version+"\n")
		commitUpgradeFixture(t, root, "release "+version, version)

		out, err := runUpgradeMake(project, version)
		if err != nil {
			t.Fatalf("upgrade %s: %v\n%s", version, err, out)
		}
		state, err := loadUpgradeState(statePath)
		if err != nil {
			t.Fatal(err)
		}
		if state.Stage != "complete" || state.Target != version {
			t.Fatalf("upgrade %s: state = %+v\n%s", version, state, out)
		}
		if status, err := gitOutput(project, "status", "--porcelain"); err != nil {
			t.Fatal(err)
		} else if status != "" {
			commitUpgradeFixture(t, project, "upgrade starter to "+version, "")
		}
	}
	state, err := loadUpgradeState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Base != "v0.2.0" {
		t.Fatalf("第二次升级的基线应是 v0.2.0，实际 state = %+v", state)
	}
}

// upgradeUntilComplete 反复以 CONTINUE=1 执行，直到完成；每次都应以 0 退出（停下等待也是 0）。
func upgradeUntilComplete(t *testing.T, project, version string) string {
	t.Helper()
	statePath, err := gitPath(project, "starter-upgrade/state.json")
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for range 6 {
		out, err := runUpgradeMake(project, version)
		all.WriteString(out)
		if err != nil {
			t.Fatalf("upgrade %s: %v\n%s", version, err, all.String())
		}
		state, err := loadUpgradeState(statePath)
		if err != nil {
			t.Fatal(err)
		}
		if state.Stage == "complete" {
			return all.String()
		}
	}
	t.Fatalf("upgrade %s did not complete:\n%s", version, all.String())
	return ""
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// 项目改过、合并时没有冲突的 starter 文件，升级后也要恢复为目标版本，和有冲突的文件一致。
func TestUpgradeRestoresCleanlyMergedStarterFiles(t *testing.T) {
	root, project := setupUpgradeRepos(t)
	setupUpgradePostgres(t)
	const file = "docs/STARTER.md"
	path := filepath.Join(project, file)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "# 使用 go-starter", "# 使用 go-starter（项目改过）", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	commitUpgradeFixture(t, project, "patch starter doc", "")
	appendFile(t, filepath.Join(root, file), "\n目标版本追加\n")
	commitUpgradeFixture(t, root, "release v0.2.0", "v0.2.0")

	out := upgradeUntilComplete(t, project, "v0.2.0")
	if differs, err := gitOutput(project, "diff", "--name-only", "v0.2.0", "--", file); err != nil {
		t.Fatal(err)
	} else if differs != "" {
		t.Fatalf("%s 应等于目标版本\n%s", file, out)
	}
	if !strings.Contains(out, "starter-upgrade-status: stopped point=drift") {
		t.Fatalf("应在漂移处停下：\n%s", out)
	}
}

// 业务代码引用了目标版本删除的包时，升级停在"合并后迁移前"等待修改，而不是在 go mod tidy 处失败。
func TestUpgradeStopsForCodeFixBeforeBuild(t *testing.T) {
	root, project := setupUpgradeRepos(t)
	setupUpgradePostgres(t)
	legacy := filepath.Join(root, "internal/kit/legacyx/legacyx.go")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("package legacyx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitUpgradeFixture(t, root, "add legacyx", "v0.2.0")
	gitIn(t, project, "fetch", "-q", "starter", "--tags")
	gitIn(t, project, "merge", "-q", "--ff-only", "v0.2.0")
	user := filepath.Join(project, "internal/projectx/x.go")
	if err := os.MkdirAll(filepath.Dir(user), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(user, []byte("package projectx\n\nimport _ \"app.local/internal/kit/legacyx\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitUpgradeFixture(t, project, "use legacyx", "")

	if err := os.RemoveAll(filepath.Dir(legacy)); err != nil {
		t.Fatal(err)
	}
	appendFile(t, filepath.Join(root, "starter-changelog.md"), "\n## v0.3.0 — 2026-11-21\n\n### 升级说明\n\n#### 合并后迁移前\n\n- 夹具：legacyx 已删除，改掉引用\n")
	commitUpgradeFixture(t, root, "remove legacyx", "v0.3.0")

	out, err := runUpgradeMake(project, "v0.3.0")
	if err != nil {
		t.Fatalf("应停下等待修改而不是失败：%v\n%s", err, out)
	}
	statePath, err := gitPath(project, "starter-upgrade/state.json")
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadUpgradeState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Pending != pointBeforeMigrate {
		t.Fatalf("应停在 %q，实际 state = %+v\n%s", pointBeforeMigrate, state, out)
	}
	for _, want := range []string{"夹具：legacyx 已删除，改掉引用", "starter-upgrade-status: stopped point=合并后迁移前"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q：\n%s", want, out)
		}
	}
	gitIn(t, project, "rm", "-rq", "internal/projectx")
	out = upgradeUntilComplete(t, project, "v0.3.0")
	for _, want := range []string{"git diff --cached", "starter-upgrade-status: complete target=v0.3.0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q：\n%s", want, out)
		}
	}
}

func appendFile(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// 共享 spec 里的 starter 操作被项目改过时，漂移提示要说明 spec 保留合并结果，不能说会恢复为目标版本。
func TestUpgradeDriftMessageKeepsSpecMergeResult(t *testing.T) {
	root, project := setupUpgradeRepos(t)
	setupUpgradePostgres(t)
	spec := filepath.Join(project, "api/openapi.yaml")
	data, err := os.ReadFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), "summary: 原生 SDK 登录", "summary: 项目改过的原生 SDK 登录", 1)
	if err := os.WriteFile(spec, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	commitUpgradeFixture(t, project, "patch starter operation", "")
	appendFile(t, filepath.Join(root, "docs/STARTER.md"), "\n目标版本追加\n")
	commitUpgradeFixture(t, root, "release v0.2.0", "v0.2.0")

	out := upgradeUntilComplete(t, project, "v0.2.0")
	for _, want := range []string{"spec 保留三方合并结果，不会恢复为目标版本", "starter operation changed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q：\n%s", want, out)
		}
	}
	if strings.Contains(out, "项目改过以下 starter 文件") {
		t.Fatalf("没有改过 starter 文件，不应提示文件恢复：\n%s", out)
	}
	data, err = os.ReadFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "项目改过的原生 SDK 登录") {
		t.Fatal("spec 应保留三方合并结果")
	}
}

// 会重新生成的文件（派生代码、go.sum）冲突时不拦住升级：派生文件重新生成，go.sum 由 tidy 收口。
func TestUpgradeResolvesRegeneratedConflicts(t *testing.T) {
	root, project := setupUpgradeRepos(t)
	setupUpgradePostgres(t)
	const gen = "internal/modules/user/httpapi/oapi/api.gen.go"
	for _, side := range []struct{ dir, name string }{{project, "project"}, {root, "target"}} {
		appendFile(t, filepath.Join(side.dir, gen), "\n// "+side.name+" edit\n")
		appendFile(t, filepath.Join(side.dir, "go.sum"), "example.com/"+side.name+" v1.0.0/go.mod h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n")
	}
	commitUpgradeFixture(t, project, "touch derived files", "")
	commitUpgradeFixture(t, root, "release v0.2.0", "v0.2.0")

	out := upgradeUntilComplete(t, project, "v0.2.0")
	if strings.Contains(out, "point=conflict") {
		t.Fatalf("派生文件和 go.sum 的冲突应自动处理：\n%s", out)
	}
	for _, file := range []string{gen, "go.sum"} {
		data, err := os.ReadFile(filepath.Join(project, file))
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"<<<<<<<", "project edit", "target edit", "example.com/project", "example.com/target"} {
			if strings.Contains(string(data), bad) {
				t.Fatalf("%s 应已重新生成，仍含 %q", file, bad)
			}
		}
	}
}
