// 从 api/admin.yaml 生成 src/api/schema.d.ts（只有类型，编译后全部擦除）。
//
// openapi-typescript 依赖 TypeScript 5 的编译器 API，在本项目的 TS 7 下无法运行，所以用
// pnpm dlx 单独装一份 TS 5 来跑生成器；项目本身仍用 TS 7。两个版本号由
// scripts/check-versions.sh 检查，升级时同时改这里。
import { execFileSync } from "node:child_process";

const OPENAPI_TYPESCRIPT = "7.13.0";
const TYPESCRIPT = "5.9.3";

execFileSync(
  "pnpm",
  [
    "dlx",
    `--package=openapi-typescript@${OPENAPI_TYPESCRIPT}`,
    `--package=typescript@${TYPESCRIPT}`,
    "openapi-typescript",
    "../../api/admin.yaml",
    "--output",
    "src/api/schema.d.ts",
  ],
  { stdio: "inherit" },
);
