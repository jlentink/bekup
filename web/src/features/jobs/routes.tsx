import type { RouteObject } from "react-router";

import { access } from "@/lib/route-access";

const jobs = access.nav("/jobs");

/**
 * Task 6.5/6.6 job routes inside the shell. Every role sees jobs (users
 * only their own, server-side); the editor is for admins and maintainers.
 */
export const jobsRoutes: RouteObject[] = [
  {
    path: "jobs",
    handle: jobs,
    lazy: async () => ({ Component: (await import("./jobs-list-page")).JobsListPage }),
  },
  {
    path: "jobs/new",
    handle: access.staff,
    lazy: async () => ({ Component: (await import("./job-editor-page")).JobCreatePage }),
  },
  {
    path: "jobs/:jobId",
    handle: jobs,
    lazy: async () => ({ Component: (await import("./job-detail-page")).JobDetailPage }),
  },
  {
    path: "jobs/:jobId/test-restores",
    handle: jobs,
    lazy: async () => ({
      Component: (await import("./test-restores-page")).TestRestoreHistoryPage,
    }),
  },
  {
    path: "jobs/:jobId/test-restores/:testRestoreId",
    handle: jobs,
    lazy: async () => ({
      Component: (await import("./test-restores-page")).TestRestoreDetailPage,
    }),
  },
  {
    path: "jobs/:jobId/edit",
    handle: access.staff,
    lazy: async () => ({ Component: (await import("./job-editor-page")).JobEditPage }),
  },
];
