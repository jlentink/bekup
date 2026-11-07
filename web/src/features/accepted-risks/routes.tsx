import type { RouteObject } from "react-router";

import { access } from "@/lib/route-access";

/** The accepted security risks (D-228): every role; the server scopes it. */
export const acceptedRisksRoutes: RouteObject[] = [
  {
    path: "risks",
    handle: access.nav("/risks"),
    lazy: async () => ({
      Component: (await import("./accepted-risks-page")).AcceptedRisksPage,
    }),
  },
];
