import { Link, type ErrorComponentProps } from "@tanstack/react-router";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { errorMessage } from "@/lib/server-error";

// 路由 loader 失败（比如打开不存在的员工）和未知路径时显示，替换 TanStack Router 的英文默认页。
export function RouteError({ error }: ErrorComponentProps) {
  return <RouteMessage title="页面加载失败" description={errorMessage(error)} />;
}

export function RouteNotFound() {
  return <RouteMessage title="页面不存在" description="链接可能已失效。" />;
}

function RouteMessage({ title, description }: { title: string; description: string }) {
  return (
    <Alert className="max-w-xl">
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>
        {description}{" "}
        <Link to="/" className="underline underline-offset-4">
          回到首页
        </Link>
      </AlertDescription>
    </Alert>
  );
}
