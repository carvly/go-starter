//go:build genfixture

// 夹具 CI：tools/gen/testdata 和 tools/gen/resource/examples 里的每份描述文件，在仓库的临时副本里
// 生成，然后跑全部检查。模板跟不上代码库的约定（公共包改了签名、lint 规则变严……）时这里失败。
// 后台资源还跑前端的检查，要求 web/admin 已经 pnpm install（临时副本链接到它的 node_modules）。
//
//	go test -tags genfixture -timeout 30m ./tools/gen/      # 生成、vet、全部测试、NotImplemented、lint、前端检查、删除演练
//	GEN_FIXTURE_INTEGRATION=1 go test -tags genfixture -timeout 30m ./tools/gen/   # 再跑 Postgres 上的契约测试
//	GEN_FIXTURE_SHARD=2/7 go test -tags genfixture ./tools/gen/   # 只跑编号对 7 取余等于 2 的描述文件（CI 分片并行用）
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"app.local/tools/gen/resource"
)

const golangciLint = "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0"

func TestFixtures(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(root, "tools/gen/testdata/*.yaml"))
	files = append(files, filepath.Join(root, "tools/gen/resource/examples/admin.yaml"), filepath.Join(root, "tools/gen/resource/examples/web.yaml"))
	files, err = shard(files, os.Getenv("GEN_FIXTURE_SHARD"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			p, problems := resource.Parse(f, data)
			if len(problems) > 0 {
				t.Fatalf("描述文件有错误：%v", problems)
			}
			module := "./internal/modules/" + p.D.Module + "/..."
			dir := copyRepo(t, root)
			rel, _ := filepath.Rel(root, f)
			mustRun(t, dir, "go", "run", "./tools/gen", "apply", rel)
			mustRun(t, dir, "go", "vet", "./...")
			mustRun(t, dir, "go", "test", "-skip", "NotImplemented$", "./...")
			out, err := runIn(dir, "go", "test", "-run", "NotImplemented$", "-v", module)
			stubs := strings.Count(string(data), "custom:")
			if got := strings.Count(out, "--- FAIL: "); got != stubs {
				t.Errorf("NotImplemented 测试失败了 %d 个，want %d（每个 custom 动作和每条 rules 一个）：\n%s", got, stubs, out)
			}
			if stubs == 0 && err != nil {
				t.Errorf("没有桩代码时 NotImplemented 测试不应该失败：\n%s", out)
			}
			mustRun(t, dir, "go", "run", golangciLint, "run", module, "./internal/app/...")
			if p.D.Admin != nil {
				admin := filepath.Join(dir, "web/admin")
				for _, s := range []string{"typecheck", "lint", "format:check", "test", "check:unused", "build"} {
					mustRun(t, admin, "pnpm", s)
				}
			}
			mustRun(t, dir, "scripts/check-module-removal.sh", p.D.Module)
			if os.Getenv("GEN_FIXTURE_INTEGRATION") != "" {
				mustRun(t, dir, "scripts/test-integration.sh", "-skip", "NotImplemented$", module)
			}
		})
	}
}

// shard 按 "i/n" 取出编号对 n 取余等于 i 的文件；spec 为空时返回全部。CI 固定开 n 个分片，
// 新增描述文件不用改工作流，每份都一定落在某个分片里。
func shard(files []string, spec string) ([]string, error) {
	if spec == "" {
		return files, nil
	}
	var i, n int
	if _, err := fmt.Sscanf(spec, "%d/%d", &i, &n); err != nil || n <= 0 || i < 0 || i >= n {
		return nil, fmt.Errorf("GEN_FIXTURE_SHARD=%q 应该写成 i/n，0 <= i < n", spec)
	}
	var out []string
	for k, f := range files {
		if k%n == i {
			out = append(out, f)
		}
	}
	return out, nil
}

// copyRepo 把工作区（已跟踪的和未被忽略的文件，含未提交的改动）复制到临时目录，并提交一次：
// gen apply 要求工作区干净。
func copyRepo(t *testing.T, root string) string {
	t.Helper()
	dir := t.TempDir()
	out := mustRun(t, root, "git", "ls-files", "-co", "--exclude-standard")
	for f := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		src := filepath.Join(root, f)
		info, err := os.Lstat(src)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "web/admin/node_modules"), filepath.Join(dir, "web/admin/node_modules")); err != nil {
		t.Fatal(err)
	}
	mustRun(t, dir, "git", "init", "-q")
	mustRun(t, dir, "git", "add", "-A")
	mustRun(t, dir, "git", "-c", "user.email=gen@example.com", "-c", "user.name=gen", "commit", "-qm", "base")
	return dir
}

func runIn(dir string, args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	// node_modules 是链接过来的：不让 pnpm 在运行脚本前检查并安装依赖。
	cmd.Env = append(os.Environ(), "pnpm_config_verify_deps_before_run=false")
	var b bytes.Buffer
	cmd.Stdout, cmd.Stderr = &b, &b
	err := cmd.Run()
	return b.String(), err
}

func mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runIn(dir, args...)
	if err != nil {
		t.Fatalf("%s：%v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func TestShard(t *testing.T) {
	files := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	seen := map[string]int{}
	for i := range 3 {
		got, err := shard(files, fmt.Sprintf("%d/3", i))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range got {
			seen[f]++
		}
	}
	for _, f := range files {
		if seen[f] != 1 {
			t.Errorf("%s 出现在 %d 个分片里，want 1", f, seen[f])
		}
	}
	if all, _ := shard(files, ""); len(all) != len(files) {
		t.Errorf("不分片时应该返回全部 %d 个，got %d", len(files), len(all))
	}
	for _, bad := range []string{"3/3", "-1/3", "1/0", "x"} {
		if _, err := shard(files, bad); err == nil {
			t.Errorf("GEN_FIXTURE_SHARD=%q 应该报错", bad)
		}
	}
}
