import type { RouteObject } from "react-router";

import { access } from "@/lib/route-access";

/** Restore pages (task 6.7): the wizard, the history and one restore. */
export const restoreRoutes: RouteObject[] = [
  {
    path: "restore",
    handle: access.nav("/restores"),
    lazy: async () => ({ Component: (await import("./wizard-page")).RestoreWizardPage }),
  },
  {
    path: "restores",
    handle: access.nav("/restores"),
    lazy: async () => ({ Component: (await import("./history-page")).RestoresPage }),
  },
  {
    path: "restores/:restoreId",
    handle: access.nav("/restores"),
    lazy: async () => ({ Component: (await import("./history-page")).RestoreDetailPage }),
  },
];
