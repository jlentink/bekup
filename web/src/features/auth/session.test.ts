import { describe, expect, it } from "vitest";

import { normalizeTotp } from "./code-input-utils";
import { enrollmentState } from "./session-model";

const NOW = Date.parse("2026-10-28T12:00:00Z");

describe("enrollmentState", () => {
  it.each([
    [false, null, "none"],
    [true, null, "none"],
    [true, "2026-11-01T00:00:00Z", "grace"],
    [true, "2026-10-28T12:00:00Z", "required"],
    [true, "2026-10-01T00:00:00Z", "required"],
    [true, "not a date", "required"],
  ] as const)(
    "required=%s deadline=%s → %s",
    (twoFactorRequired, twoFactorEnrollmentDeadline, want) => {
      expect(enrollmentState({ twoFactorRequired, twoFactorEnrollmentDeadline }, NOW)).toBe(want);
    },
  );
});

describe("normalizeTotp", () => {
  it("keeps six digits", () => {
    expect(normalizeTotp("123 456")).toBe("123456");
    expect(normalizeTotp("12-34-56-78")).toBe("123456");
    expect(normalizeTotp("abc")).toBe("");
  });
});
