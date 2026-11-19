package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func read(root, version, name string) (string, error) {
	if version == "" {
		data, err := os.ReadFile(filepath.Join(root, name))
		return string(data), err
	}
	cmd := exec.Command("git", "-C", root, "show", version+":"+name)
	out, err := cmd.Output()
	return string(out), err
}

var operationMethodRe = regexp.MustCompile(`^    [a-z]+:\s*$`)

func operations(spec string) map[string]string {
	lines := strings.Split(spec, "\n")
	out := map[string]string{}
	var path, method string
	var block []string
	flush := func() {
		if method != "" && path != "" {
			out[method+" "+path] = strings.Join(block, "\n")
		}
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "  /") {
			path = strings.TrimSpace(strings.SplitN(line, ":", 2)[0])
			method = ""
			continue
		}
		if operationMethodRe.MatchString(line) {
			flush()
			method = strings.TrimSpace(strings.TrimSuffix(line, ":"))
			block = []string{line}
			continue
		}
		if method != "" {
			block = append(block, line)
		}
	}
	flush()
	return out
}

func starterOperations(spec string, tags map[string]bool) map[string]string {
	all := operations(spec)
	out := map[string]string{}
	for key, block := range all {
		for tag := range tags {
			if strings.Contains(block, "tags: ["+tag+"]") {
				out[key] = block
			}
		}
	}
	return out
}

func schemas(spec string) map[string]string {
	lines := strings.Split(spec, "\n")
	start := -1
	for i, line := range lines {
		if line == "  schemas:" {
			start = i + 1
			break
		}
	}
	out := map[string]string{}
	if start < 0 {
		return out
	}
	var name string
	var block []string
	flush := func() {
		if name != "" {
			out[name] = strings.Join(block, "\n")
		}
	}
	for i := start; i < len(lines); i++ {
		line := lines[i]
		if line != "" && !strings.HasPrefix(line, "    ") {
			flush()
			break
		}
		if match := regexp.MustCompile(`^    ([A-Za-z0-9_]+):\s*$`).FindStringSubmatch(line); match != nil {
			flush()
			name = match[1]
			block = []string{line}
			continue
		}
		if name != "" {
			block = append(block, line)
		}
	}
	flush()
	return out
}

func referenced(text string) [][]string {
	return regexp.MustCompile(`\$ref:\s*["']?#/components/schemas/([A-Za-z0-9_]+)`).FindAllStringSubmatch(text, -1)
}

func schemaClosure(initial []string, definitions map[string]string) map[string]bool {
	seen := map[string]bool{}
	queue := append([]string(nil), initial...)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		for _, match := range referenced(definitions[name]) {
			queue = append(queue, match[1])
		}
	}
	return seen
}

func starterTags(root string) map[string]bool {
	tags := map[string]bool{}
	for _, module := range []string{"auth", "staff"} {
		for _, api := range []string{"httpapi", "adminapi"} {
			content, err := read(root, "", "internal/modules/"+module+"/"+api+"/oapi/config.yaml")
			if err == nil {
				match := regexp.MustCompile(`(?m)^\s*include-tags:\s*\[([^]]+)\]`).FindStringSubmatch(content)
				if match != nil {
					tags[strings.ReplaceAll(match[1], " ", "")] = true
				}
			}
		}
	}
	return tags
}

func mapsKeys(in map[string]bool) []string {
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	return out
}
