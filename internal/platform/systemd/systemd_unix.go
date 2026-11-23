//go:build unix

package systemd

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"syscall"
)

// listenFDsStart 是 systemd 传给进程的第一个 fd（SD_LISTEN_FDS_START）。
const listenFDsStart = 3

// Listeners 返回 systemd 按 socket activation 协议传给本进程的监听 socket，顺序与 .socket 单元里
// ListenStream= 的书写顺序相同。LISTEN_PID 必须等于本进程，否则这些变量是从父进程继承来的，fd 不属于本进程。
// 无论成败都清除 LISTEN_PID、LISTEN_FDS、LISTEN_FDNAMES，并给 fd 设置 close-on-exec，不泄漏给子进程。
func Listeners() ([]net.Listener, error) {
	pidEnv, fdsEnv := os.Getenv("LISTEN_PID"), os.Getenv("LISTEN_FDS")
	for _, k := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
		_ = os.Unsetenv(k)
	}
	if pidEnv == "" {
		return nil, errors.New("systemd: LISTEN_PID is not set: the process was not started by systemd socket activation " +
			"(the service unit needs Sockets= pointing at a .socket unit)")
	}
	if pid, err := strconv.Atoi(pidEnv); err != nil || pid != os.Getpid() {
		return nil, fmt.Errorf("systemd: LISTEN_PID=%q is not this process (%d): the sockets were passed to another process", pidEnv, os.Getpid())
	}
	n, err := strconv.Atoi(fdsEnv)
	if err != nil || n < 1 {
		return nil, fmt.Errorf("systemd: LISTEN_FDS=%q must be a positive number", fdsEnv)
	}
	lns := make([]net.Listener, 0, n)
	for fd := listenFDsStart; fd < listenFDsStart+n; fd++ {
		syscall.CloseOnExec(fd)
		f := os.NewFile(uintptr(fd), "systemd-listen-fd-"+strconv.Itoa(fd))
		ln, err := net.FileListener(f) // dup 一份交给 netpoller，原 fd 随即关闭
		_ = f.Close()
		if err != nil {
			for _, l := range lns {
				_ = l.Close()
			}
			return nil, fmt.Errorf("systemd: fd %d is not a listening socket: %w", fd, err)
		}
		lns = append(lns, ln)
	}
	return lns, nil
}

// Notify 向 systemd 发送状态（如 Ready）。NOTIFY_SOCKET 未设置（不在 systemd 下，或单元不是 Type=notify）时
// 什么都不做，返回 nil。
func Notify(state string) error {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" {
		return nil
	}
	if addr[0] == '@' { // 抽象命名空间
		addr = "\x00" + addr[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: addr, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("systemd: notify %q: %w", state, err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte(state)); err != nil {
		return fmt.Errorf("systemd: notify %q: %w", state, err)
	}
	return nil
}
