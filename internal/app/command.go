package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"app.local/internal/config"
	"app.local/internal/platform/cli"
)

// RunCommand 执行模块提供的子命令（api <模块> <动作> ...），返回进程退出码。
// 它构建和 api 进程相同的 Infra 与模块，但不启动 HTTP。日志写 stderr，
// stdout 只留给命令的结果，脚本可以直接解析。
//
// NewInfra 不连库（连接池按需建连），所以没有数据库时也能列出命令。
func RunCommand(ctx context.Context, cfg *config.Config, args []string, stdout, stderr io.Writer) int {
	configs, err := loadModuleConfigs(cfg.Source)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "api: load module config:", err)
		return cli.ExitError
	}
	id, log := newProcess(cfg, "api", stderr)

	in, err := NewInfra(ctx, cfg, id, log)
	if err != nil {
		log.ErrorContext(ctx, "build infra", slog.Any("error", err))
		return cli.ExitError
	}
	defer in.Close(ctx)

	return cli.Run(ctx, "api", commands(buildModules(in, configs)), args, stdout, stderr)
}

func commands(mods *Modules) []cli.Command {
	var all []cli.Command
	for _, cmds := range mods.commandRegistrars() {
		all = append(all, cmds()...)
	}
	return all
}
