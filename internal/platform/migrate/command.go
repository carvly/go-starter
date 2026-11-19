package migrate

import (
	"flag"
	"fmt"
	"io"
)

// Run 执行 migrate 子命令，stdout 只写命令结果。
func Run(dsn string, sources []Source, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("migrate: expected up, down, version or force")
	}
	runner, err := NewRunner(dsn, sources)
	if err != nil {
		return err
	}
	switch args[0] {
	case "up":
		if len(args) != 1 {
			return fmt.Errorf("migrate up takes no arguments")
		}
		return runner.Up()
	case "down":
		flags := flag.NewFlagSet("migrate down", flag.ContinueOnError)
		flags.SetOutput(stdout)
		module := flags.String("module", "", "migration source")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *module == "" || flags.NArg() != 0 {
			return fmt.Errorf("usage: api migrate down -module <m>")
		}
		return runner.Down(*module)
	case "force":
		flags := flag.NewFlagSet("migrate force", flag.ContinueOnError)
		flags.SetOutput(stdout)
		module := flags.String("module", "", "migration source")
		version := flags.Int("version", -1, "migration version")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *module == "" || *version < 0 || flags.NArg() != 0 {
			return fmt.Errorf("usage: api migrate force -module <m> -version N")
		}
		return runner.Force(*module, *version)
	case "version":
		if len(args) != 1 {
			return fmt.Errorf("migrate version takes no arguments")
		}
		versions, err := runner.Versions()
		if err != nil {
			return err
		}
		for _, v := range versions {
			_, _ = fmt.Fprintf(stdout, "%s\t%d\t%v\n", v.Source, v.Version, v.Dirty)
		}
		return nil
	default:
		return fmt.Errorf("migrate: unknown action %q", args[0])
	}
}
