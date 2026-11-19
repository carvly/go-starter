// shadcn CLI 写入的文件只经这个脚本修改，并记录 sha256，防止手改（手改会在下次升级时丢失）：
//
//   pnpm ui:add <组件...>   安装组件（及其依赖）后重写 ui.lock.json
//   pnpm ui:update          用 CLI 重新生成全部已安装组件后重写 ui.lock.json
//   pnpm ui:check           校验 CLI 管理的文件与 ui.lock.json 一致
//
// 定制不改这些文件：主题改 src/index.css 的 CSS 变量，行为在 src/components/ 里包一层。
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";

// CLI 管理的路径：ui 组件、它们依赖的 hooks、cn 工具函数。
const OWNED = ["src/components/ui", "src/hooks", "src/lib/utils.ts"];
const LOCK = "ui.lock.json";

function files(path: string): string[] {
  if (!existsSync(path)) return [];
  if (statSync(path).isFile()) return [path];
  return readdirSync(path)
    .flatMap((name) => files(join(path, name)))
    .sort();
}

function digest(): Record<string, string> {
  const out: Record<string, string> = {};
  for (const f of OWNED.flatMap(files)) {
    out[f] = createHash("sha256").update(readFileSync(f)).digest("hex");
  }
  return out;
}

function writeLock(): void {
  writeFileSync(LOCK, JSON.stringify(digest(), null, 2) + "\n");
}

function shadcn(args: string[]): void {
  execFileSync("pnpm", ["exec", "shadcn", "add", "--yes", ...args], { stdio: "inherit" });
}

const [command, ...args] = process.argv.slice(2);
switch (command) {
  case "add":
    if (args.length === 0) throw new Error("usage: pnpm ui:add <component...>");
    shadcn(args);
    writeLock();
    break;
  case "update": {
    const installed = files("src/components/ui").map((f) => basename(f, ".tsx"));
    shadcn(["--overwrite", ...installed]);
    writeLock();
    break;
  }
  case "check": {
    const want: Record<string, string> = existsSync(LOCK) ? JSON.parse(readFileSync(LOCK, "utf8")) : {};
    const got = digest();
    const problems = [
      ...Object.keys(got)
        .filter((f) => want[f] !== got[f])
        .map((f) => (want[f] ? `改动了 ${f}` : `未登记的 ${f}`)),
      ...Object.keys(want)
        .filter((f) => !(f in got))
        .map((f) => `缺少 ${f}`),
    ];
    if (problems.length > 0) {
      console.error(problems.join("\n"));
      console.error("\nshadcn CLI 管理的文件只能经 pnpm ui:add / pnpm ui:update 修改；定制写在 src/components/ 里。");
      process.exit(1);
    }
    break;
  }
  default:
    throw new Error("usage: pnpm ui:add <component...> | ui:update | ui:check");
}
