import {
  Bell,
  Cable,
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

/** D-011 roles. "owner" is a user who owns at least one job. */
export type Role = "admin" | "maintainer" | "owner";

export interface NavItem {
  to: string;
  label: string;
  icon: LucideIcon;
  roles: readonly Role[];
  /** Plan task that builds the page (placeholder until then). */
  task: string;
}

export interface NavSection {
  label: string;
  items: readonly NavItem[];
}

const ALL: readonly Role[] = ["admin", "maintainer", "owner"];
const STAFF: readonly Role[] = ["admin", "maintainer"];
const ADMIN: readonly Role[] = ["admin"];

export const NAV: readonly NavSection[] = [
  {
    label: "Overview",
    items: [{ to: "/", label: "Dashboard", icon: LayoutDashboard, roles: ALL, task: "6.2" }],
  },
  {
    label: "Backups",
    items: [
      { to: "/jobs", label: "Jobs", icon: DatabaseBackup, roles: ALL, task: "6.5" },
      { to: "/runs", label: "Runs", icon: History, roles: ALL, task: "6.6" },
      { to: "/restores", label: "Restores", icon: RotateCcw, roles: ALL, task: "6.7" },
    ],
  },
  {
    label: "Infrastructure",
    items: [
      { to: "/connections", label: "Connections", icon: Cable, roles: STAFF, task: "6.3" },
      { to: "/destinations", label: "Destinations", icon: HardDrive, roles: STAFF, task: "6.4" },
    ],
  },
  {
    label: "Administration",
    items: [
      { to: "/admin/users", label: "Users", icon: Users, roles: ADMIN, task: "6.8" },
      { to: "/admin/channels", label: "Shared channels", icon: Bell, roles: ADMIN, task: "6.9" },
      { to: "/admin/audit", label: "Audit log", icon: ScrollText, roles: ADMIN, task: "6.9" },
      { to: "/admin/settings", label: "Settings", icon: Settings, roles: ADMIN, task: "6.9" },
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
