import {
  Bell,
  Cable,
  CalendarDays,
  DatabaseBackup,
  HardDrive,
  History,
  KeyRound,
  LayoutDashboard,
  RotateCcw,
  ShieldCheck,
  ScrollText,
  Settings,
  Users,
  type LucideIcon,
} from "lucide-react";

import type { Role } from "@/graphql/gen/graphql";
import { defineMessages } from "@/lib/i18n";
import { messages } from "@/lib/messages";

const T = defineMessages({
  en: {
    sections: {
      overview: "Overview",
      backups: "Backups",
      infrastructure: "Infrastructure",
      administration: "Administration",
    },
    items: {
      dashboard: "Dashboard",
      calendar: "Calendar",
      risks: "Accepted risks",
      jobs: "Jobs",
      runs: "Runs",
      restores: "Restores",
      connections: "Connections",
      destinations: "Destinations",
      users: "Users",
      channels: "Shared channels",
      apiAccess: "API access",
      audit: "Audit log",
      settings: "Settings",
    },
    routes: {
      restore: "Restore",
      newJob: "New job",
      editJob: "Edit job",
      testRestores: "Test restores",
      newConnection: "New connection",
      editConnection: "Edit connection",
      newDestination: "New destination",
      editDestination: "Edit destination",
      notifications: "Notifications",
      verifyEmail: "Verify e-mail",
      twoFactor: "Two-factor policy",
    },
  },
  nl: {
    sections: {
      overview: "Overzicht",
      backups: "Back-ups",
      infrastructure: "Infrastructuur",
      administration: "Beheer",
    },
    items: {
      dashboard: "Dashboard",
      calendar: "Kalender",
      risks: "Geaccepteerde risico's",
      jobs: "Taken",
      runs: "Runs",
      restores: "Herstel",
      connections: "Verbindingen",
      destinations: "Bestemmingen",
      users: "Gebruikers",
      channels: "Gedeelde kanalen",
      apiAccess: "API-toegang",
      audit: "Auditlog",
      settings: "Instellingen",
    },
    routes: {
      restore: "Herstellen",
      newJob: "Nieuwe taak",
      editJob: "Taak bewerken",
      testRestores: "Testherstel",
      newConnection: "Nieuwe verbinding",
      editConnection: "Verbinding bewerken",
      newDestination: "Nieuwe bestemming",
      editDestination: "Bestemming bewerken",
      notifications: "Meldingen",
      verifyEmail: "E-mailadres bevestigen",
      twoFactor: "Beleid voor tweestapsverificatie",
    },
  },
});

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
    label: T.sections.overview,
    items: [
      { to: "/", label: T.items.dashboard, icon: LayoutDashboard, roles: ALL },
      { to: "/calendar", label: T.items.calendar, icon: CalendarDays, roles: ALL },
      { to: "/risks", label: T.items.risks, icon: ShieldCheck, roles: ALL },
    ],
  },
  {
    label: T.sections.backups,
    items: [
      { to: "/jobs", label: T.items.jobs, icon: DatabaseBackup, roles: ALL },
      { to: "/runs", label: T.items.runs, icon: History, roles: ALL },
      { to: "/restores", label: T.items.restores, icon: RotateCcw, roles: ALL },
    ],
  },
  {
    label: T.sections.infrastructure,
    items: [
      { to: "/connections", label: T.items.connections, icon: Cable, roles: STAFF },
      { to: "/destinations", label: T.items.destinations, icon: HardDrive, roles: STAFF },
    ],
  },
  {
    label: T.sections.administration,
    items: [
      { to: "/admin/users", label: T.items.users, icon: Users, roles: ADMIN },
      { to: "/admin/channels", label: T.items.channels, icon: Bell, roles: ADMIN },
      { to: "/admin/api-access", label: T.items.apiAccess, icon: KeyRound, roles: ADMIN },
      { to: "/admin/audit", label: T.items.audit, icon: ScrollText, roles: ADMIN },
      { to: "/admin/settings", label: T.items.settings, icon: Settings, roles: ADMIN },
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
  [/^\/restore\/?$/, T.routes.restore],
  [/^\/jobs\/new\/?$/, T.routes.newJob],
  [/^\/jobs\/[^/]+\/edit\/?$/, T.routes.editJob],
  [/^\/jobs\/[^/]+\/test-restores(\/[^/]+)?\/?$/, T.routes.testRestores],
  [/^\/connections\/new\/?$/, T.routes.newConnection],
  [/^\/connections\/[^/]+\/edit\/?$/, T.routes.editConnection],
  [/^\/destinations\/new\/?$/, T.routes.newDestination],
  [/^\/destinations\/[^/]+\/edit\/?$/, T.routes.editDestination],
  [/^\/notifications\/?$/, T.routes.notifications],
  [/^\/verify-email\/?$/, T.routes.verifyEmail],
  [/^\/admin\/two-factor(\/|$)/, T.routes.twoFactor],
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
