// Package systemd 实现 systemd 的两个进程协议，只用标准库：socket activation（sd_listen_fds，继承 systemd
// 预先绑定的监听 socket）和就绪通知（sd_notify）。只用 systemd 219 就有的部分：219 没有 LISTEN_FDNAMES，
// 调用方按 socket 的地址区分用途。
package systemd

// Ready 是初始化完成、可以接流量时发给 systemd 的状态；Type=notify 的 systemctl start 阻塞到收到它。
const Ready = "READY=1"
