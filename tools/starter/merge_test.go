package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMergeTargetReturnsNonConflictFailure(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	git := filepath.Join(bin, "git")
	if err := os.WriteFile(git, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if err := mergeTarget(root, "v0.2.0"); err == nil {
		t.Fatal("merge failure without MERGE_HEAD should stop")
	}
}

func TestMergeTargetAllowsConflictRecovery(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	git := filepath.Join(bin, "git")
	if err := os.WriteFile(git, []byte("#!/bin/sh\nmkdir -p .git\ntouch .git/MERGE_HEAD\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if err := mergeTarget(root, "v0.2.0"); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeRejectsDifferentTarget(t *testing.T) {
	root := t.TempDir()
	if err := saveUpgradeState(root, upgradeState{Target: "v0.1.0", Stage: "merge"}); err != nil {
		t.Fatal(err)
	}
	if err := upgrade(upgradeOptions{version: "v0.2.0", projectRoot: root, targetRoot: root}); err == nil {
		t.Fatal("different target should be rejected")
	}
}
