import { describe, expect, it } from "vitest";

import { accountParts, estimateStrength, MIN_ACCEPTED_SCORE } from "./password-strength";

describe("estimateStrength", () => {
  it("flags passwords below the server minimum", () => {
    const e = estimateStrength("Short1!");
    expect(e.tooShort).toBe(true);
    expect(e.score).toBeLessThan(MIN_ACCEPTED_SCORE);
  });

  it("caps repetitive and sequential passwords", () => {
    expect(estimateStrength("aaaaaaaaaaaaaaaa").score).toBeLessThan(MIN_ACCEPTED_SCORE);
    expect(estimateStrength("abcdefghijklmnop").score).toBeLessThan(MIN_ACCEPTED_SCORE);
    expect(estimateStrength("123456789012").score).toBeLessThan(MIN_ACCEPTED_SCORE);
    expect(estimateStrength("passwordpassword").score).toBeLessThan(MIN_ACCEPTED_SCORE);
    expect(estimateStrength("Welcome2026!").score).toBeLessThan(MIN_ACCEPTED_SCORE);
    expect(estimateStrength("xk9!xk9!xk9!xk9!").score).toBeLessThan(MIN_ACCEPTED_SCORE);
  });

  it("rates long passphrases strong", () => {
    expect(estimateStrength("violet-anchor-meadow-41").score).toBeGreaterThanOrEqual(
      MIN_ACCEPTED_SCORE,
    );
    expect(estimateStrength("correct horse battery staple").score).toBeGreaterThanOrEqual(
      MIN_ACCEPTED_SCORE,
    );
  });

  it("refuses the account's e-mail address and name like the server", () => {
    const e = estimateStrength("lovelace-rocks-2026!", ["ada@example.com", "Ada Lovelace"]);
    expect(e.containsAccount).toBe(true);
    expect(e.score).toBeLessThan(MIN_ACCEPTED_SCORE);
  });

  it("counts characters as code points", () => {
    expect(estimateStrength("🔒🔒🔒🔒🔒🔒🔒🔒🔒🔒🔒🔒").tooShort).toBe(false);
  });
});

describe("accountParts", () => {
  it("splits addresses and names like the server, ignoring short parts", () => {
    expect(accountParts(["ada@example.com", "Ada Lovelace"])).toEqual([
      "ada@example.com",
      "ada",
      "ada",
      "example",
      "com",
      "ada lovelace",
      "ada",
      "lovelace",
    ]);
    expect(accountParts(["Al"])).toEqual([]);
  });
});
