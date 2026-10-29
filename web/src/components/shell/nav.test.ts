import { describe, expect, it } from "vitest";

import { navForRole, titleFor } from "./nav";

const labels = (role: Parameters<typeof navForRole>[0]) =>
  navForRole(role).flatMap((s) => s.items.map((i) => i.label));

describe("navForRole", () => {
  it("shows administration only to admins", () => {
    expect(labels("ADMIN")).toContain("Users");
    expect(labels("MAINTAINER")).not.toContain("Users");
    expect(labels("USER")).not.toContain("Audit log");
  });

  it("hides connections and destinations from users", () => {
    expect(labels("MAINTAINER")).toEqual(expect.arrayContaining(["Connections", "Destinations"]));
    expect(labels("USER")).not.toContain("Connections");
    expect(labels("USER")).not.toContain("Destinations");
  });

  it("gives every role the dashboard, calendar and backup pages", () => {
    for (const role of ["ADMIN", "MAINTAINER", "USER"] as const) {
      expect(labels(role)).toEqual(
        expect.arrayContaining(["Dashboard", "Calendar", "Jobs", "Runs", "Restores"]),
      );
    }
  });

  it("drops empty sections", () => {
    expect(navForRole("USER").map((s) => s.label)).toEqual(["Overview", "Backups"]);
  });
});

describe("titleFor", () => {
  it.each([
    ["/", "Dashboard"],
    ["/restore", "Restore"],
    ["/restore/", "Restore"],
    ["/restores", "Restores"],
    ["/restores/abc", "Restores"],
    ["/runs", "Runs"],
    ["/runs/abc", "Runs"],
    ["/jobs", "Jobs"],
    ["/jobs/new", "New job"],
    ["/jobs/abc", "Jobs"],
    ["/jobs/abc/edit", "Edit job"],
    ["/jobs/abc/test-restores", "Test restores"],
    ["/jobs/abc/test-restores/def", "Test restores"],
    ["/connections/new", "New connection"],
    ["/connections/abc/edit", "Edit connection"],
    ["/destinations/new", "New destination"],
    ["/destinations/abc", "Destinations"],
    ["/calendar", "Calendar"],
    ["/admin/users/abc", "Users"],
    ["/admin/two-factor", "Two-factor policy"],
    ["/profile/security", "Profile and security"],
    ["/notifications", "Notifications"],
    ["/profile/notifications", "Profile and security"],
    ["/admin/settings/network", "Settings"],
    ["/runsx", "bekup"],
    ["/nowhere", "bekup"],
  ])("%s → %s", (path, title) => {
    expect(titleFor(path)).toBe(title);
  });
});
