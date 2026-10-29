import type { RouteObject } from "react-router";

import { access } from "@/lib/route-access";

/** The job calendar (task 6.11). */
export const calendarRoutes: RouteObject[] = [
  {
    path: "calendar",
    handle: access.nav("/calendar"),
    lazy: async () => ({ Component: (await import("./calendar-page")).CalendarPage }),
  },
];
