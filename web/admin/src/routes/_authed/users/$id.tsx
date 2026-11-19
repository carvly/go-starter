import { createFileRoute } from "@tanstack/react-router";

import { page } from "@/features/auth/guard";
import { UserDetailPage } from "@/features/users/detail";
import { userQuery } from "@/features/users/queries";

export const Route = createFileRoute("/_authed/users/$id")({
  ...page({ permission: "user:read" }),
  loader: ({ context, params }) => context.queryClient.ensureQueryData(userQuery(params.id)),
  component: function UserDetail() {
    const { id } = Route.useParams();
    return <UserDetailPage id={id} />;
  },
});
