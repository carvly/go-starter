import { fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// 开发时浏览器只访问 Vite（localhost），/admin/v1 由它代理到本地 admin server：
// 同源，会话 cookie 照常工作，不需要 CORS。地址与 HTTP_ADMIN_ADDR 一致。
const adminServer = process.env.ADMIN_SERVER ?? "http://127.0.0.1:8081";

export default defineConfig({
  plugins: [tanstackRouter({ target: "react", autoCodeSplitting: true }), react(), tailwindcss()],
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  // 不清空 dist/：里面有提交的 .placeholder（go:embed 需要），由 scripts/clean-dist.ts 在构建前清理。
  build: { emptyOutDir: false },
  server: {
    proxy: { "/admin/v1": { target: adminServer } },
  },
});
