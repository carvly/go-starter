//go:build unix

package logger

import (
	"fmt"
	"io"
	"os"
	"syscall"
)

// toJournal 判断 w 是不是 systemd 接到 journald 的那条流。systemd 把它的"设备号:inode"写在 JOURNAL_STREAM 里；
// 只看变量是否存在不够：子进程会继承它，而 stdout 可能已经被重定向到别处。
func toJournal(w io.Writer) bool {
	stream := os.Getenv("JOURNAL_STREAM")
	f, ok := w.(*os.File)
	if stream == "" || !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && stream == fmt.Sprintf("%d:%d", st.Dev, st.Ino)
}
