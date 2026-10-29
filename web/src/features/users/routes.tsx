import type { RouteObject } from "react-router";

import { access } from "@/lib/route-access";

/**
 * Task 6.8 routes inside the shell: the user admin and the 2FA policy.
 * Admin only (RequireRole shapes the UI; the server enforces @hasRole).
 */
export const usersRoutes: RouteObject[] = [
  {
    path: "admin/users",
    handle: access.nav("/admin/users"),
    lazy: async () => ({ Component: (await import("./users-page")).UsersPage }),
  },
  {
    path: "admin/users/:userId",
    handle: access.nav("/admin/users"),
    lazy: async () => ({ Component: (await import("./user-detail-page")).UserDetailPage }),
  },
  {
    path: "admin/two-factor",
    handle: access.admin,
    lazy: async () => ({
      Component: (await import("@/features/admin/two-factor/two-factor-policy-page"))
        .TwoFactorPolicyPage,
    }),
  },
];
