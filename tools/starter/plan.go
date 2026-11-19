package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v4"
)

type DeletionSet struct {
	Modules      []string `json:"modules"`
	Paths        []string `json:"paths"`
	HTTPTags     []string `json:"http_tags"`
	AdminTags    []string `json:"admin_tags"`
	AdminSurface bool     `json:"admin_surface"`
}

type repository struct {
	root   string
	target string
}

func (r repository) read(name string) (string, error) {
	if r.target == "" {
		data, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(name)))
		return string(data), err
	}
	cmd := exec.Command("git", "-C", r.root, "show", r.target+":"+name)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git show %s:%s: %w", r.target, name, err)
	}
	return string(out), nil
}

func (r repository) files() ([]string, error) {
	if r.target != "" {
		cmd := exec.Command("git", "-C", r.root, "ls-tree", "-r", "--name-only", r.target)
		out, err := cmd.Output()
		if err != nil {
			return nil, err
		}
		return strings.Fields(string(out)), nil
	}
	var files []string
	err := filepath.WalkDir(r.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", ".gen":
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(r.root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	return files, err
}

type moduleManifest struct {
	WebPaths []string `yaml:"web_paths"`
}

func includeTag(content string) string {
	match := regexp.MustCompile(`(?m)^\s*include-tags:\s*\[([^]]+)\]`).FindStringSubmatch(content)
	if match == nil {
		return ""
	}
	return strings.ReplaceAll(match[1], " ", "")
}

func moduleRequires(content string) map[string][]string {
	out := map[string][]string{}
	start := strings.Index(content, "var moduleRequires")
	if start < 0 {
		return out
	}
	end := strings.Index(content[start:], "\n}")
	if end >= 0 {
		content = content[start : start+end]
	}
	for _, match := range regexp.MustCompile(`"([^"]+)"\s*:\s*\{([^}]*)\}`).FindAllStringSubmatch(content, -1) {
		for _, dep := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(match[2], -1) {
			out[match[1]] = append(out[match[1]], dep[1])
		}
	}
	return out
}

func closure(modules []string, deps map[string][]string, removed []string) ([]string, error) {
	known := map[string]bool{}
	for _, module := range modules {
		known[module] = true
	}
	drop := map[string]bool{}
	for _, module := range removed {
		if !known[module] {
			return nil, fmt.Errorf("starter: unknown module %q", module)
		}
		drop[module] = true
	}
	changed := true
	for changed {
		changed = false
		for _, module := range modules {
			if drop[module] {
				continue
			}
			for _, dep := range deps[module] {
				if drop[dep] {
					drop[module] = true
					changed = true
					break
				}
			}
		}
	}
	var out []string
	for module := range drop {
		out = append(out, module)
	}
	slices.Sort(out)
	return out, nil
}

func compute(repo repository, removed []string) (DeletionSet, error) {
	files, err := repo.files()
	if err != nil {
		return DeletionSet{}, err
	}
	var modules []string
	for _, name := range files {
		if strings.HasPrefix(name, "internal/modules/") && strings.HasSuffix(name, "/module.go") {
			parts := strings.Split(name, "/")
			if len(parts) == 4 {
				modules = append(modules, parts[2])
			}
		}
	}
	slices.Sort(modules)
	modulesGo, err := repo.read("internal/app/modules.go")
	if err != nil {
		return DeletionSet{}, err
	}
	deps := moduleRequires(modulesGo)
	for _, module := range modules {
		line := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(module) + `M\s*:=\s*(.*)$`).FindStringSubmatch(modulesGo)
		if line == nil {
			return DeletionSet{}, fmt.Errorf("starter: constructor for %s not found", module)
		}
		for _, other := range modules {
			if other != module && regexp.MustCompile(`\b`+regexp.QuoteMeta(other)+`M\b`).MatchString(line[1]) {
				deps[module] = append(deps[module], other)
			}
		}
	}
	manifests := map[string]moduleManifest{}
	webOwners := map[string]string{}
	for _, module := range modules {
		manifestData, err := repo.read("internal/modules/" + module + "/module.yaml")
		if err != nil {
			return DeletionSet{}, err
		}
		var manifest moduleManifest
		if err := yaml.Unmarshal([]byte(manifestData), &manifest); err != nil {
			return DeletionSet{}, fmt.Errorf("starter: parse %s module.yaml: %w", module, err)
		}
		for _, webPath := range manifest.WebPaths {
			if pathpkg.Clean(webPath) != webPath || !strings.HasPrefix(webPath, "web/admin/") {
				return DeletionSet{}, fmt.Errorf("starter: %s has invalid web path %q", module, webPath)
			}
			if owner, exists := webOwners[webPath]; exists {
				return DeletionSet{}, fmt.Errorf("starter: web path %s claimed by both %s and %s", webPath, owner, module)
			}
			webOwners[webPath] = module
		}
		manifests[module] = manifest
	}
	drop, err := closure(modules, deps, removed)
	if err != nil {
		return DeletionSet{}, err
	}
	removedSet := map[string]bool{}
	for _, module := range drop {
		removedSet[module] = true
	}
	pathSet := map[string]bool{}
	var httpTags, adminTags []string
	adminSurface := false
	for _, module := range drop {
		pathSet["internal/modules/"+module+"/"] = true
		manifest := manifests[module]
		for _, path := range manifest.WebPaths {
			pathSet[path] = true
		}
		for _, item := range []struct {
			path string
			tags *[]string
		}{
			{"internal/modules/" + module + "/httpapi/oapi/config.yaml", &httpTags},
			{"internal/modules/" + module + "/adminapi/oapi/config.yaml", &adminTags},
		} {
			content, err := repo.read(item.path)
			if err == nil {
				if tag := includeTag(content); tag != "" {
					*item.tags = append(*item.tags, tag)
				}
			}
		}
		authStart := strings.Index(modulesGo, "func (m *Modules) adminAuth()")
		authEnd := -1
		if authStart >= 0 {
			authEnd = strings.Index(modulesGo[authStart:], "\n}")
		}
		if authStart >= 0 && authEnd >= 0 && strings.Contains(modulesGo[authStart:authStart+authEnd], "m."+strings.ToUpper(module[:1])+module[1:]+".") {
			adminSurface = true
		}
	}
	for _, name := range files {
		if !strings.HasPrefix(name, "internal/app/") {
			continue
		}
		base := filepath.Base(name)
		for _, module := range drop {
			matched := regexp.MustCompile(`^[a-z]+(_[a-z]+)*_` + regexp.QuoteMeta(module) + `(_[a-z_]+)?\.go$`).MatchString(base)
			if matched {
				pathSet[name] = true
			}
			if strings.HasSuffix(name, "_test.go") {
				content, err := repo.read(name)
				if err == nil && strings.Contains(content, "/internal/modules/"+module) {
					pathSet[name] = true
				}
			}
		}
	}
	var paths []string
	for path := range pathSet {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	slices.Sort(httpTags)
	slices.Sort(adminTags)
	return DeletionSet{Modules: drop, Paths: paths, HTTPTags: unique(httpTags), AdminTags: unique(adminTags), AdminSurface: adminSurface}, nil
}

func unique(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}
