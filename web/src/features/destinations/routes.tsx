import type { RouteObject } from "react-router";

import { access } from "@/lib/route-access";

/** /destinations pages (task 6.4); each page is its own lazy chunk. */
export const destinationsRoutes: RouteObject[] = [
  {
    path: "destinations",
    handle: access.nav("/destinations"),
    lazy: async () => ({
      Component: (await import("./destinations-page")).DestinationsPage,
    }),
  },
  {
    path: "destinations/new",
    handle: access.nav("/destinations"),
    lazy: async () => ({
      Component: (await import("./destination-form-page")).NewDestinationPage,
    }),
  },
  {
    path: "destinations/:id",
    handle: access.nav("/destinations"),
    lazy: async () => ({
      Component: (await import("./destination-detail-page")).DestinationDetailPage,
    }),
  },
  {
    path: "destinations/:id/edit",
    handle: access.nav("/destinations"),
    lazy: async () => ({
      Component: (await import("./destination-form-page")).EditDestinationPage,
    }),
  },
];
