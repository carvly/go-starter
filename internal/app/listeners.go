package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"app.local/internal/config"
	"app.local/internal/platform/httpserver"
	"app.local/internal/platform/systemd"
)

// apiListeners 是 api 进程的监听 socket。socket activation 时来自 systemd（.socket 单元的 ListenStream=），由轮流上线的
// 实例共享；否则由 bindListeners 按配置绑定。
type apiListeners struct {
	front, admin, internal net.Listener
}

func (l *apiListeners) close() {
	for _, ln := range []net.Listener{l.front, l.admin, l.internal} {
		if ln != nil {
			_ = ln.Close()
		}
	}
}

// inheritListeners 取得 systemd 传来的监听 socket，按地址认领用途。后台、内部端口的 socket 的有无与它们是否启动的
// 比对要等模块构造之后（checkOptionalSockets）：是否启动取决于有没有对应的路由。
func inheritListeners(cfg config.HTTPConfig) (*apiListeners, error) {
	lns, err := systemd.Listeners()
	if err != nil {
		return nil, fmt.Errorf("app: HTTP_SOCKET_ACTIVATION=true: %w", err)
	}
	out, err := matchInherited(lns, cfg)
	if err != nil {
		for _, ln := range lns {
			_ = ln.Close()
		}
		return nil, err
	}
	return out, nil
}

// matchInherited 按地址认领继承的 socket：HTTP_ADDR 是前台、HTTP_ADMIN_ADDR 是后台、HTTP_API_INTERNAL_ADDR 是内部端口，
// 与 ListenStream= 的书写顺序无关
// （systemd 219 没有 FileDescriptorName=，只能靠地址区分）。前台必须有；认领不了的 socket、重复的地址都报错：
// 没人 accept 的 socket 上的连接不会被拒绝，而是挂到客户端超时。
func matchInherited(lns []net.Listener, cfg config.HTTPConfig) (*apiListeners, error) {
	out := &apiListeners{}
	roles := []struct {
		env, addr string
		slot      *net.Listener
	}{
		{"HTTP_ADDR", cfg.Addr, &out.front},
		{"HTTP_ADMIN_ADDR", cfg.AdminAddr, &out.admin},
		{"HTTP_API_INTERNAL_ADDR", cfg.APIInternalAddr, &out.internal},
	}
	for _, ln := range lns {
		claimed := false
		for _, r := range roles {
			if !sameAddr(ln.Addr(), r.addr) {
				continue
			}
			if *r.slot != nil {
				return nil, fmt.Errorf("app: systemd passed %s twice (%s): remove the duplicate ListenStream= from the .socket unit", ln.Addr(), r.env)
			}
			*r.slot, claimed = ln, true
			break
		}
		if !claimed {
			return nil, fmt.Errorf("app: systemd passed a socket on %s that matches none of HTTP_ADDR=%q, HTTP_ADMIN_ADDR=%q, "+
				"HTTP_API_INTERNAL_ADDR=%q: ListenStream= in the .socket unit and the addresses in the environment must agree",
				ln.Addr(), cfg.Addr, cfg.AdminAddr, cfg.APIInternalAddr)
		}
	}
	if out.front == nil {
		return nil, fmt.Errorf("app: systemd passed no socket for HTTP_ADDR=%q: add ListenStream=%s to the .socket unit", cfg.Addr, cfg.Addr)
	}
	return out, nil
}

func sameAddr(a net.Addr, want string) bool {
	ta, ok := a.(*net.TCPAddr)
	if !ok || want == "" {
		return false
	}
	tw, err := net.ResolveTCPAddr("tcp", want)
	return err == nil && ta.IP.Equal(tw.IP) && ta.Port == tw.Port
}

// optionalSocket 是按需启动的监听端口（后台、内部端口）：配置了地址并且有路由时才启动。
type optionalSocket struct {
	name, env string
	enabled   bool
	ln        net.Listener
}

// checkOptionalSockets 比对"是否启动"与"有没有对应的 socket"。只在 socket activation 时可能不一致（自己绑定时按是否
// 启用决定绑不绑）。不一致时：没人 accept 的 socket 上的连接不会被拒绝，而是挂到网关超时；缺了 socket 则根本没有入口。
func checkOptionalSockets(sockets ...optionalSocket) error {
	for _, s := range sockets {
		switch {
		case s.enabled && s.ln == nil:
			return fmt.Errorf("app: the %s server is enabled (%s is set and %s routes are registered) but systemd "+
				"passed no socket for it: add ListenStream=<%s> to the .socket unit", s.name, s.env, s.name, s.env)
		case !s.enabled && s.ln != nil:
			return fmt.Errorf("app: systemd passed the %s socket (%s) but the %s server is not enabled (no %s routes): "+
				"remove that ListenStream= from the .socket unit", s.name, s.ln.Addr(), s.name, s.name)
		}
	}
	return nil
}

// waitDependencies 在发 READY 之前确认依赖可用：用的是 /readyz 的同一组 Pinger。连接池是惰性建连的，不检查的话
// 库连不上的新实例也会发出 READY，发布脚本随即停掉旧实例。失败时每秒重试，直到成功或 ctx 取消；一直不通过时由
// systemd 的 TimeoutStartSec 终止进程。
func waitDependencies(ctx context.Context, pingers []httpserver.Pinger, log *slog.Logger) error {
	const (
		attemptTimeout = 3 * time.Second
		retryInterval  = time.Second
	)
	for {
		err := pingAll(ctx, pingers, attemptTimeout)
		if err == nil {
			return nil
		}
		log.WarnContext(ctx, "dependencies not ready, retrying before READY", slog.Any("error", err))
		select {
		case <-ctx.Done():
			return fmt.Errorf("app: dependencies not ready: %w", context.Cause(ctx))
		case <-time.After(retryInterval):
		}
	}
}

func pingAll(ctx context.Context, pingers []httpserver.Pinger, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for _, p := range pingers {
		if err := p.Ping(ctx); err != nil {
			return err
		}
	}
	return nil
}
