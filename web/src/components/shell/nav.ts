import {
  Bell,
  Cable,
  CalendarDays,
  DatabaseBackup,
  HardDrive,
  History,
  LayoutDashboard,
  RotateCcw,
  ScrollText,
  Settings,
  Users,
  type LucideIcon,
} from "lucide-react";

import type { Role } from "@/graphql/gen/graphql";
import { messages } from "@/lib/messages";

/**
 * D-011 roles as the server names them. A USER sees the backup pages; the
 * server narrows them to the jobs the user owns (owner grants, D-067).
 */
export type { Role };

export interface NavItem {
  to: string;
  label: string;
  icon: LucideIcon;
  roles: readonly Role[];
}

export interface NavSection {
  label: string;
  items: readonly NavItem[];
}

/** Role sets of the nav items; route guards reuse them (lib/route-access). */
export const ALL: readonly Role[] = ["ADMIN", "MAINTAINER", "USER"];
export const STAFF: readonly Role[] = ["ADMIN", "MAINTAINER"];
export const ADMIN: readonly Role[] = ["ADMIN"];

export const NAV: readonly NavSection[] = [
  {
    label: "Overview",
    items: [
      { to: "/", label: "Dashboard", icon: LayoutDashboard, roles: ALL },
      { to: "/calendar", label: "Calendar", icon: CalendarDays, roles: ALL },
    ],
  },
  {
    label: "Backups",
    items: [
      { to: "/jobs", label: "Jobs", icon: DatabaseBackup, roles: ALL },
      { to: "/runs", label: "Runs", icon: History, roles: ALL },
      { to: "/restores", label: "Restores", icon: RotateCcw, roles: ALL },
    ],
  },
  {
    label: "Infrastructure",
    items: [
      { to: "/connections", label: "Connections", icon: Cable, roles: STAFF },
      { to: "/destinations", label: "Destinations", icon: HardDrive, roles: STAFF },
    ],
  },
  {
    label: "Administration",
    items: [
      { to: "/admin/users", label: "Users", icon: Users, roles: ADMIN },
      { to: "/admin/channels", label: "Shared channels", icon: Bell, roles: ADMIN },
      { to: "/admin/audit", label: "Audit log", icon: ScrollText, roles: ADMIN },
      { to: "/admin/settings", label: "Settings", icon: Settings, roles: ADMIN },
    ],
  },
];

/**
 * Navigation visible to a role. This only shapes the UI; authorization is
 * enforced server-side by the authz matrix (D-053).
 */
export function navForRole(role: Role): NavSection[] {
  return NAV.map((section) => ({
    label: section.label,
    items: section.items.filter((item) => item.roles.includes(role)),
  })).filter((section) => section.items.length > 0);
}

export function allNavItems(): NavItem[] {
  return NAV.flatMap((s) => s.items);
}

/**
 * Titles of routes that are not (or more specific than) a nav item,
 * checked first. Patterns match whole paths.
 */
const ROUTE_TITLES: readonly [RegExp, string][] = [
  [/^\/restore\/?$/, "Restore"],
  [/^\/jobs\/new\/?$/, "New job"],
  [/^\/jobs\/[^/]+\/edit\/?$/, "Edit job"],
  [/^\/jobs\/[^/]+\/test-restores(\/[^/]+)?\/?$/, "Test restores"],
  [/^\/connections\/new\/?$/, "New connection"],
  [/^\/connections\/[^/]+\/edit\/?$/, "Edit connection"],
  [/^\/destinations\/new\/?$/, "New destination"],
  [/^\/destinations\/[^/]+\/edit\/?$/, "Edit destination"],
  [/^\/notifications\/?$/, "Notifications"],
  [/^\/verify-email\/?$/, "Verify e-mail"],
  [/^\/admin\/two-factor(\/|$)/, "Two-factor policy"],
  [/^\/profile(\/|$)/, messages.shell.profile],
];

/** True when pathname is `prefix` or below it (segment-aware). */
function under(pathname: string, prefix: string): boolean {
  if (prefix === "/") return pathname === "/";
  return pathname === prefix || pathname.startsWith(`${prefix}/`);
}

/** The page title for the top bar and document.title. */
export function titleFor(pathname: string): string {
  const route = ROUTE_TITLES.find(([pattern]) => pattern.test(pathname));
  if (route) return route[1];
  const match = allNavItems()
    .filter((item) => under(pathname, item.to))
    .sort((a, b) => b.to.length - a.to.length)[0];
  return match?.label ?? "bekup";
}
