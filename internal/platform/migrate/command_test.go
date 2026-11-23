package migrate_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"testing/fstest"

	"app.local/internal/platform/migrate"
)

func sources() []migrate.Source {
	return []migrate.Source{{Name: "river", Migrator: &fakeMigrator{}}, {Name: "user", FS: fstest.MapFS{}}}
}

func TestRunRejectsGlobalDown(t *testing.T) {
	err := migrate.Run("postgres://unused", sources(), []string{"down"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "-module") {
		t.Fatalf("down error = %v, want -module requirement", err)
	}
}

func TestNewRunnerRejectsDuplicateSources(t *testing.T) {
	_, err := migrate.NewRunner("postgres://unused", []migrate.Source{{Name: "user", FS: fstest.MapFS{}}, {Name: "user", FS: fstest.MapFS{}}})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("error = %v, want duplicate source", err)
	}
}

type fakeMigrator struct {
	calls   []string
	version uint
}

func (f *fakeMigrator) Up(context.Context, string) error { f.calls = append(f.calls, "up"); return nil }
func (f *fakeMigrator) Down(context.Context, string) error {
	f.calls = append(f.calls, "down")
	return nil
}
func (f *fakeMigrator) Version(context.Context, string) (uint, error) { return f.version, nil }

// 自带迁移器的来源（river）由 Runner 调用它自己的迁移器：up、down 转给它，version 读它的版本；它不会留下 dirty 版本，
// force 不适用、直接报错。只有它一个来源时，不需要连 golang-migrate 的数据库。
func TestRun_MigratorSource(t *testing.T) {
	m := &fakeMigrator{version: 8}
	srcs := []migrate.Source{{Name: "river", Migrator: m}}
	for _, args := range [][]string{{"up"}, {"down", "-module", "river"}} {
		if err := migrate.Run("postgres://unused", srcs, args, io.Discard); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if got := strings.Join(m.calls, ","); got != "up,down" {
		t.Errorf("calls = %q, want up,down", got)
	}
	var out bytes.Buffer
	if err := migrate.Run("postgres://unused", srcs, []string{"version"}, &out); err != nil || out.String() != "river\t8\tfalse\n" {
		t.Errorf("version = %q, %v", out.String(), err)
	}
	err := migrate.Run("postgres://unused", srcs, []string{"force", "-module", "river", "-version", "3"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "force does not apply") {
		t.Errorf("force error = %v", err)
	}
}

func TestNewRunnerRequiresExactlyOneOfFSAndMigrator(t *testing.T) {
	for name, src := range map[string]migrate.Source{
		"都没有":  {Name: "river"},
		"两个都有": {Name: "river", FS: fstest.MapFS{}, Migrator: &fakeMigrator{}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := migrate.NewRunner("postgres://unused", []migrate.Source{src})
			if err == nil || !strings.Contains(err.Error(), "exactly one of FS and Migrator") {
				t.Errorf("error = %v", err)
			}
		})
	}
}
