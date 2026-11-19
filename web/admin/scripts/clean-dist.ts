// 构建前清空 dist/，但保留已提交的 dist/.placeholder：web/admin/embed.go 用 go:embed 嵌入
// dist/，目录里一个文件都没有时 go build 会失败。vite.config.ts 因此关掉了 emptyOutDir。
import { mkdirSync, readdirSync, rmSync } from "node:fs";

const dir = new URL("../dist/", import.meta.url);
mkdirSync(dir, { recursive: true });
for (const name of readdirSync(dir)) {
  if (name !== ".placeholder") rmSync(new URL(name, dir), { recursive: true, force: true });
}
