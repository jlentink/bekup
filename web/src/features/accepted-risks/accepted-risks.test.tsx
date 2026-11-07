import { screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import {
  baseHandlers,
  gqlError,
  meFixture,
  ok,
  renderApp,
  type Handler,
  type Vars,
} from "@/test/mock-api";

import { reasonProblem, subjectPath } from "./model";

const CONN = "0b6c3f7e-0000-4000-8000-00000000c0n1";
const ACC = "0b6c3f7e-0000-4000-8000-00000000acc1";

function risk(overrides: Record<string, unknown> = {}) {
  return {
    __typename: "SecurityRisk",
    kind: "CONNECTION_TLS",
    conditionId: "ab".repeat(32),
    condition: "TLS without verification (db.internal:5432)",
    connectionId: CONN,
    jobId: null,
    subjectName: "billing-db",
    canAccept: true,
    acceptance: null,
    ...overrides,
  };
}

function acceptance(overrides: Record<string, unknown> = {}) {
  return {
    __typename: "RiskAcceptance",
    id: ACC,
    kind: "INSECURE_DB_TLS",
    connectionId: null,
    jobId: null,
    subjectName: null,
    subjectDeleted: false,
    condition: "App database TLS mode require",
    duration: "FOREVER",
    reason: "Managed database without a CA we can pin",
    createdBy: {
      __typename: "RiskAcceptor",
      id: "u-1",
      displayName: "Ada Admin",
      email: "ada@example.com",
      deleted: false,
    },
    createdByMe: false,
    createdAt: "2026-11-01T10:00:00Z",
    expiresAt: null,
    endedAt: null,
    endReason: null,
    status: "ACTIVE",
    ...overrides,
  };
}

const stepUpHandlers = (onDone: () => void): Record<string, Handler> => ({
  BeginStepUp: () =>
    ok({ beginStepUp: { __typename: "StepUpChallenge", methods: ["TOTP"], passkeyOptions: null } }),
  CompleteStepUp: () => {
    onDone();
    return ok({
      completeStepUp: {
        __typename: "StepUpResult",
        stepUpAt: "2026-11-07T10:00:00Z",
        aal2At: null,
        aal: 1,
      },
    });
  },
});

function page(role: "ADMIN" | "MAINTAINER" | "USER", extra: Record<string, Handler> = {}) {
  return renderApp("/risks", {
    ...baseHandlers(meFixture({}, role)),
    SecurityRisks: () => ok({ securityRisks: [risk()] }),
    RiskAcceptances: () => ok({ riskAcceptances: [acceptance()] }),
    ...extra,
  });
}

describe("Accepted risks page", () => {
  it("lists the ledger and the open risks", async () => {
    page("ADMIN");
    const entry = await screen.findByTestId("risk-acceptance");
    expect(within(entry).getByText("App database TLS")).toBeInTheDocument();
    expect(within(entry).getByText(/Managed database without a CA/)).toBeInTheDocument();
    expect(within(entry).getByText("No end date")).toBeInTheDocument();
    expect(within(entry).getByText(/Ada Admin/)).toBeInTheDocument();
    expect(
      await screen.findByText("TLS without verification (db.internal:5432)"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /Accept risk: Connection TLS billing-db/ }),
    ).toBeInTheDocument();
  });

  it("accepts a risk behind a step-up with a duration and a reason", async () => {
    let stepped = false;
    let input: Vars | undefined;
    const { user, callsOf } = page("ADMIN", {
      ...stepUpHandlers(() => {
        stepped = true;
      }),
      AcceptRisk: (v) => {
        if (!stepped) return gqlError("STEP_UP_REQUIRED", "please confirm your identity again");
        input = v["input"] as Vars;
        return ok({
          acceptRisk: {
            __typename: "RiskAcceptance",
            id: "acc-2",
            kind: "CONNECTION_TLS",
            duration: "ONE_MONTH",
            expiresAt: "2026-12-07T10:00:00Z",
            status: "ACTIVE",
          },
        });
      },
    });
    await user.click(await screen.findByRole("button", { name: /Accept risk: Connection TLS/ }));
    const dialog = await screen.findByRole("dialog", { name: "Accept this risk" });
    // A reason is required.
    await user.click(within(dialog).getByRole("button", { name: "Accept risk" }));
    expect(within(dialog).getByText("Say why the risk is accepted.")).toBeInTheDocument();
    expect(callsOf("AcceptRisk")).toHaveLength(0);
    // Forever warns.
    await user.click(within(dialog).getByRole("radio", { name: "Forever" }));
    expect(within(dialog).getByText(/never flagged again/)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("radio", { name: "1 month" }));
    await user.type(within(dialog).getByLabelText("Reason"), "Legacy server, replaced in Q1");
    await user.click(within(dialog).getByRole("button", { name: "Accept risk" }));
    const stepUp = await screen.findByRole("dialog", { name: "Confirm your identity" });
    await user.type(within(stepUp).getByLabelText("6-digit code"), "123456");
    await user.click(within(stepUp).getByRole("button", { name: "Confirm" }));
    await waitFor(() => {
      expect(input).toBeDefined();
    });
    expect(input).toEqual({
      kind: "CONNECTION_TLS",
      conditionId: "ab".repeat(32),
      connectionId: CONN,
      jobId: null,
      duration: "ONE_MONTH",
      reason: "Legacy server, replaced in Q1",
    });
  });

  it("asks to review a risk whose condition changed", async () => {
    const { user } = page("ADMIN", {
      AcceptRisk: () => gqlError("CONFLICT", "the condition changed; review the risk again"),
    });
    await user.click(await screen.findByRole("button", { name: /Accept risk: Connection TLS/ }));
    const dialog = await screen.findByRole("dialog", { name: "Accept this risk" });
    await user.type(within(dialog).getByLabelText("Reason"), "x");
    await user.click(within(dialog).getByRole("button", { name: "Accept risk" }));
    expect(
      await within(dialog).findByText(/The condition changed since you opened this/),
    ).toBeInTheDocument();
  });

  it("lets maintainers read without deciding", async () => {
    page("MAINTAINER", {
      SecurityRisks: () =>
        ok({
          securityRisks: [
            risk({ canAccept: false }),
            risk({
              kind: "INSECURE_DB_TLS",
              connectionId: null,
              subjectName: null,
              canAccept: false,
              acceptance: acceptance(),
            }),
          ],
        }),
    });
    const entry = await screen.findByTestId("risk-acceptance");
    expect(within(entry).queryByRole("button", { name: /Revoke/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /Accept risk/ })).toBeNull();
  });

  it("revokes an acceptance after a confirmation", async () => {
    let revoked: Vars | undefined;
    const { user } = page("ADMIN", {
      SecurityRisks: () =>
        ok({
          securityRisks: [
            risk({
              kind: "INSECURE_DB_TLS",
              connectionId: null,
              subjectName: null,
              acceptance: acceptance(),
            }),
          ],
        }),
      RevokeRiskAcceptance: (v) => {
        revoked = v;
        return ok({
          revokeRiskAcceptance: {
            __typename: "RiskAcceptance",
            id: ACC,
            status: "REVOKED",
            endedAt: "2026-11-07T10:00:00Z",
            endReason: "REVOKED",
          },
        });
      },
    });
    const entry = await screen.findByTestId("risk-acceptance");
    await user.click(await within(entry).findByRole("button", { name: /Revoke/ }));
    const confirm = await screen.findByRole("dialog", { name: "Revoke this acceptance?" });
    await user.click(within(confirm).getByRole("button", { name: "Revoke" }));
    await waitFor(() => {
      expect(revoked).toEqual({ id: ACC });
    });
  });
});

describe("model", () => {
  it("validates reasons like the server", () => {
    expect(reasonProblem("  ")).toBe("missing");
    expect(reasonProblem("é".repeat(500))).toBeNull();
    expect(reasonProblem("é".repeat(501))).toBe("long");
  });

  it("links a risk's subject", () => {
    expect(subjectPath({ kind: "CONNECTION_TLS", connectionId: "c" })).toBe("/connections/c");
    expect(subjectPath({ kind: "ENCRYPTION_OPT_OUT", jobId: "j" })).toBe("/jobs/j");
    expect(subjectPath({ kind: "INSECURE_DB_TLS" })).toBe("/admin/settings/configuration");
  });
});
