package staff

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"app.local/internal/modules/staff/domain"
	"app.local/internal/platform/cli"
)

// commands 不经过 HTTP，也就不经过后台的操作日志中间件，自己记日志，操作人为空（CLI）。
func commands(svc *domain.Service) []cli.Command {
	return []cli.Command{
		{
			Name:    "staff create",
			Summary: "创建超级管理员，输出临时密码（首个管理员用它创建）",
			Run: func(ctx context.Context, args []string, out io.Writer) error {
				fs := flag.NewFlagSet("staff create", flag.ContinueOnError)
				fs.SetOutput(out)
				username := fs.String("username", "", "登录名（必填）")
				display := fs.String("display-name", "", "显示名，默认同登录名")
				if err := fs.Parse(args); err != nil {
					return err
				}
				if *username == "" {
					return errors.New("-username is required")
				}
				a, temp, err := svc.CreateAccount(ctx, domain.CreateAccountInput{Username: *username, DisplayName: *display, SuperAdmin: true})
				if err != nil {
					return err
				}
				if err := svc.RecordAudit(ctx, &domain.AuditEntry{Action: "cli:staff create", Target: a.ID, Result: "ok"}); err != nil {
					return fmt.Errorf("account created but audit failed: %w", err)
				}
				_, err = fmt.Fprintf(out, "已创建超级管理员 %s（%s）\n临时密码（%s 前有效，首次登录后必须修改）：%s\n",
					a.Username, a.ID, a.TempPasswordExpiresAt.Local().Format(time.DateTime), temp)
				return err
			},
		},
		{
			Name:    "staff reset-password",
			Summary: "重设员工密码并启用账号，输出临时密码（所有超级管理员都进不来时的恢复手段）",
			Run: func(ctx context.Context, args []string, out io.Writer) error {
				fs := flag.NewFlagSet("staff reset-password", flag.ContinueOnError)
				fs.SetOutput(out)
				username := fs.String("username", "", "登录名（必填）")
				if err := fs.Parse(args); err != nil {
					return err
				}
				if *username == "" {
					return errors.New("-username is required")
				}
				a, err := svc.GetAccountByUsername(ctx, *username)
				if err != nil {
					return err
				}
				temp, expires, err := svc.RecoverAccount(ctx, a.ID)
				if err != nil {
					return err
				}
				if err := svc.RecordAudit(ctx, &domain.AuditEntry{Action: "cli:staff reset-password", Target: a.ID, Result: "ok"}); err != nil {
					return fmt.Errorf("password reset but audit failed: %w", err)
				}
				_, err = fmt.Fprintf(out, "已重设 %s 的密码并启用账号，全部会话已吊销\n临时密码（%s 前有效，首次登录后必须修改）：%s\n",
					a.Username, expires.Local().Format(time.DateTime), temp)
				return err
			},
		},
	}
}
