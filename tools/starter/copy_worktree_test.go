package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCopyWorktreePreservesExecutableMode(t *testing.T) {
	source := filepath.Join("..", "..", "scripts", "check-module-removal.sh")
	want, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if out, err := exec.Command("git", "-C", "../..", "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := copyWorktree("../..", destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.Stat(filepath.Join(destination, "scripts", "check-module-removal.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode().Perm() != want.Mode().Perm() {
		t.Fatalf("mode = %v, want %v", got.Mode().Perm(), want.Mode().Perm())
	}
}
