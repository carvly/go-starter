package main

import (
	"os"
	"slices"
	"testing"
)

func TestApplyRemovesModulesAndRecords(t *testing.T) {
	root := testRepository(t)
	plan, err := compute(repository{root: root}, []string{"beta"})
	if err != nil {
		t.Fatal(err)
	}
	if err := apply(root, plan, applyOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"internal/modules/beta", "internal/modules/gamma", "internal/app/adapters_beta_alpha.go"} {
		if _, err := os.Stat(root + "/" + path); !os.IsNotExist(err) {
			t.Errorf("%s still exists", path)
		}
	}
	removed, err := removedModules(root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(removed, []string{"beta", "gamma"}) {
		t.Fatalf("removed = %v, want [beta gamma]", removed)
	}
}
