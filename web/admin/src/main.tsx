import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createRouter } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { ApiError } from "@/api/errors";
import { RouteError, RouteNotFound } from "@/components/route-error";
import { toast } from "@/components/ui/toast";
import { type HandlesErrors, describeError, handledByCaller } from "@/lib/server-error";

import "./index.css";
import { routeTree } from "./routeTree.gen";

// 统一错误处理：401 去登录、需要改密去改密页、读数据 403 去无权限页，其余弹提示。
// 自己展示错误的 mutation 用 meta.handlesErrors 跳过这里（全部，或只跳过列出的错误码）。
function onServerError(error: unknown, source: "query" | "mutation") {
  const action = describeError(error, source);
  // 正在导航时，查询是路由 beforeLoad / loader 发起的：401 / 改密 / 无权限由守卫 throw redirect
  // 处理，而且它知道目标地址，这里再导航会和它抢、回跳地址也会用成旧页面的；其它错误由路由的
  // errorComponent 显示，这里再弹提示就重复了。
  if (source === "query" && router.state.status === "pending") return;
  const { pathname, href } = router.state.location;
  switch (action.kind) {
    case "login":
      if (pathname !== "/login") void router.navigate({ to: "/login", search: { redirect: href } });
      break;
    case "change-password":
      if (pathname !== "/change-password") void router.navigate({ to: "/change-password" });
      break;
    case "forbidden":
      void router.navigate({ to: "/forbidden" });
      break;
    case "notify":
      toast.add({ title: action.message, type: "error" });
  }
}

const queryClient = new QueryClient({
  queryCache: new QueryCache({ onError: (error) => onServerError(error, "query") }),
  mutationCache: new MutationCache({
    onError: (error, _vars, _ctx, mutation) => {
      if (!handledByCaller(mutation.meta?.handlesErrors, error)) onServerError(error, "mutation");
    },
  }),
  defaultOptions: {
    // 4xx 是确定的结果，重试没有意义；只重试网络错误和 5xx。
    queries: { retry: (count, error) => !(error instanceof ApiError && error.status < 500) && count < 2 },
  },
});

const router = createRouter({
  routeTree,
  context: { queryClient },
  defaultPreload: "intent",
  defaultErrorComponent: RouteError,
  defaultNotFoundComponent: RouteNotFound,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

declare module "@tanstack/react-query" {
  interface Register {
    mutationMeta: { handlesErrors?: HandlesErrors };
  }
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
