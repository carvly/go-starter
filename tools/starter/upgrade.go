package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type upgradeState struct {
	Target               string `json:"target"`
	Base                 string `json:"base"`
	Stage                string `json:"stage"`
	ConfirmPrecheck      bool   `json:"confirm_precheck"`
	ConfirmDrift         bool   `json:"confirm_drift"`
	ConfirmBeforeMigrate bool   `json:"confirm_before_migrate"`
	ConfirmAfterMigrate  bool   `json:"confirm_after_migrate"`
}

type upgradeOptions struct {
	version     string
	continueRun bool
	projectRoot string
	targetRoot  string
}

func runUpgrade(args []string) error {
	flags := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	version := flags.String("version", "", "target version")
	continueRun := flags.Bool("continue", false, "continue after manual steps")
	projectRoot := flags.String("project-root", ".", "project root")
	targetRoot := flags.String("target-root", "", "target worktree")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *version == "" {
		return fmt.Errorf("upgrade: --version is required")
	}
	return upgrade(upgradeOptions{version: *version, continueRun: *continueRun, projectRoot: *projectRoot, targetRoot: *targetRoot})
}

func upgrade(options upgradeOptions) error {
	if options.targetRoot == "" {
		if err := run(options.projectRoot, "git", "fetch", "starter", "--tags"); err != nil {
			return err
		}
		temp, err := os.MkdirTemp("", "starter-target-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(temp) }()
		options.targetRoot = filepath.Join(temp, "target")
		if err := run(options.projectRoot, "git", "worktree", "add", "--detach", options.targetRoot, options.version); err != nil {
			return err
		}
		defer func() { _ = run(options.projectRoot, "git", "worktree", "remove", "--force", options.targetRoot) }()
	}
	state, err := loadUpgradeState(options.projectRoot)
	if err != nil {
		return err
	}
	if state.Target != "" && state.Target != options.version {
		return fmt.Errorf("upgrade: unfinished target %s", state.Target)
	}
	if state.Stage == "" {
		if out, err := gitOutput(options.projectRoot, "status", "--porcelain"); err != nil {
			return err
		} else if out != "" {
			return fmt.Errorf("upgrade: initial workspace must be clean")
		}
		if _, err := os.Stat(filepath.Join(options.projectRoot, ".git", "MERGE_HEAD")); err == nil {
			return fmt.Errorf("upgrade: initial merge already in progress")
		}
		state.Base, err = upgradeBase(options.projectRoot, options.version)
		if err != nil {
			return err
		}
		state.Stage = "precheck"
	}
	state.Target = options.version
	if err := saveUpgradeState(options.projectRoot, state); err != nil {
		return err
	}
	for {
		fmt.Printf("升级基线：%s；目标：%s\n", state.Base, state.Target)
		switch state.Stage {
		case "precheck":
			changelog, err := os.ReadFile(filepath.Join(options.targetRoot, "CHANGELOG.md"))
			if err != nil {
				return err
			}
			fmt.Println(string(changelog))
			if strings.Contains(string(changelog), "合并前") && !state.ConfirmPrecheck && !options.continueRun {
				return nil
			}
			state.ConfirmPrecheck = true
			state.Stage = "drift"
		case "drift":
			drift, err := starterFileDrift(options, state.Base)
			if err != nil {
				return err
			}
			specDrift, err := ownershipDrift(options.projectRoot, state.Base)
			if err != nil {
				return err
			}
			drift = append(drift, specDrift...)
			if len(drift) > 0 && !state.ConfirmDrift && !options.continueRun {
				for _, problem := range drift {
					fmt.Println(problem)
				}
				return nil
			}
			state.ConfirmDrift = true
			state.Stage = "merge"
		case "merge":
			if _, err := os.Stat(filepath.Join(options.projectRoot, ".git", "MERGE_HEAD")); os.IsNotExist(err) {
				if err := mergeTarget(options.projectRoot, options.version); err != nil {
					return err
				}
			}
			paths, err := starterOwnedPaths(options.targetRoot, options.version, nil)
			if err != nil {
				return err
			}
			for _, path := range paths {
				conflicted, err := gitOutput(options.projectRoot, "diff", "--name-only", "--diff-filter=U", "--", path)
				if err != nil {
					return err
				}
				if conflicted != "" {
					if err := run(options.projectRoot, "git", "checkout", options.version, "--", path); err != nil {
						return err
					}
					if err := run(options.projectRoot, "git", "add", "--", path); err != nil {
						return err
					}
				}
			}
			remaining, err := gitOutput(options.projectRoot, "diff", "--name-only", "--diff-filter=U")
			if err != nil {
				return err
			}
			if remaining != "" {
				fmt.Printf("请解决并暂存以下共享/项目文件冲突，然后用 --continue 续跑：\n%s\n", remaining)
				return nil
			}
			state.Stage = "replay"
		case "replay":
			remaining, err := gitOutput(options.projectRoot, "diff", "--name-only", "--diff-filter=U")
			if err != nil {
				return err
			}
			if remaining != "" {
				return fmt.Errorf("upgrade: unmerged paths: %s", remaining)
			}
			removed, err := removedModules(options.projectRoot)
			if err != nil {
				return err
			}
			if len(removed) > 0 {
				plan, err := compute(repository{root: options.projectRoot, target: options.version}, removed)
				if err != nil {
					return err
				}
				if err := apply(options.projectRoot, plan, applyOptions{NoRecord: true}); err != nil {
					return err
				}
			}
			for _, command := range [][]string{{"go", "mod", "tidy"}, {"go", "generate", "./..."}} {
				if err := run(options.projectRoot, command...); err != nil {
					return err
				}
			}
			if _, err := os.Stat(filepath.Join(options.projectRoot, "web/admin/package.json")); err == nil {
				if _, err := os.Stat(filepath.Join(options.projectRoot, "web/admin/node_modules")); os.IsNotExist(err) {
					if err := run(filepath.Join(options.projectRoot, "web/admin"), "pnpm", "install", "--frozen-lockfile"); err != nil {
						return err
					}
				}
				for _, command := range [][]string{{"pnpm", "gen:api"}, {"pnpm", "gen:routes"}} {
					if err := run(filepath.Join(options.projectRoot, "web/admin"), command...); err != nil {
						return err
					}
				}
			}
			state.Stage = "migrate"
		case "migrate":
			changelog, err := os.ReadFile(filepath.Join(options.targetRoot, "CHANGELOG.md"))
			if err != nil {
				return err
			}
			if strings.Contains(string(changelog), "合并后迁移前") && !state.ConfirmBeforeMigrate && !options.continueRun {
				return nil
			}
			state.ConfirmBeforeMigrate = true
			if err := run(options.projectRoot, "go", "run", "./cmd/api", "migrate", "up"); err != nil {
				return err
			}
			if strings.Contains(string(changelog), "迁移后") && !state.ConfirmAfterMigrate && !options.continueRun {
				return nil
			}
			state.ConfirmAfterMigrate = true
			state.Stage = "verify"
		case "verify":
			if err := run(options.projectRoot, "go", "test", "./..."); err != nil {
				return err
			}
			modules, err := allModules(options.projectRoot)
			if err != nil {
				return err
			}
			if err := check(options.projectRoot, modules); err != nil {
				return err
			}
			state.Stage = "complete"
		case "complete":
			changelog, err := os.ReadFile(filepath.Join(options.targetRoot, "CHANGELOG.md"))
			if err != nil {
				return err
			}
			fmt.Println(string(changelog))
			fmt.Printf("升级流程完成：%s。请按升级说明执行部署后事项。\n", state.Target)
			return saveUpgradeState(options.projectRoot, state)
		default:
			return fmt.Errorf("upgrade: unknown stage %s", state.Stage)
		}
		if err := saveUpgradeState(options.projectRoot, state); err != nil {
			return err
		}
	}
}

func mergeTarget(root, version string) error {
	if err := run(root, "git", "merge", version); err != nil {
		if _, statErr := os.Stat(filepath.Join(root, ".git", "MERGE_HEAD")); os.IsNotExist(statErr) {
			return err
		}
	}
	return nil
}

func upgradeBase(root, target string) (string, error) {
	out, err := gitOutput(root, "tag", "--merged", "HEAD", "--sort=-v:refname")
	if err != nil {
		return "", err
	}
	for _, tag := range strings.Fields(out) {
		if tag != target {
			return tag, nil
		}
	}
	return gitOutput(root, "merge-base", "HEAD", target)
}

func starterFileDrift(options upgradeOptions, base string) ([]string, error) {
	removed, err := removedModules(options.projectRoot)
	if err != nil {
		return nil, err
	}
	paths, err := starterOwnedPaths(options.targetRoot, base, removed)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, path := range paths {
		changed, err := gitOutput(options.projectRoot, "diff", "--name-only", base, "--", path)
		if err != nil {
			return nil, err
		}
		if changed != "" {
			out = append(out, changed)
		}
	}
	return out, nil
}

func loadUpgradeState(root string) (upgradeState, error) {
	var state upgradeState
	data, err := os.ReadFile(filepath.Join(root, ".git", "starter-upgrade", "state.json"))
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	return state, json.Unmarshal(data, &state)
}

func saveUpgradeState(root string, state upgradeState) error {
	dir := filepath.Join(root, ".git", "starter-upgrade")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "state.json"), append(data, '\n'), 0o644)
}

func gitOutput(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}
