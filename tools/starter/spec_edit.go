package main

import (
	"regexp"
	"strings"
)

func removeFromSpec(spec, tag string) string {
	lines := strings.Split(spec, "\n")
	out, i := make([]string, 0, len(lines)), 0
	inPaths := false
	for i < len(lines) {
		line := lines[i]
		if len(line) > 0 && !strings.HasPrefix(line, " ") {
			inPaths = strings.HasPrefix(line, "paths:")
		}
		if inPaths && strings.HasPrefix(line, "  /") {
			block := []string{line}
			i++
			for i < len(lines) && (strings.HasPrefix(lines[i], "   ") || lines[i] == "") {
				block = append(block, lines[i])
				i++
			}
			var ops [][]string
			var current []string
			for _, item := range block[1:] {
				if operationMethodRe.MatchString(item) {
					current = []string{item}
					ops = append(ops, current)
				} else if current != nil {
					current = append(current, item)
					ops[len(ops)-1] = current
				}
			}
			var kept [][]string
			for _, op := range ops {
				matched := false
				for _, item := range op {
					if strings.Contains(item, "tags: ["+tag+"]") {
						matched = true
					}
				}
				if !matched {
					kept = append(kept, op)
				}
			}
			if len(kept) > 0 {
				out = append(out, block[0])
				for _, op := range kept {
					out = append(out, op...)
				}
			}
			continue
		}
		if matched, _ := regexp.MatchString(`^  - name:\s*`+regexp.QuoteMeta(tag)+`\s*$`, line); matched {
			i++
			for i < len(lines) && strings.HasPrefix(lines[i], "    ") {
				i++
			}
			continue
		}
		out = append(out, line)
		i++
	}
	return pruneUnreferencedSchemas(strings.Join(out, "\n"))
}

func pruneUnreferencedSchemas(spec string) string {
	lines := strings.Split(spec, "\n")
	start := -1
	for i, line := range lines {
		if line == "  schemas:" {
			start = i
			break
		}
	}
	if start < 0 {
		return spec
	}
	type block struct {
		name  string
		lines []string
	}
	var blocks []block
	i := start + 1
	var current *block
	for i < len(lines) && (strings.HasPrefix(lines[i], "    ") || strings.TrimSpace(lines[i]) == "") {
		if match := regexp.MustCompile(`^    ([A-Za-z0-9_]+):\s*$`).FindStringSubmatch(lines[i]); match != nil {
			current = &block{name: match[1], lines: []string{lines[i]}}
			blocks = append(blocks, *current)
			current = &blocks[len(blocks)-1]
		} else if current != nil {
			current.lines = append(current.lines, lines[i])
		}
		i++
	}
	var outside []string
	for idx, line := range lines {
		if idx <= start || idx >= i {
			outside = append(outside, line)
		}
	}
	keep := map[string]bool{}
	for _, line := range outside {
		for _, match := range referenced(line) {
			keep[match[1]] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, block := range blocks {
			if keep[block.name] {
				for _, match := range referenced(strings.Join(block.lines, "\n")) {
					if !keep[match[1]] {
						keep[match[1]] = true
						changed = true
					}
				}
			}
		}
	}
	out := append([]string{}, lines[:start+1]...)
	for _, block := range blocks {
		if keep[block.name] {
			out = append(out, block.lines...)
		}
	}
	out = append(out, lines[i:]...)
	return strings.Join(out, "\n")
}
