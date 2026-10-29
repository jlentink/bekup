import { describe, expect, it } from "vitest";

import { accountFlags, isLocked, ownershipBlock, twoFactorState } from "./status";

const NOW = Date.parse("2026-10-28T12:00:00Z");
const base = {
  deletedAt: null,
  disabled: false,
  lockedUntil: null,
  passwordChangeRequired: false,
  temporaryPasswordExpiresAt: null,
};

describe("accountFlags", () => {
  it.each([
    [{}, ["active"]],
    [{ disabled: true }, ["disabled"]],
    [{ lockedUntil: "2026-10-28T12:10:00Z" }, ["locked"]],
    [{ lockedUntil: "2026-10-28T11:00:00Z" }, ["active"]],
    [
      { passwordChangeRequired: true, temporaryPasswordExpiresAt: "2026-10-30T00:00:00Z" },
      ["temporary"],
    ],
    [
      { passwordChangeRequired: true, temporaryPasswordExpiresAt: "2026-10-27T00:00:00Z" },
      ["temporaryExpired"],
    ],
    [
      { disabled: true, lockedUntil: "2026-10-29T00:00:00Z", passwordChangeRequired: true },
      ["disabled", "locked", "temporary"],
    ],
    [{ deletedAt: "2026-10-01T00:00:00Z", disabled: true }, ["deleted"]],
  ] as const)("%o → %o", (overrides, want) => {
    expect(accountFlags({ ...base, ...overrides }, NOW)).toEqual(want);
  });

  it("isLocked follows lockedUntil", () => {
    expect(isLocked({ lockedUntil: "2026-10-28T12:00:01Z" }, NOW)).toBe(true);
    expect(isLocked({ lockedUntil: "garbage" }, NOW)).toBe(false);
  });
});

describe("twoFactorState", () => {
  it.each([
    [false, null, false, "none"],
    [false, null, true, "enrolled"],
    [true, null, true, "enrolled"],
    [true, null, false, "overdue"],
    [true, "2026-11-01T00:00:00Z", false, "grace"],
    [true, "2026-10-28T12:00:00Z", false, "overdue"],
  ] as const)("required=%s deadline=%s enrolled=%s → %s", (req, deadline, enrolled, want) => {
    expect(
      twoFactorState(
        {
          twoFactorRequired: req,
          twoFactorEnrollmentDeadline: deadline,
          twoFactorEnrolled: enrolled,
        },
        NOW,
      ),
    ).toBe(want);
  });
});

describe("ownershipBlock (D-067)", () => {
  it.each([
    [{ role: "USER", disabled: false }, null],
    [{ role: "ADMIN", disabled: false }, "role"],
    [{ role: "MAINTAINER", disabled: false }, "role"],
    [{ role: "USER", disabled: true }, "disabled"],
    [{ role: "USER", disabled: false, deletedAt: "2026-01-01T00:00:00Z" }, "deleted"],
  ] as const)("%o → %s", (u, want) => {
    expect(ownershipBlock(u)).toBe(want);
  });
});
