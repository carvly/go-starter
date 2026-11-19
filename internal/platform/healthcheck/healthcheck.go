// Package healthcheck 实现 `<binary> healthcheck` 子命令，供 Dockerfile
// 的 HEALTHCHECK 指令调用。
//
// distroless/static 镜像里没有 shell、没有 curl/wget——常见的
// `HEALTHCHECK CMD curl -f http://localhost:PORT/healthz` 写法在这个
// 镜像里根本跑不起来（找不到 curl 这个可执行文件）。这里复用应用
// 自己已经编译好的同一个二进制，加一个极简的子命令自己发一次 HTTP
// 请求探活，不需要在镜像里额外装任何工具。
package healthcheck

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// Run 对 addr（形如 ":8080" 或 "0.0.0.0:8080"）的 /healthz 发一次 GET，
// 返回适合直接喂给 os.Exit 的退出码——0 表示健康，非 0 表示不健康，
// 跟 Docker/Podman HEALTHCHECK 判断容器状态的约定一致。
func Run(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: invalid addr %q: %v\n", addr, err)
		return 1
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: request failed: %v\n", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: unexpected status %d\n", resp.StatusCode)
		return 1
	}
	return 0
}
