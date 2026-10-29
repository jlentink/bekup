import { CombinedGraphQLErrors } from "@apollo/client/errors";
import { describe, expect, it } from "vitest";

import { authEventFor } from "./apollo";

const err = (code: string) =>
  new CombinedGraphQLErrors({ errors: [{ message: "x", extensions: { code } }] });

describe("authEventFor", () => {
  it.each([
    ["UNAUTHENTICATED", "unauthenticated"],
    ["SECOND_FACTOR_REQUIRED", "second-factor-required"],
    ["PASSWORD_CHANGE_REQUIRED", "restricted"],
    ["REQUIRES_2FA_ENROLLMENT", "restricted"],
    ["STEP_UP_REQUIRED", undefined],
    ["FORBIDDEN", undefined],
    ["VALIDATION", undefined],
  ])("maps %s", (code, event) => {
    expect(authEventFor("MySessions", err(code))).toBe(event);
  });

  it("leaves self-handled operations alone", () => {
    expect(authEventFor("Login", err("UNAUTHENTICATED"))).toBeUndefined();
    expect(authEventFor("Me", err("UNAUTHENTICATED"))).toBeUndefined();
    expect(authEventFor("Logout", err("UNAUTHENTICATED"))).toBeUndefined();
  });
});
