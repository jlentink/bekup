import { screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { baseHandlers, gqlError, type Handler, ok, renderApp } from "@/test/mock-api";

import { newlyRequired, policySchema, weakenings } from "./policy";

const base = { requireForAll: false, requiredRoles: ["ADMIN"] as const, gracePeriodDays: 7 };

describe("policy rules", () => {
  it("detects weakenings", () => {
    expect(weakenings(base, { ...base, requiredRoles: [] })).toEqual([
      { kind: "role", role: "ADMIN" },
    ]);
    expect(weakenings(base, { ...base, gracePeriodDays: 14 })).toEqual([
      { kind: "grace", from: 7, to: 14 },
    ]);
    expect(
      weakenings(base, { ...base, requiredRoles: ["ADMIN", "USER"], gracePeriodDays: 3 }),
    ).toEqual([]);
    expect(
      weakenings(
        { ...base, requireForAll: true },
        { ...base, requireForAll: false, requiredRoles: ["ADMIN", "MAINTAINER", "USER"] },
      ),
    ).toEqual([{ kind: "all" }]);
    expect(newlyRequired(base, { ...base, requireForAll: true })).toEqual(["MAINTAINER", "USER"]);
  });

  it.each([
    ["0", true],
    ["90", true],
    ["91", false],
    ["-1", false],
    ["1.5", false],
    ["", false],
  ])("grace %j valid=%s", (v, want) => {
    expect(
      policySchema.safeParse({ requireForAll: false, requiredRoles: [], gracePeriodDays: v })
        .success,
    ).toBe(want);
  });
});

function policy(over: Record<string, unknown> = {}) {
  return {
    __typename: "TwoFactorPolicy",
    requireForAll: false,
    requiredRoles: ["ADMIN"],
    gracePeriodDays: 7,
    updatedAt: null,
    passkeysAvailable: true,
    ...over,
  };
}

function handlers(update?: Handler): Record<string, Handler> {
  let stepped = false;
  return {
    ...baseHandlers(),
    AdminTwoFactorPolicy: () => ok({ twoFactorPolicy: policy() }),
    BeginStepUp: () =>
      ok({
        beginStepUp: { __typename: "StepUpChallenge", methods: ["TOTP"], passkeyOptions: null },
      }),
    CompleteStepUp: () => {
      stepped = true;
      return ok({
        completeStepUp: {
          __typename: "StepUpResult",
          stepUpAt: "2026-10-28T10:00:00Z",
          aal2At: "2026-10-28T10:00:00Z",
          aal: 2,
        },
      });
    },
    AdminUpdateTwoFactorPolicy: (vars) =>
      stepped
        ? (
            update ??
            ((v) => ok({ updateTwoFactorPolicy: policy(v["input"] as Record<string, unknown>) }))
          )(vars)
        : gqlError("STEP_UP_REQUIRED", "please confirm your identity again"),
  };
}

describe("2FA policy page", () => {
  // Radix Switch measures itself; jsdom has no ResizeObserver.
  beforeEach(() => {
    vi.stubGlobal(
      "ResizeObserver",
      class {
        observe = vi.fn();
        unobserve = vi.fn();
        disconnect = vi.fn();
      },
    );
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("validates the grace period before saving", async () => {
    const { user, callsOf } = renderApp("/admin/two-factor", handlers());
    const grace = await screen.findByLabelText("Grace period in days");
    await user.clear(grace);
    await user.type(grace, "120");
    await user.click(screen.getByRole("button", { name: "Save policy" }));
    expect(await screen.findByText(/whole number from 0 to 90/)).toBeInTheDocument();
    expect(callsOf("AdminUpdateTwoFactorPolicy")).toHaveLength(0);
  });

  it("warns when weakening, confirms, then saves with a step-up", async () => {
    const { user, callsOf } = renderApp("/admin/two-factor", handlers());
    await user.click(await screen.findByRole("checkbox", { name: /^Admin/ }));
    const alert = await screen.findByRole("alert", { name: "This weakens the policy" });
    expect(
      within(alert).getByText("Admin no longer requires a second factor."),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Save policy" }));
    const confirm = await screen.findByRole("dialog", { name: "This weakens the policy" });
    await user.click(within(confirm).getByRole("button", { name: "Weaken the policy" }));
    const stepUp = await screen.findByRole("dialog", { name: "Confirm your identity" });
    await user.type(within(stepUp).getByLabelText("6-digit code"), "654321");
    await user.click(within(stepUp).getByRole("button", { name: "Confirm" }));

    expect(await screen.findByText("Two-factor policy saved.")).toBeInTheDocument();
    expect(callsOf("AdminUpdateTwoFactorPolicy").at(-1)?.variables).toEqual({
      input: { requireForAll: false, requiredRoles: [], gracePeriodDays: 7 },
    });
  });

  it("saves a stricter policy without the weakening confirmation", async () => {
    const { user, callsOf } = renderApp("/admin/two-factor", handlers());
    await user.click(await screen.findByRole("switch", { name: /Require for every role/ }));
    expect(screen.getByText(/Maintainer, User: the grace period starts now/)).toBeInTheDocument();
    expect(screen.queryByRole("alert", { name: "This weakens the policy" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Save policy" }));
    const stepUp = await screen.findByRole("dialog", { name: "Confirm your identity" });
    await user.type(within(stepUp).getByLabelText("6-digit code"), "654321");
    await user.click(within(stepUp).getByRole("button", { name: "Confirm" }));
    expect(await screen.findByText("Two-factor policy saved.")).toBeInTheDocument();
    expect(callsOf("AdminUpdateTwoFactorPolicy").at(-1)?.variables).toEqual({
      input: { requireForAll: true, requiredRoles: ["ADMIN"], gracePeriodDays: 7 },
    });
  });
});
