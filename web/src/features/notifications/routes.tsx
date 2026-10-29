import type { RouteObject } from "react-router";

import { access } from "@/lib/route-access";

/** Task 6.10 pages inside the shell: the inbox. */
export const notificationsRoutes: RouteObject[] = [
  {
    path: "notifications",
    handle: access.all,
    lazy: async () => ({ Component: (await import("./inbox-page")).InboxPage }),
  },
];

/**
 * /verify-email#code=… lives outside the session gate: the gate would put
 * the current URL, fragment included, into the login return path. The page
 * clears the fragment first and handles a missing session itself (D-178).
 */
export const verifyEmailRoute: RouteObject = {
  path: "verify-email",
  lazy: async () => ({ Component: (await import("./verify-email-page")).VerifyEmailPage }),
};

/** Profile → Notifications. */
export const notificationSettingsRoute: RouteObject = {
  path: "notifications",
  lazy: async () => ({
    Component: (await import("./settings-page")).NotificationSettingsPage,
  }),
};
