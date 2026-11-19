import { createFileRoute } from "@tanstack/react-router";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";

export const Route = createFileRoute("/_authed/forbidden")({
  component: () => (
    <Alert>
      <AlertTitle>没有权限</AlertTitle>
      <AlertDescription>你没有访问这个页面的权限。需要的话，请联系超级管理员给你分配相应的角色，处理后刷新页面。</AlertDescription>
    </Alert>
  ),
});
