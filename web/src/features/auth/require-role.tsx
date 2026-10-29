import { ShieldAlert } from "lucide-react";
import { Link, Outlet, useMatches } from "react-router";

import { Button } from "@/components/ui/button";
import type { Role } from "@/graphql/gen/graphql";
import { messages } from "@/lib/messages";
import { allowedRoles, mayAccess } from "@/lib/route-access";

import { useSessionUser } from "./use-session-user";

const M = messages.access;

/**
 * The one route guard: a layout route around every signed-in page. It
 * reads the `handle` (RouteAccess) of the matched routes and renders the
 * page only for an allowed role; a route without a declared handle is
 * denied. It only shapes the UI; the server refuses every operation
 * outside the authz matrix anyway.
 */
export function RequireRole() {
  const user = useSessionUser();
  const roles = allowedRoles(useMatches());
  if (!mayAccess(user.role, roles)) return <NoAccess roles={roles ?? []} />;
  return <Outlet />;
}

function copyFor(roles: readonly Role[]): { title: string; lead: string } {
  if (roles.length === 1 && roles[0] === "ADMIN") return M.admin;
  if (roles.length === 2 && roles.includes("ADMIN") && roles.includes("MAINTAINER")) {
    return M.staff;
  }
  return M.other;
}

function NoAccess({ roles }: { roles: readonly Role[] }) {
  const { title, lead } = copyFor(roles);
  return (
    <div className="flex max-w-lg flex-col items-start gap-3 py-16">
      <span
        aria-hidden="true"
        className="inline-flex size-10 items-center justify-center rounded-md border bg-surface text-muted-foreground"
      >
        <ShieldAlert className="size-5" />
      </span>
      <h1 className="font-display text-3xl">{title}</h1>
      <p className="text-sm text-muted-foreground">{lead}</p>
      <Button asChild variant="outline" size="sm">
        <Link to="/">{M.backHome}</Link>
      </Button>
    </div>
  );
}
