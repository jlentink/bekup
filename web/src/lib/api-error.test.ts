import { CombinedGraphQLErrors, ServerError } from "@apollo/client/errors";
import { describe, expect, it, vi } from "vitest";

import { apiErrors, applyFieldErrors, hasApiCode, userMessage } from "./api-error";

function gql(...errors: { message: string; extensions?: Record<string, unknown> }[]) {
  return new CombinedGraphQLErrors({ errors });
}

describe("apiErrors", () => {
  it("normalizes GraphQL errors with code, field and request ID", () => {
    const err = gql(
      { message: "the code is not valid", extensions: { code: "VALIDATION", field: "code" } },
      { message: "internal error", extensions: { code: "INTERNAL", requestId: "req-1" } },
    );
    expect(apiErrors(err)).toEqual([
      { code: "VALIDATION", message: "the code is not valid", field: "code" },
      { code: "INTERNAL", message: "internal error", requestId: "req-1" },
    ]);
    expect(hasApiCode(err, "VALIDATION")).toBe(true);
    expect(hasApiCode(err, "FORBIDDEN")).toBe(false);
  });

  it("maps HTTP refusals and network failures", () => {
    const res = (status: number) => new Response("", { status });
    expect(apiErrors(new ServerError("x", { response: res(429), bodyText: "" }))[0]?.code).toBe(
      "RATE_LIMITED",
    );
    expect(apiErrors(new ServerError("x", { response: res(401), bodyText: "" }))[0]?.code).toBe(
      "UNAUTHENTICATED",
    );
    expect(apiErrors(new ServerError("x", { response: res(502), bodyText: "" }))[0]?.code).toBe(
      "NETWORK",
    );
    expect(apiErrors(new TypeError("fetch failed"))[0]?.code).toBe("NETWORK");
    expect(apiErrors(null)).toEqual([]);
  });
});

describe("userMessage", () => {
  it.each([
    [
      "VALIDATION",
      "the password is too short (at least 12 characters)",
      "The password is too short (at least 12 characters).",
    ],
    [
      "RATE_LIMITED",
      "too many failed attempts; try again in 30 seconds",
      "Too many failed attempts; try again in 30 seconds.",
    ],
    [
      "CONFLICT",
      "TOTP is already enabled; disable it first",
      "TOTP is already enabled; disable it first.",
    ],
    ["NOT_FOUND", "passkey not found", "That item no longer exists or is not visible to you."],
    ["FORBIDDEN", "not allowed", "Not allowed."],
    [
      "UNAUTHENTICATED",
      "authentication required",
      "Your session ended. Sign in again to continue.",
    ],
  ])("presents %s", (code, message, want) => {
    expect(userMessage(gql({ message, extensions: { code } }))).toBe(want);
  });

  it("adds the request ID of masked internal errors", () => {
    expect(
      userMessage(
        gql({ message: "internal error", extensions: { code: "INTERNAL", requestId: "abc" } }),
      ),
    ).toBe("The server hit an internal error. Reference: abc");
  });
});

describe("applyFieldErrors", () => {
  it("puts validation errors on known fields only", () => {
    const setError = vi.fn();
    const err = gql(
      {
        message: "the current password is wrong",
        extensions: { code: "VALIDATION", field: "currentPassword" },
      },
      { message: "x", extensions: { code: "VALIDATION", field: "unknownField" } },
    );
    expect(applyFieldErrors(err, setError, ["currentPassword", "newPassword"])).toBe(true);
    expect(setError).toHaveBeenCalledTimes(1);
    expect(setError).toHaveBeenCalledWith(
      "currentPassword",
      { type: "server", message: "The current password is wrong." },
      { shouldFocus: true },
    );
  });

  it("returns false when nothing could be placed", () => {
    expect(
      applyFieldErrors(gql({ message: "x", extensions: { code: "CONFLICT" } }), vi.fn(), ["a"]),
    ).toBe(false);
  });
});
