import { type Page, expect, test } from "@playwright/test";

// 前提：空库上刚用 `api staff create -username <E2E_ROOT_USER>` 建出的超级管理员，
// E2E_ROOT_PASSWORD 是它的临时密码。测试会改掉这个密码，再跑一次时在同一个库上换个用户名
// 重新建一个即可。
const rootUser = process.env.E2E_ROOT_USER ?? "root";
const rootTemp = process.env.E2E_ROOT_PASSWORD ?? "";

// 侧边栏里的菜单链接。首页也会列出能进入的页面，同名链接有两个，菜单相关的断言只看侧边栏。
const menu = (page: Page, name: string) => page.locator("[data-slot=sidebar]").getByRole("link", { name });

async function firstLogin(page: Page, username: string, temp: string, password: string) {
  await page.goto("/login");
  await page.getByLabel("用户名").fill(username);
  await page.getByLabel("密码").fill(temp);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page).toHaveURL(/\/change-password/);
  await page.getByLabel("临时密码").fill(temp);
  await page.getByLabel("新密码", { exact: true }).fill(password);
  await page.getByLabel("再次输入新密码").fill(password);
  await page.getByRole("button", { name: "保存" }).click();
  await expect(page).toHaveURL("/");
}

test("首次登录强制改密；没有权限的员工看不到菜单，接口返回 403", async ({ page, browser, baseURL }) => {
  expect(rootTemp, "设置 E2E_ROOT_PASSWORD 为超级管理员的临时密码").not.toBe("");
  const staff = `e2e-${Date.now()}`;
  let staffTemp = "";

  await test.step("超级管理员首次登录，必须先改密", async () => {
    await page.goto("/accounts");
    await expect(page).toHaveURL(/\/login/);
    await firstLogin(page, rootUser, rootTemp, "e2e-root-password");
    await expect(menu(page, "员工")).toBeVisible();
  });

  await test.step("超级管理员新建员工，拿到一次性临时密码", async () => {
    await menu(page, "员工").click();
    await page.getByRole("button", { name: "新建员工" }).click();
    await page.getByLabel("用户名").fill(staff);
    await page.getByRole("button", { name: "创建并生成临时密码" }).click();
    staffTemp = (await page.locator("code").innerText()).trim();
    expect(staffTemp.length).toBeGreaterThan(10);
  });

  await test.step("宽表格在自己的容器里横向滚动，不把整页撑宽", async () => {
    // 操作日志的"目标"列是完整的 UUID，窄屏上表格比内容区宽。布局的 flex 子元素没有 min-w-0 时，
    // 整页出现横向滚动条，右侧的列被截掉。
    await page.setViewportSize({ width: 1024, height: 768 });
    await page.goto("/audit-logs");
    await expect(page.locator("tbody tr").first()).toBeVisible();
    const width = await page.evaluate(() => ({ page: document.documentElement.scrollWidth, viewport: innerWidth }));
    expect(width.page).toBeLessThanOrEqual(width.viewport);
  });

  await test.step("普通员工看不到系统菜单，打开员工页去无权限页，直接调接口 403", async () => {
    // 手动建的上下文不继承配置里的 use，baseURL 要显式传。
    const context = await browser.newContext({ baseURL });
    const other = await context.newPage();
    await firstLogin(other, staff, staffTemp, "e2e-staff-password");
    await expect(menu(other, "首页")).toBeVisible();
    await expect(other.getByRole("link", { name: "员工" })).toHaveCount(0);
    await other.goto("/accounts");
    await expect(other).toHaveURL(/\/forbidden/);
    const res = await other.request.get("/admin/v1/accounts");
    expect(res.status()).toBe(403);
    await context.close();
  });
});
