import { describe, expect, it } from "vitest";

import { navForRole } from "./nav";

const labels = (role: Parameters<typeof navForRole>[0]) =>
  navForRole(role).flatMap((s) => s.items.map((i) => i.label));

describe("navForRole", () => {
  it("shows administration only to admins", () => {
    expect(labels("admin")).toContain("Users");
    expect(labels("maintainer")).not.toContain("Users");
    expect(labels("owner")).not.toContain("Audit log");
  });

  it("hides connections and destinations from owners", () => {
    expect(labels("maintainer")).toEqual(expect.arrayContaining(["Connections", "Destinations"]));
    expect(labels("owner")).not.toContain("Connections");
    expect(labels("owner")).not.toContain("Destinations");
  });

  it("drops empty sections", () => {
    expect(navForRole("owner").map((s) => s.label)).toEqual(["Overview", "Backups"]);
  });
});
