import { createBrowserRouter, type RouteObject } from "react-router";

import { allNavItems } from "@/components/shell/nav";
import { AppShell } from "@/components/shell/app-shell";
import { DashboardPage } from "@/routes/dashboard";
import { LoginPage } from "@/routes/login";
import { NotFoundPage, RouteErrorPage } from "@/routes/not-found";
import { PlaceholderPage } from "@/routes/placeholder";

/**
 * The /design living style guide is dev-only. `pnpm csp-check` also enables it
 * (VITE_ENABLE_DESIGN=true) in a separate build so every component is
 * exercised under the production CSP; the shipped dist never contains it.
 */
export const DESIGN_ROUTE_ENABLED =
  import.meta.env.DEV || import.meta.env.VITE_ENABLE_DESIGN === "true";

const placeholderRoutes: RouteObject[] = allNavItems()
  .filter((item) => item.to !== "/")
  .map((item) => ({
    path: item.to.slice(1),
    element: <PlaceholderPage title={item.label} task={item.task} />,
  }));

export const routes: RouteObject[] = [
  { path: "/login", element: <LoginPage />, errorElement: <RouteErrorPage /> },
  ...(DESIGN_ROUTE_ENABLED
    ? [
        {
          path: "/design",
          errorElement: <RouteErrorPage />,
          lazy: async () => {
            const { DesignPage } = await import("@/routes/design");
            return { Component: DesignPage };
          },
        } satisfies RouteObject,
      ]
    : []),
  {
    path: "/",
    element: <AppShell />,
    errorElement: <RouteErrorPage />,
    children: [{ index: true, element: <DashboardPage /> }, ...placeholderRoutes],
  },
  { path: "*", element: <NotFoundPage /> },
];

export function createRouter() {
  return createBrowserRouter(routes);
}
