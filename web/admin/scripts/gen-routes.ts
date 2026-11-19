// 重新生成 src/routeTree.gen.ts：tools/gen 新增路由文件之后调用，不必跑完整的构建。路由树由
// vite.config.ts 里的 TanStack Router 插件在 buildStart 时生成，这里只启动插件、不打包。
import { createServer } from "vite";

const server = await createServer({ server: { middlewareMode: true, hmr: false, watch: null }, logLevel: "warn" });
try {
  await server.pluginContainer.buildStart({});
} finally {
  await server.close();
}
