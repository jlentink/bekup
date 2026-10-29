import { matchRoutes, type RouteObject } from "react-router";
import { describe, expect, it } from "vitest";

import { appRoutes } from "@/app/router";
import type { Role } from "@/graphql/gen/graphql";

import { allowedRoles, mayAccess, navRoles } from "./route-access";

/** Every route chain (root → leaf) below the shell. */
function chains(routes: RouteObject[], parents: RouteObject[] = []): RouteObject[][] {
  return routes.flatMap((r) =>
    r.children?.length ? chains(r.children, [...parents, r]) : [[...parents, r]],
  );
}

const shell: RouteObject[] = [{ path: "/", children: appRoutes }];

function rolesAt(path: string): readonly Role[] | null {
  const matches = matchRoutes(shell, path) ?? [];
  return allowedRoles(matches.map((m) => ({ handle: m.route.handle as unknown })));
}

describe("route access", () => {
  it("declares access on every signed-in route (deny by default otherwise)", () => {
    for (const chain of chains(appRoutes)) {
      const label = chain.map((r) => (r.index ? "(index)" : r.path)).join("/");
      expect(
        allowedRoles(chain.map((r) => ({ handle: r.handle as unknown }))),
        label,
      ).not.toBeNull();
    }
  });

  it.each([
    ["/", ["ADMIN", "MAINTAINER", "USER"]],
    ["/calendar", ["ADMIN", "MAINTAINER", "USER"]],
    ["/jobs", ["ADMIN", "MAINTAINER", "USER"]],
    ["/jobs/j1", ["ADMIN", "MAINTAINER", "USER"]],
    ["/jobs/j1/test-restores/t1", ["ADMIN", "MAINTAINER", "USER"]],
    ["/jobs/new", ["ADMIN", "MAINTAINER"]],
    ["/jobs/j1/edit", ["ADMIN", "MAINTAINER"]],
    ["/runs/r1", ["ADMIN", "MAINTAINER", "USER"]],
    ["/restore", ["ADMIN", "MAINTAINER", "USER"]],
    ["/restores/x", ["ADMIN", "MAINTAINER", "USER"]],
    ["/connections", ["ADMIN", "MAINTAINER"]],
    ["/connections/c1/edit", ["ADMIN", "MAINTAINER"]],
    ["/destinations/new", ["ADMIN", "MAINTAINER"]],
    ["/admin/users/u1", ["ADMIN"]],
    ["/admin/two-factor", ["ADMIN"]],
    ["/admin/channels", ["ADMIN"]],
    ["/admin/audit", ["ADMIN"]],
    ["/admin/settings/network", ["ADMIN"]],
    ["/notifications", ["ADMIN", "MAINTAINER", "USER"]],
    ["/profile/notifications", ["ADMIN", "MAINTAINER", "USER"]],
    ["/nowhere", ["ADMIN", "MAINTAINER", "USER"]],
  ])("%s → %j", (path, want) => {
    expect([...(rolesAt(path) ?? [])].sort()).toEqual([...want].sort());
  });

  it("matches the nav roles", () => {
    expect(rolesAt("/connections")).toEqual(navRoles("/connections"));
    expect(rolesAt("/admin/settings")).toEqual(navRoles("/admin/settings"));
    expect(() => navRoles("/nope")).toThrow();
  });

  it("lets a child only narrow its parent and denies without a handle", () => {
    expect(
      allowedRoles([
        { handle: { roles: ["ADMIN", "USER"] } },
        { handle: { roles: ["USER", "MAINTAINER"] } },
      ]),
    ).toEqual(["USER"]);
    expect(allowedRoles([{ handle: undefined }])).toBeNull();
    expect(mayAccess("ADMIN", null)).toBe(false);
    expect(mayAccess("USER", ["ADMIN"])).toBe(false);
    expect(mayAccess("ADMIN", ["ADMIN"])).toBe(true);
  });
});
