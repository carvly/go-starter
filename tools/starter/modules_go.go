package main

import (
	"fmt"
	"go/format"
	"os"
	"regexp"
	"strings"
)

func removeModuleLines(root, module string) error {
	for _, name := range []string{"internal/app/modules.go", "internal/app/module_configs.go", "internal/app/migrate.go"} {
		path := root + "/" + name
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var out []string
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "/internal/modules/"+module) ||
				regexp.MustCompile(`\b`+regexp.QuoteMeta(module)+`M\b`).MatchString(line) ||
				regexp.MustCompile(`\b`+regexp.QuoteMeta(module)+`\.`).MatchString(line) ||
				strings.Contains(line, "*"+module+".Module") ||
				regexp.MustCompile(`\bm\.`+strings.ToUpper(module[:1])+module[1:]+`\.`).MatchString(line) ||
				regexp.MustCompile(`\b(?:configs|c)\.`+strings.ToUpper(module[:1])+module[1:]+`\b`).MatchString(line) {
				continue
			}
			out = append(out, line)
		}
		formatted, err := format.Source([]byte(strings.Join(out, "\n")))
		if err != nil {
			return fmt.Errorf("format %s after removing %s: %w", name, module, err)
		}
		if err := os.WriteFile(path, formatted, 0o644); err != nil {
			return err
		}
	}
	return nil
}
