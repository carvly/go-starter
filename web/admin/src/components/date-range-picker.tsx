import { CalendarIcon, X } from "lucide-react";
import { useState } from "react";
import { zhCN } from "react-day-picker/locale";

import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { formatDay, parseDay } from "@/lib/format";

// 日期是 yyyy-mm-dd（含两端），方便直接放进 URL 的 search；换算成后端的时间区间用 dayRange。
export type DayRange = { from?: string; to?: string };

export function DateRangePicker({
  id,
  value,
  onChange,
  placeholder = "选择日期",
}: {
  id?: string;
  value: DayRange;
  onChange: (range: DayRange) => void;
  placeholder?: string;
}) {
  // 未来的日期不可选。挂载时取一次当天：渲染时直接 new Date() 不纯。
  const [today] = useState(() => new Date());
  const from = parseDay(value.from);
  const to = parseDay(value.to);
  const label = value.from ? (value.to && value.to !== value.from ? `${value.from} 至 ${value.to}` : value.from) : placeholder;
  return (
    <div className="flex items-center gap-1">
      <Popover>
        <PopoverTrigger
          render={<Button id={id} variant="outline" className="w-60 justify-start font-normal data-[empty=true]:text-muted-foreground" />}
          data-empty={!value.from}
        >
          <CalendarIcon />
          {label}
        </PopoverTrigger>
        <PopoverContent className="w-auto p-0" align="start">
          <Calendar
            mode="range"
            locale={zhCN}
            numberOfMonths={2}
            defaultMonth={from}
            selected={from ? { from, to } : undefined}
            onSelect={(r) => onChange({ from: r?.from && formatDay(r.from), to: r?.to && formatDay(r.to) })}
            disabled={{ after: today }}
          />
        </PopoverContent>
      </Popover>
      {value.from && (
        <Button variant="ghost" size="icon-sm" aria-label="清除日期" onClick={() => onChange({})}>
          <X />
        </Button>
      )}
    </div>
  );
}
