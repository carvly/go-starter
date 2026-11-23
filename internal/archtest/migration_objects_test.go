package archtest

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	createIndexRe             = regexp.MustCompile(`(?i)^CREATE\s+(?:UNIQUE\s+)?INDEX\s+(?:CONCURRENTLY\s+)?(?:IF\s+NOT\s+EXISTS\s+)?([A-Za-z_][A-Za-z0-9_]*)\s+ON\s+(?:ONLY\s+)?` + identPattern)
	constraintRe              = regexp.MustCompile(`(?i)\bCONSTRAINT\s+([A-Za-z_][A-Za-z0-9_]*)`)
	commentTableRe            = regexp.MustCompile(`(?i)^COMMENT\s+ON\s+(?:TABLE|COLUMN)\s+` + identPattern)
	dropIndexRe               = regexp.MustCompile(`(?i)^DROP\s+INDEX\s+(?:CONCURRENTLY\s+)?(?:IF\s+EXISTS\s+)?([A-Za-z_][A-Za-z0-9_]*)`)
	allowedMigrationStatement = regexp.MustCompile(`(?i)^(?:CREATE\s+(?:(?:GLOBAL|LOCAL)\s+)?(?:(?:UNLOGGED|TEMP|TEMPORARY)\s+)?TABLE\b|ALTER\s+TABLE\b|CREATE\s+(?:UNIQUE\s+)?INDEX\b|DROP\s+(?:TABLE|INDEX)\b|COMMENT\s+ON\b|INSERT\b|UPDATE\b|DELETE\b|SET\s+lock_timeout\s*=|RESET\s+lock_timeout\b)`)
)

func migrationModule(name string) string {
	parts := strings.Split(filepath.ToSlash(name), "/")
	for i, part := range parts {
		if part == "modules" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func longestNamedTable(name string, owner map[string]string) string {
	var match string
	for table := range owner {
		for _, prefix := range []string{"pk_", "uk_", "ck_", "fk_", "idx_"} {
			if name == prefix+table || strings.HasPrefix(name, prefix+table+"_") {
				if len(table) > len(match) {
					match = table
				}
			}
		}
	}
	return match
}

func migrationObjectViolations(files []migrationFile, owner map[string]string) []string {
	var out []string
	for _, file := range files {
		module := migrationModule(file.name)
		if module == "" {
			continue
		}
		for _, raw := range strings.Split(stripSQLNoise(file.content), ";") {
			statement := strings.TrimSpace(raw)
			if statement == "" {
				continue
			}
			if !allowedMigrationStatement.MatchString(statement) {
				out = append(out, fmt.Sprintf("%s：模块迁移只允许表、索引、注释、数据修改和 lock_timeout；类型、函数、扩展等共享对象不属于任何模块，不能放进模块迁移：%s", file.name, firstSQLWords(statement)))
				continue
			}

			var tables []string
			var objectNames []string
			actualTable := ""
			if match := createTableRe.FindStringSubmatch(statement); match != nil {
				actualTable = normalizeIdent(match[1])
				tables = append(tables, actualTable)
			}
			if match := alterTableRe.FindStringSubmatch(statement); match != nil {
				actualTable = normalizeIdent(match[1])
				tables = append(tables, actualTable)
			}
			if match := dropTableRe.FindStringSubmatch(statement); match != nil {
				for table := range strings.SplitSeq(match[1], ",") {
					tables = append(tables, normalizeIdent(table))
				}
			}
			if match := commentTableRe.FindStringSubmatch(statement); match != nil {
				table := match[1]
				if strings.HasPrefix(strings.ToUpper(statement), "COMMENT ON COLUMN") {
					table = strings.Split(table, ".")[0]
				}
				tables = append(tables, normalizeIdent(table))
			}
			if match := createIndexRe.FindStringSubmatch(statement); match != nil {
				objectNames = append(objectNames, match[1])
				actualTable = normalizeIdent(match[2])
				tables = append(tables, actualTable)
			}
			if match := dropIndexRe.FindStringSubmatch(statement); match != nil {
				objectNames = append(objectNames, match[1])
			}
			for _, match := range constraintRe.FindAllStringSubmatch(statement, -1) {
				objectNames = append(objectNames, match[1])
			}
			tables = append(tables, referencedTables(statement, ownerKeys(owner))...)

			for _, table := range tables {
				tableOwner, ok := owner[table]
				if !ok || tableOwner != module {
					out = append(out, fmt.Sprintf("%s：模块 %s 的迁移操作了表 %s（归属 %s）；只能操作本模块在 postgres/tables.go 登记的表", file.name, module, table, tableOwner))
				}
			}
			for _, name := range objectNames {
				table := longestNamedTable(name, owner)
				if table == "" {
					out = append(out, fmt.Sprintf("%s：对象名 %s 必须以 pk_/uk_/ck_/fk_/idx_ 加已登记表名开头", file.name, name))
					continue
				}
				if owner[table] != module || (actualTable != "" && table != actualTable) {
					out = append(out, fmt.Sprintf("%s：对象名 %s 按最长表名匹配到 %s，与实际表 %s 或模块 %s 不符", file.name, name, table, actualTable, module))
				}
			}
		}
	}
	return out
}

func ownerKeys(owner map[string]string) map[string]bool {
	out := make(map[string]bool, len(owner))
	for table := range owner {
		out[table] = true
	}
	return out
}

func firstSQLWords(statement string) string {
	if len(statement) > 100 {
		return statement[:100] + "..."
	}
	return statement
}

func TestTables_MigrationObjectsStayInModule(t *testing.T) {
	files, err := loadModuleMigrations("*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range migrationObjectViolations(files, tableFactsFor(t).owner) {
		t.Error(problem)
	}
}

func TestMigrationObjectViolations(t *testing.T) {
	owner := map[string]string{"betas": "beta", "beta_items": "beta", "alphas": "alpha"}
	tests := []struct {
		name    string
		module  string
		sql     string
		wantBad bool
	}{
		{name: "最长表名匹配", module: "beta", sql: `CREATE INDEX idx_beta_items_created_at ON beta_items (created_at);`},
		{name: "索引建到短表名", module: "beta", sql: `CREATE INDEX idx_beta_items_created_at ON betas (created_at);`, wantBad: true},
		{name: "跨模块数据修改", module: "beta", sql: `DELETE FROM alphas;`, wantBad: true},
		{name: "共享类型", module: "beta", sql: `CREATE TYPE beta_status AS ENUM ('pending');`, wantBad: true},
		{name: "锁超时", module: "beta", sql: `SET lock_timeout = '5s'; RESET lock_timeout;`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := migrationFile{name: "internal/modules/" + tt.module + "/migrations/0001_test.up.sql", content: tt.sql}
			got := migrationObjectViolations([]migrationFile{file}, owner)
			if (len(got) > 0) != tt.wantBad {
				t.Fatalf("violations = %v, wantBad = %v", got, tt.wantBad)
			}
		})
	}
}
