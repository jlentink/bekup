import type { RouteObject } from "react-router";

import { access } from "@/lib/route-access";

/** /connections pages (task 6.3); each page is its own lazy chunk. */
export const connectionsRoutes: RouteObject[] = [
  {
    path: "connections",
    handle: access.nav("/connections"),
    lazy: async () => ({
      Component: (await import("./connections-page")).ConnectionsPage,
    }),
  },
  {
    path: "connections/new",
    handle: access.nav("/connections"),
    lazy: async () => ({
      Component: (await import("./connection-form-page")).NewConnectionPage,
    }),
  },
  {
    path: "connections/:id",
    handle: access.nav("/connections"),
    lazy: async () => ({
      Component: (await import("./connection-detail-page")).ConnectionDetailPage,
    }),
  },
  {
    path: "connections/:id/edit",
    handle: access.nav("/connections"),
    lazy: async () => ({
      Component: (await import("./connection-form-page")).EditConnectionPage,
    }),
  },
];
