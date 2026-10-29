import { describe, expect, it } from "vitest";

import { loginPathFor, safeReturnPath, withReturnParam } from "./return-path";

const ORIGIN = "https://bekup.example.com";

describe("safeReturnPath", () => {
  it.each([
    ["/jobs", "/jobs"],
    ["/jobs/42?tab=runs#log", "/jobs/42?tab=runs#log"],
    ["/", "/"],
    ["/a/../b", "/b"],
  ])("accepts the relative path %s", (raw, want) => {
    expect(safeReturnPath(raw, ORIGIN)).toBe(want);
  });

  it.each([
    null,
    "",
    "jobs",
    "https://evil.example/",
    "//evil.example/x",
    "/\\evil.example",
    "/\\/evil.example",
    "\\\\evil.example",
    "javascript:alert(1)",
    "/jobs\nSet-Cookie: x",
    "/\tjobs",
    "/login",
    "/login?next=/x",
    "/login/verify",
    "/.//evil.com",
    "/a/..//evil.com",
    "/%2e//evil.com",
    "/%2E%2E//evil.com",
    "/a/%2e%2e//evil.com",
    "/./\\evil.com",
    "/%5cevil.com",
    `/${"a".repeat(3000)}`,
  ])("rejects %j", (raw) => {
    expect(safeReturnPath(raw, ORIGIN)).toBeNull();
  });
});

describe("loginPathFor / withReturnParam", () => {
  it("encodes the return path", () => {
    expect(loginPathFor("/jobs?x=1", ORIGIN)).toBe("/login?next=%2Fjobs%3Fx%3D1");
    expect(loginPathFor("/", ORIGIN)).toBe("/login");
    expect(loginPathFor("//evil", ORIGIN)).toBe("/login");
  });

  it("carries a safe return parameter over to another auth page", () => {
    expect(withReturnParam("/login/verify", "?next=%2Fjobs", ORIGIN)).toBe(
      "/login/verify?next=%2Fjobs",
    );
    expect(withReturnParam("/login/verify", "?next=https%3A%2F%2Fevil", ORIGIN)).toBe(
      "/login/verify",
    );
  });
});
