import type { UIMatch } from "react-router";

import { ADMIN, ALL, allNavItems, STAFF, type Role } from "@/components/shell/nav";

/**
 * Who may open a route, declared as the route's `handle`. Derived from the
 * nav item roles so the menu and the guard cannot drift apart. This only
 * shapes the UI (and keeps a page's queries from running for other roles);
 * the server enforces every operation through the authz matrix (D-053).
 */
export interface RouteAccess {
  roles: readonly Role[];
}

/** Roles of the nav item at `to`; an unknown path is a programming error. */
export function navRoles(to: string): readonly Role[] {
  const item = allNavItems().find((i) => i.to === to);
  if (!item) throw new Error(`route-access: no nav item ${to}`);
  return item.roles;
}

/** Route handles: `handle: access.nav("/connections")`, `handle: access.staff`, ... */
export const access = {
  nav: (to: string): RouteAccess => ({ roles: navRoles(to) }),
  all: { roles: ALL } satisfies RouteAccess,
  staff: { roles: STAFF } satisfies RouteAccess,
  admin: { roles: ADMIN } satisfies RouteAccess,
} as const;

function isAccess(handle: unknown): handle is RouteAccess {
  return (
    typeof handle === "object" &&
    handle !== null &&
    Array.isArray((handle as { roles?: unknown }).roles)
  );
}

/**
 * The roles allowed by the matched route chain: every handle along the
 * chain must allow the role (a child can only narrow its parent). null
 * when no route declares access: deny by default.
 */
export function allowedRoles(matches: readonly Pick<UIMatch, "handle">[]): readonly Role[] | null {
  let roles: readonly Role[] | null = null;
  for (const m of matches) {
    if (!isAccess(m.handle)) continue;
    const next = m.handle.roles;
    roles = roles === null ? next : roles.filter((r) => next.includes(r));
  }
  return roles;
}

export function mayAccess(role: Role, roles: readonly Role[] | null): boolean {
  return roles?.includes(role) ?? false;
}
