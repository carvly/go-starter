//go:build !unix

package systemd

import (
	"errors"
	"net"
	"os"
)

// Listeners 在非 unix 平台上不可用：socket activation 是 systemd 的协议。
func Listeners() ([]net.Listener, error) {
	return nil, errors.New("systemd: socket activation is only supported on unix")
}

// Notify 在非 unix 平台上只在设置了 NOTIFY_SOCKET 时报错。
func Notify(string) error {
	if os.Getenv("NOTIFY_SOCKET") == "" {
		return nil
	}
	return errors.New("systemd: notify is only supported on unix")
}
