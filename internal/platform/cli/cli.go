// Package cli 是 api 二进制子命令的查找和执行。命令由模块提供、组合根汇总，
// 本包不认识任何模块。
package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// Command 是一个子命令。Name 固定两个词："<模块> <动作>"，如 "staff create"。
type Command struct {
	Name    string
	Summary string
	// Run 收到的 args 不含命令名。结果写 out；日志走 logger，不写 out。
	Run func(ctx context.Context, args []string, out io.Writer) error
}

// 退出码：0 成功，1 命令执行出错，2 用法错误（与 flag 包一致）。
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

// Run 按 args 的前两个词查找命令并执行，返回进程退出码。
func Run(ctx context.Context, prog string, cmds []Command, args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		usage(stdout, prog, cmds)
		return ExitOK
	}
	if len(args) >= 2 {
		name := args[0] + " " + args[1]
		for _, c := range cmds {
			if c.Name == name {
				if err := c.Run(ctx, args[2:], stdout); err != nil {
					printf(stderr, "%s %s: %v\n", prog, name, err)
					return ExitError
				}
				return ExitOK
			}
		}
	}

	var sameModule []Command
	if len(args) > 0 {
		for _, c := range cmds {
			if strings.HasPrefix(c.Name, args[0]+" ") {
				sameModule = append(sameModule, c)
			}
		}
	}
	printf(stderr, "%s: 未知命令 %q\n\n", prog, strings.Join(args, " "))
	if len(sameModule) > 0 {
		usage(stderr, prog, sameModule)
	} else {
		usage(stderr, prog, cmds)
	}
	return ExitUsage
}

func usage(w io.Writer, prog string, cmds []Command) {
	printf(w, "用法：%s                 启动 HTTP 服务\n", prog)
	printf(w, "      %s healthcheck     探活\n", prog)
	printf(w, "      %s <模块> <动作> [参数]\n\n", prog)
	if len(cmds) == 0 {
		printf(w, "目前没有模块提供命令。\n")
		return
	}
	printf(w, "命令：\n")
	width := 0
	for _, c := range cmds {
		width = max(width, len(c.Name))
	}
	for _, c := range cmds {
		printf(w, "  %-*s  %s\n", width, c.Name, c.Summary)
	}
}

// printf 写终端。写 stdout/stderr 失败时没有更好的去处可以报告，忽略错误。
func printf(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}
