import { createBrowserRouter, type RouteObject } from "react-router";

import { AppShell } from "@/components/shell/app-shell";
import { ShellSkeleton } from "@/components/shell/shell-skeleton";
import { ForcedEnrollment, ForcedPasswordChange } from "@/features/auth/forced-flows";
import { RequireRole } from "@/features/auth/require-role";
import { RequireSession } from "@/features/auth/session";
import { adminRoutes } from "@/features/admin/routes";
import { calendarRoutes } from "@/features/calendar/routes";
import { connectionsRoutes } from "@/features/connections/routes";
import { destinationsRoutes } from "@/features/destinations/routes";
import { jobsRoutes } from "@/features/jobs/routes";
import { restoreRoutes } from "@/features/restore/routes";
import { runsRoutes } from "@/features/runs/routes";
import {
  notificationSettingsRoute,
  notificationsRoutes,
  verifyEmailRoute,
} from "@/features/notifications/routes";
import { usersRoutes } from "@/features/users/routes";
import { access } from "@/lib/route-access";
import { LoginPage } from "@/routes/login";
import { LoginVerifyPage } from "@/routes/login-verify";
import { InShellErrorPage, NotFoundPage, RouteErrorPage } from "@/routes/not-found";

import { RootLayout } from "./root-layout";

/**
 * The /design living style guide is dev-only. `pnpm csp-check` also enables it
 * (VITE_ENABLE_DESIGN=true) in a separate build so every component is
 * exercised under the production CSP; the shipped dist never contains it.
 */
export const DESIGN_ROUTE_ENABLED =
  import.meta.env.DEV || import.meta.env.VITE_ENABLE_DESIGN === "true";

/** Signed-in pages inside the shell; each is its own lazy chunk. */
export const appRoutes: RouteObject[] = [
  {
    index: true,
    handle: access.nav("/"),
    lazy: async () => ({
      Component: (await import("@/features/dashboard/dashboard-page")).DashboardPage,
    }),
  },
  {
    path: "profile",
    handle: access.all,
    lazy: async () => ({ Component: (await import("@/routes/profile")).ProfileLayout }),
    children: [
      {
        index: true,
        lazy: async () => ({ Component: (await import("@/routes/profile")).ProfileAccountPage }),
      },
      {
        path: "security",
        lazy: async () => ({ Component: (await import("@/routes/profile")).ProfileSecurityPage }),
      },
      {
        path: "sessions",
        lazy: async () => ({ Component: (await import("@/routes/profile")).ProfileSessionsPage }),
      },
      notificationSettingsRoute,
    ],
  },
  ...usersRoutes,
  ...connectionsRoutes,
  ...destinationsRoutes,
  ...restoreRoutes,
  ...calendarRoutes,
  ...jobsRoutes,
  ...runsRoutes,
  ...notificationsRoutes,
  ...adminRoutes,
  { path: "*", handle: access.all, Component: NotFoundPage },
];

export const routes: RouteObject[] = [
  {
    Component: RootLayout,
    errorElement: <RouteErrorPage />,
    HydrateFallback: ShellSkeleton,
    children: [
      { path: "login", Component: LoginPage },
      { path: "login/verify", Component: LoginVerifyPage },
      verifyEmailRoute,
      ...(DESIGN_ROUTE_ENABLED
        ? [
            {
              path: "design",
              lazy: async () => {
                const { DesignPage } = await import("@/routes/design");
                return { Component: DesignPage };
              },
            } satisfies RouteObject,
          ]
        : []),
      {
        path: "/",
        element: (
          <RequireSession
            passwordChange={<ForcedPasswordChange />}
            enrollment={<ForcedEnrollment />}
          >
            <AppShell />
          </RequireSession>
        ),
        children: [
          {
            // Errors and pending pages stay inside the shell.
            errorElement: <InShellErrorPage />,
            element: <RequireRole />,
            children: appRoutes,
          },
        ],
      },
    ],
  },
];

export function createRouter() {
  return createBrowserRouter(routes);
}
