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
	return root, project
}

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

func TestUpgradeCompletes(t *testing.T) {
	_, project := setupUpgradeRepos(t)
	setupUpgradePostgres(t)
	if err := upgrade(upgradeOptions{version: "v0.1.0", continueRun: true, projectRoot: project}); err != nil {
		t.Fatal(err)
	}
	state, err := loadUpgradeState(project)
	if err != nil {
		t.Fatal(err)
	}
	if state.Stage != "complete" || state.Target != "v0.1.0" {
		t.Fatalf("state = %+v", state)
	}
	if _, err := os.Stat(filepath.Join(project, ".git", "starter-upgrade", "state.json")); err != nil {
		t.Fatal(err)
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
	if err := run(project, "go", "run", "./tools/starter", "remove", "-removed", "auth"); err != nil {
		t.Fatal(err)
	}
	commitUpgradeFixture(t, project, "remove auth", "")
	for name, content := range map[string]string{
		"internal/app/adapters_auth_example.go":         "package app\nvar _ = struct{}{}\n",
		"web/admin/src/features/auth-profiles/index.ts": "export {};\n",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(root, "internal/modules/auth/module.yaml")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("  - web/admin/src/features/auth-profiles\n")...)
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
	commitUpgradeFixture(t, root, "extend auth", "v0.2.0")

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
	for _, path := range []string{"internal/modules/auth", "internal/app/adapters_auth_example.go", "web/admin/src/features/auth-profiles"} {
		if _, err := os.Stat(filepath.Join(project, path)); !os.IsNotExist(err) {
			t.Errorf("%s still exists", path)
		}
	}
	state, err := loadUpgradeState(project)
	if err != nil {
		t.Fatal(err)
	}
	if state.Stage != "complete" {
		t.Fatalf("stage = %s", state.Stage)
	}
}
