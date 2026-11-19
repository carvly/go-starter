package main

import (
	"os"
	"slices"
	"strings"
)

func starterOwnedPaths(root, target string, removed []string) ([]string, error) {
	data, err := os.ReadFile(root + "/starter-owned.txt")
	if err != nil {
		return nil, err
	}
	repo := repository{root: root, target: target}
	files, err := repo.files()
	if err != nil {
		return nil, err
	}
	owned := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, file := range files {
			if strings.HasPrefix(file, line) {
				owned[file] = true
			}
		}
	}
	var excluded []string
	if len(removed) > 0 {
		plan, err := compute(repo, removed)
		if err != nil {
			return nil, err
		}
		excluded = plan.Paths
	}
	var out []string
	for file := range owned {
		if strings.Contains(file, ".gen.") || strings.HasSuffix(file, "schema.d.ts") || strings.HasSuffix(file, "go.sum") || strings.HasSuffix(file, "pnpm-lock.yaml") || strings.HasPrefix(file, ".github/workflows/project-") {
			continue
		}
		skip := false
		for _, path := range excluded {
			if strings.HasPrefix(file, path) {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, file)
		}
	}
	slices.Sort(out)
	return out, nil
}
