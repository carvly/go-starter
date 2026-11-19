// Package admin 把后台前端的构建产物（pnpm build 输出的 dist/）嵌入二进制。
// dist/.placeholder 已提交，没有构建前端时 go build 照常通过，页面换成 unbuilt/ 里的提示页。
package admin

import (
	"embed"
	"io/fs"
)

//go:embed all:dist unbuilt
var files embed.FS

// Dist 返回要托管的页面根目录（index.html、assets/）：构建过就是 dist/，否则是提示怎么构建的页面。
func Dist() fs.FS {
	dir := "dist"
	if _, err := fs.Stat(files, "dist/index.html"); err != nil {
		dir = "unbuilt"
	}
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic(err) // dir 是编译期确定的合法路径，不会出错
	}
	return sub
}
