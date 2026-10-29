import type { RouteObject } from "react-router";

import { access } from "@/lib/route-access";

/**
 * Task 6.6 run routes inside the shell. The restore wizard (task 6.7) is opened
 * from artifacts via `/restore?artifactId=<id>`.
 */
export const runsRoutes: RouteObject[] = [
  {
    path: "runs",
    handle: access.nav("/runs"),
    lazy: async () => ({ Component: (await import("./runs-list-page")).RunsListPage }),
  },
  {
    path: "runs/:runId",
    handle: access.nav("/runs"),
    lazy: async () => ({ Component: (await import("./run-detail-page")).RunDetailPage }),
  },
];
