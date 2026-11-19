import { defineConfig, devices } from "@playwright/test";

// 端到端测试跑在真实后端上（compose 栈或本地的 api），不起 Vite：直接访问嵌入了前端的后台
// server。只用 Chromium：WebKit 在 http://localhost 上不接受 __Host- cookie（见 docs/agents/admin.md）。
export default defineConfig({
  testDir: "e2e",
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: { baseURL: process.env.E2E_BASE_URL ?? "http://localhost:8081", trace: "retain-on-failure" },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
