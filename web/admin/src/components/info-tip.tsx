import { Info } from "lucide-react";
import type { ReactNode } from "react";

import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";

// InfoTip 是标题、表头旁的 ⓘ：补充说明和操作指引放进去，页面上只留必须一直看到的提示。
// 用 Popover 而不是 Tooltip：悬停、点按（触屏没有悬停）、键盘聚焦都能打开，里面可以放链接。
export function InfoTip({ children, label = "说明" }: { children: ReactNode; label?: string }) {
  return (
    <Popover>
      <PopoverTrigger
        openOnHover
        delay={150}
        aria-label={label}
        className="inline-flex shrink-0 rounded-full align-middle text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
      >
        <Info className="size-4" />
      </PopoverTrigger>
      <PopoverContent className="gap-2 leading-relaxed">{children}</PopoverContent>
    </Popover>
  );
}

// TitleWithTip 是"标题 + ⓘ"的常用组合，用于页面标题、卡片标题和表头。
export function TitleWithTip({ children, tip }: { children: ReactNode; tip: ReactNode }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      {children}
      <InfoTip>{tip}</InfoTip>
    </span>
  );
}
