import { ApolloClient, InMemoryCache } from "@apollo/client";
import { ApolloProvider } from "@apollo/client/react";
import { MockLink } from "@apollo/client/testing";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { SecurityRisksQuery } from "@/lib/operations";

import { RiskBadge, RiskDecision } from "./risk-badge";

const CONN = "0b6c3f7e-0000-4000-8000-00000000c0n1";

function risks(acceptance: Record<string, unknown> | null, canAccept = true) {
  return {
    securityRisks: [
      {
        __typename: "SecurityRisk",
        kind: "CONNECTION_TLS",
        conditionId: "ab".repeat(32),
        condition: "TLS disabled (db:5432)",
        connectionId: CONN,
        jobId: null,
        subjectName: "billing-db",
        canAccept,
        acceptance,
      },
    ],
  };
}

const ACCEPTANCE = {
  __typename: "RiskAcceptance",
  id: "acc-1",
  duration: "ONE_YEAR",
  expiresAt: "2027-11-07T10:00:00Z",
  createdAt: "2026-11-07T10:00:00Z",
  reason: "legacy",
  createdByMe: true,
  createdBy: { __typename: "RiskAcceptor", id: "u-1", displayName: "Ada" },
};

function withRisks(data: Record<string, unknown>, ui: React.ReactNode) {
  const link = new MockLink([
    { request: { query: SecurityRisksQuery }, result: { data }, maxUsageCount: 5 },
  ]);
  const client = new ApolloClient({ cache: new InMemoryCache(), link });
  return render(<ApolloProvider client={client}>{ui}</ApolloProvider>);
}

describe("RiskBadge", () => {
  it("shows an accepted risk muted with its end date (D-228)", async () => {
    const { container } = withRisks(
      risks(ACCEPTANCE),
      <RiskBadge flag="insecure-transport" kind="CONNECTION_TLS" subjectId={CONN} />,
    );
    expect(await screen.findByText(/Accepted until/)).toBeInTheDocument();
    expect(container.querySelector("[data-accepted]")).toHaveClass("hatch-accepted");
    expect(screen.queryByRole("button", { name: /Accept risk/ })).toBeNull();
  });

  it("offers to accept an open risk only when the caller may", async () => {
    withRisks(
      risks(null),
      <RiskBadge flag="insecure-transport" kind="CONNECTION_TLS" subjectId={CONN} />,
    );
    expect(await screen.findByRole("button", { name: /Accept risk/ })).toBeInTheDocument();
  });

  it("is the plain badge for someone who may not decide", async () => {
    const { container } = withRisks(
      risks(null, false),
      <RiskBadge flag="insecure-transport" kind="CONNECTION_TLS" subjectId={CONN} />,
    );
    expect(await screen.findByText("Insecure transport")).toBeInTheDocument();
    expect(container.querySelector(".hatch-insecure")).not.toBeNull();
    expect(screen.queryByRole("button", { name: /Accept risk/ })).toBeNull();
  });
});

describe("RiskDecision", () => {
  it("names the acceptance next to a page's own badge", async () => {
    withRisks(
      risks({ ...ACCEPTANCE, expiresAt: null }),
      <RiskDecision kind="CONNECTION_TLS" subjectId={CONN} />,
    );
    expect(await screen.findByText("Accepted, no end date")).toBeInTheDocument();
  });
});
