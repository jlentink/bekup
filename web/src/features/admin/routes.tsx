import type { RouteObject } from "react-router";

import { access } from "@/lib/route-access";

/**
 * Task 6.9 admin routes inside the shell, admin only (RequireRole shapes
 * the UI and keeps the queries from running; the server enforces @hasRole).
 */
export const adminRoutes: RouteObject[] = [
  {
    path: "admin/channels",
    handle: access.nav("/admin/channels"),
    lazy: async () => ({
      Component: (await import("./channels/channels-page")).SharedChannelsPage,
    }),
  },
  {
    path: "admin/audit",
    handle: access.nav("/admin/audit"),
    lazy: async () => ({ Component: (await import("./audit/audit-page")).AuditLogPage }),
  },
  {
    path: "admin/settings",
    handle: access.nav("/admin/settings"),
    lazy: async () => ({ Component: (await import("./settings-layout")).SettingsLayout }),
    children: [
      {
        index: true,
        lazy: async () => ({ Component: (await import("./smtp/smtp-page")).SmtpSettingsPage }),
      },
      {
        path: "security-alerts",
        lazy: async () => ({
          Component: (await import("./security-alerts/security-alerts-page")).SecurityAlertsPage,
        }),
      },
      {
        path: "network",
        lazy: async () => ({
          Component: (await import("./network/network-page")).NetworkAllowlistPage,
        }),
      },
      {
        path: "configuration",
        handle: access.admin,
        lazy: async () => ({
          Component: (await import("./configuration/configuration-page")).ConfigurationPage,
        }),
      },
      {
        path: "recovery",
        lazy: async () => ({
          Component: (await import("./recovery/recovery-page")).RecoveryPage,
        }),
      },
    ],
  },
];
