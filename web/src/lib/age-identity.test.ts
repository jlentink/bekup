import { describe, expect, it } from "vitest";

import { extractIdentity } from "./age-identity";

const KEY = `AGE-SECRET-KEY-1${"Q".repeat(58)}`;

describe("extractIdentity", () => {
  it.each([
    [KEY, KEY],
    [`  ${KEY}  `, KEY],
    [`# created: now\r\n# public key: age1x\r\n${KEY}\r\n`, KEY],
  ])("finds the identity in %j", (text, want) => {
    expect(extractIdentity(text)).toBe(want);
  });

  it.each([
    "",
    "nope",
    "age1qyqszqgpqyqszqgp",
    `# ${KEY}`,
    `${KEY}\n${KEY}`,
    `AGE-SECRET-KEY-1${"Q".repeat(57)}`,
    `AGE-SECRET-KEY-1${"q".repeat(58)}`,
    `${KEY}X`,
  ])("refuses %j", (text) => {
    expect(extractIdentity(text)).toBeNull();
  });
});
