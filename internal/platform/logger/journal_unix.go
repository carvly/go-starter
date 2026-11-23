//go:build unix

package logger

import (
	"fmt"
	"io"
	"os"
	"syscall"
)

// journalStdoutSocket 是 journald 接收服务 stdout 的 socket（systemd 把服务的 stdout 连到它）。测试可替换。
var journalStdoutSocket = "/run/systemd/journal/stdout"

// toJournal 判断 w 是不是 systemd 接到 journald 的那条流。systemd 231 起把它的"设备号:inode"写在 JOURNAL_STREAM 里；
// 只看变量是否存在不够：子进程会继承它，而 stdout 可能已经被重定向到别处。更早的 systemd（CentOS 7 的 219）不设置
// JOURNAL_STREAM，这时看 w 是否是连到 journald stdout socket 的 unix socket。
func toJournal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	if stream := os.Getenv("JOURNAL_STREAM"); stream != "" {
		fi, err := f.Stat()
		if err != nil {
			return false
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		return ok && stream == fmt.Sprintf("%d:%d", st.Dev, st.Ino)
	}
	sc, err := f.SyscallConn()
	if err != nil {
		return false
	}
	var peer syscall.Sockaddr
	var perr error
	if err := sc.Control(func(fd uintptr) { peer, perr = syscall.Getpeername(int(fd)) }); err != nil || perr != nil {
		return false
	}
	ua, ok := peer.(*syscall.SockaddrUnix)
	return ok && ua.Name == journalStdoutSocket
}
