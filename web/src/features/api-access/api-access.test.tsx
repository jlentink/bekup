import { ApolloClient, InMemoryCache } from "@apollo/client";
import { ApolloProvider } from "@apollo/client/react";
import { MockLink } from "@apollo/client/testing";
import { render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { emptyRuns, JOB_ID, jobDetail, noTestRestores } from "@/features/jobs/test-fixtures";
import {
  baseHandlers,
  gqlError,
  meFixture,
  ok,
  renderApp,
  serverInfoFixture,
  type Handler,
  type Vars,
} from "@/test/mock-api";

import { ServerInfoQuery } from "@/lib/server-info";

import { ShowOnceDialog } from "./show-once-dialog";
import { apiBaseUrl } from "./snippets";

const DAY = 24 * 60 * 60 * 1000;
const PLAINTEXT =
  "bkp_aaisem2ekvthpcezvk54zxpo74_aaaqeayeaudaocajbifqydiob4ibceqtcqkrmfyydenbwha5dypq";
const OTHER_JOB = "0b6c3f7e-0000-4000-8000-0000000000j2";

function tokenRow(overrides: Record<string, unknown> = {}) {
  return {
    __typename: "ApiToken",
    id: "t-1",
    name: "deploy pipeline",
    prefix: "bkp_aaisem2e",
    jobs: [{ __typename: "ApiTokenJob", id: JOB_ID, name: "Billing DB", deleted: false }],
    hiddenJobCount: 0,
    createdBy: {
      __typename: "ApiTokenCreator",
      id: "u-1",
      email: "olive@example.com",
      displayName: "Olive Owner",
      deleted: false,
    },
    createdByMe: false,
    createdAt: "2026-10-01T10:00:00Z",
    expiresAt: new Date(Date.now() + 60 * DAY).toISOString(),
    revokedAt: null,
    allowedCidrs: ["192.0.2.0/24"],
    allowAnyAddress: false,
    lastUsedAt: null,
    lastSeenAt: new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString(),
    lastSeenIp: "192.0.2.10",
    lastSeenRefused: false,
    ...overrides,
  };
}

const allJobs = [
  { id: JOB_ID, name: "Billing DB" },
  { id: OTHER_JOB, name: "Shop DB" },
];

/** ApiAccessJobs searched by name like the server, one page. */
const jobsForTokens: Handler = (v) => {
  const search = typeof v["search"] === "string" ? v["search"].toLowerCase() : "";
  const nodes = allJobs.filter((j) => j.name.toLowerCase().includes(search));
  return ok({
    jobs: {
      __typename: "JobConnection",
      edges: nodes.map((n) => ({ __typename: "JobEdge", node: { __typename: "Job", ...n } })),
      pageInfo: { __typename: "PageInfo", hasNextPage: false },
    },
  });
};

function detailPage(role: "ADMIN" | "MAINTAINER" | "USER", extra: Record<string, Handler> = {}) {
  return renderApp(`/jobs/${JOB_ID}`, {
    ...baseHandlers(meFixture({}, role)),
    JobDetail: () => ok({ job: jobDetail(role === "USER" ? { owners: null } : {}) }),
    RunsList: () => ok(emptyRuns),
    LatestTestRestore: () => ok(noTestRestores),
    ApiTokens: () => ok({ apiTokens: [tokenRow()] }),
    ApiAccessJobs: jobsForTokens,
    ...extra,
  });
}

const stepUpHandlers = (onDone: () => void): Record<string, Handler> => ({
  BeginStepUp: () =>
    ok({ beginStepUp: { __typename: "StepUpChallenge", methods: ["TOTP"], passkeyOptions: null } }),
  CompleteStepUp: () => {
    onDone();
    return ok({
      completeStepUp: {
        __typename: "StepUpResult",
        stepUpAt: "2026-11-03T10:00:00Z",
        aal2At: null,
        aal: 1,
      },
    });
  },
});

describe("API access panel", () => {
  it("shows a backup owner the tokens and the curl examples for this job, without any token", async () => {
    const { user, callsOf } = detailPage("USER");
    const panel = await screen.findByTestId("api-access-panel");
    expect(await within(panel).findByText("deploy pipeline")).toBeInTheDocument();
    expect(within(panel).getByText("bkp_aaisem2e…")).toBeInTheDocument();
    expect(within(panel).getByText(/from 192\.0\.2\.10/)).toBeInTheDocument();
    expect(within(panel).getByText("192.0.2.0/24")).toBeInTheDocument();
    expect(callsOf("ApiTokens")[0]?.variables).toEqual({ jobId: JOB_ID });

    const examples = within(panel).getByRole("region", { name: "curl examples" });
    const base = apiBaseUrl(window.location.origin);
    const blocks = within(examples)
      .getAllByRole("group")
      .map((g) => g.querySelector("pre")?.textContent ?? "");
    expect(blocks).toHaveLength(3);
    expect(blocks[0]).toContain(`${base}/jobs/${JOB_ID}/runs`);
    expect(blocks[1]).toContain(`${base}/requests/$REQUEST_ID`);
    expect(blocks[2]).toContain(`BEKUP_JOB_ID="\${BEKUP_JOB_ID:-${JOB_ID}}"`);
    for (const b of blocks) {
      expect(b).toContain("BEKUP_TOKEN");
      expect(b).not.toContain("bkp_");
    }
    expect(
      within(examples).getByText(/success is true only when every database/),
    ).toBeInTheDocument();

    // Every snippet has its own copy button.
    await user.click(within(examples).getByRole("button", { name: "Copy: 1. Start a backup" }));
    expect(await navigator.clipboard.readText()).toBe(blocks[0]);
    await user.click(within(examples).getByRole("button", { name: "Copy: 3. Complete CI script" }));
    expect(await navigator.clipboard.readText()).toBe(blocks[2]);
    await user.click(within(examples).getByRole("button", { name: "Copy: 2. Check the status" }));
    expect(await navigator.clipboard.readText()).toBe(blocks[1]);
  });

  it("shows a note instead of the curl examples while the external API is not enabled", async () => {
    const { user } = detailPage("USER", {
      ServerInfo: () => ok(serverInfoFixture(false, false)),
    });
    const panel = await screen.findByTestId("api-access-panel");
    expect(await within(panel).findByText("deploy pipeline")).toBeInTheDocument();
    const note = await within(panel).findByTestId("external-api-disabled");
    expect(
      within(note).getByText("The external API is not enabled on this server"),
    ).toBeInTheDocument();
    expect(note.textContent).toContain("setup upgrade --external-api");
    expect(within(note).getByRole("link", { name: /External API guide/ })).toHaveAttribute(
      "href",
      expect.stringContaining("guides/external-api.md"),
    );
    expect(within(panel).queryByRole("region", { name: "curl examples" })).toBeNull();
    // Tokens can still be created in advance.
    await user.click(within(panel).getByRole("button", { name: "Create API token" }));
    expect(await screen.findByRole("dialog", { name: "Create an API token" })).toBeInTheDocument();
  });

  it("is not shown to maintainers", async () => {
    const { callsOf } = detailPage("MAINTAINER");
    expect(await screen.findByRole("button", { name: /Run now/ })).toBeInTheDocument();
    expect(screen.queryByTestId("api-access-panel")).toBeNull();
    expect(screen.queryByRole("button", { name: "Create API token" })).toBeNull();
    expect(callsOf("ApiTokens")).toHaveLength(0);
  });

  it("searches the jobs on the server and keeps the chosen ones listed", async () => {
    // A large instance: the first page lacks this job and has more.
    const many = Array.from({ length: 50 }, (_, i) => ({
      id: `0b6c3f7e-0000-4000-8000-${String(i).padStart(12, "0")}`,
      name: `Job ${String(i).padStart(2, "0")}`,
    }));
    const pool = [...many, { id: OTHER_JOB, name: "Shop DB" }];
    const { user, callsOf } = detailPage("ADMIN", {
      ApiAccessJobs: (v) => {
        const search = typeof v["search"] === "string" ? v["search"].toLowerCase() : "";
        const hits = pool.filter((j) => j.name.toLowerCase().includes(search));
        return ok({
          jobs: {
            __typename: "JobConnection",
            edges: hits
              .slice(0, 50)
              .map((n) => ({ __typename: "JobEdge", node: { __typename: "Job", ...n } })),
            pageInfo: { __typename: "PageInfo", hasNextPage: hits.length > 50 },
          },
        });
      },
    });
    await user.click(await screen.findByRole("button", { name: "Create API token" }));
    const dialog = await screen.findByRole("dialog", { name: "Create an API token" });
    // The job of the page is chosen although the first page lacks it.
    expect(await within(dialog).findByRole("checkbox", { name: "Billing DB" })).toBeChecked();
    expect(within(dialog).getByText(/More jobs match/)).toBeInTheDocument();
    expect(callsOf("ApiAccessJobs")[0]?.variables).toEqual({ search: null, first: 50 });

    await user.type(within(dialog).getByRole("searchbox", { name: "Search jobs" }), "shop");
    await user.click(await within(dialog).findByRole("checkbox", { name: "Shop DB" }));
    expect(callsOf("ApiAccessJobs").at(-1)?.variables).toEqual({ search: "shop", first: 50 });
    expect(within(dialog).queryByRole("checkbox", { name: "Job 00" })).toBeNull();
    expect(within(dialog).queryByText(/More jobs match/)).toBeNull();

    // Another search keeps both chosen jobs on top.
    await user.clear(within(dialog).getByRole("searchbox", { name: "Search jobs" }));
    await user.type(within(dialog).getByRole("searchbox", { name: "Search jobs" }), "nothing");
    await waitFor(() => {
      expect(callsOf("ApiAccessJobs").at(-1)?.variables).toEqual({ search: "nothing", first: 50 });
    });
    expect(within(dialog).getByRole("checkbox", { name: "Billing DB" })).toBeChecked();
    expect(within(dialog).getByRole("checkbox", { name: "Shop DB" })).toBeChecked();
  });

  it("creates a token behind a step-up and shows it once", async () => {
    let stepped = false;
    let input: Vars | undefined;
    const { user, callsOf } = detailPage("ADMIN", {
      ...stepUpHandlers(() => {
        stepped = true;
      }),
      CreateApiToken: (v) => {
        if (!stepped) return gqlError("STEP_UP_REQUIRED", "please confirm your identity again");
        input = v["input"] as Vars;
        return ok({
          createApiToken: {
            __typename: "CreatedApiToken",
            plaintext: PLAINTEXT,
            token: {
              __typename: "ApiToken",
              id: "t-2",
              name: "release",
              prefix: "bkp_aaisem2e",
              jobs: [{ __typename: "ApiTokenJob", id: JOB_ID, name: "Billing DB", deleted: false }],
              expiresAt: new Date(Date.now() + 30 * DAY).toISOString(),
            },
          },
        });
      },
    });
    await user.click(await screen.findByRole("button", { name: "Create API token" }));
    const dialog = await screen.findByRole("dialog", { name: "Create an API token" });
    await user.type(within(dialog).getByLabelText("Name"), "release");
    // This job is preselected; another one can be added.
    expect(await within(dialog).findByRole("checkbox", { name: "Billing DB" })).toBeChecked();
    await user.click(within(dialog).getByRole("checkbox", { name: "Shop DB" }));
    await user.click(within(dialog).getByRole("radio", { name: "30 days" }));
    await user.type(
      within(dialog).getByLabelText("Allowed networks"),
      "192.0.2.0/24\n2001:db8::/48",
    );
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));

    const stepUp = await screen.findByRole("dialog", { name: "Confirm your identity" });
    await user.type(within(stepUp).getByLabelText("6-digit code"), "123456");
    await user.click(within(stepUp).getByRole("button", { name: "Confirm" }));

    const shown = await screen.findByRole("dialog", { name: "Save the API token" });
    expect(within(shown).getByLabelText("API token")).toHaveTextContent(PLAINTEXT);
    expect(within(shown).getByText(/CI secret named BEKUP_TOKEN/)).toBeInTheDocument();
    expect(within(shown).getByText(/won't see this token again/)).toBeInTheDocument();
    // The examples in the dialog use the variable, never the token.
    const examples = within(shown).getByRole("region", { name: "curl examples" });
    expect(examples.textContent).toContain("$BEKUP_TOKEN");
    expect(examples.textContent).not.toContain(PLAINTEXT);
    expect(examples.textContent).not.toContain("bkp_");

    expect(callsOf("CreateApiToken")).toHaveLength(2);
    expect(input).toMatchObject({
      name: "release",
      allowedCidrs: ["192.0.2.0/24", "2001:db8::/48"],
      allowAnyAddress: false,
    });
    expect(new Set(input?.["jobIds"] as string[])).toEqual(new Set([JOB_ID, OTHER_JOB]));
    const days = (Date.parse(input?.["expiresAt"] as string) - Date.now()) / DAY;
    expect(days).toBeGreaterThan(29.9);
    expect(days).toBeLessThan(30.1);

    await user.click(within(shown).getByRole("button", { name: "Copy token" }));
    expect(await navigator.clipboard.readText()).toBe(PLAINTEXT);
    // Done needs the acknowledgement; Escape does not close it before.
    const done = within(shown).getByRole("button", { name: "Done" });
    expect(done).toBeDisabled();
    await user.keyboard("{Escape}");
    expect(screen.getByRole("dialog", { name: "Save the API token" })).toBeInTheDocument();
    await user.click(within(shown).getByRole("checkbox", { name: /I stored the token/ }));
    await user.click(done);
    await waitFor(() => {
      expect(screen.queryByRole("dialog", { name: "Save the API token" })).toBeNull();
    });
    expect(document.body.textContent).not.toContain(PLAINTEXT);
  });

  it("validates addresses and needs the explicit any-address choice", async () => {
    let input: Vars | undefined;
    const { user, callsOf } = detailPage("USER", {
      CreateApiToken: (v) => {
        input = v["input"] as Vars;
        return gqlError("VALIDATION", "entry 1 is not allowed", "allowedCidrs");
      },
    });
    await user.click(await screen.findByRole("button", { name: "Create API token" }));
    const dialog = await screen.findByRole("dialog", { name: "Create an API token" });
    await user.type(within(dialog).getByLabelText("Name"), "ci");
    const submit = within(dialog).getByRole("button", { name: "Create token" });
    await user.click(submit);
    expect(await within(dialog).findByText(/Add at least one network/)).toBeInTheDocument();
    const list = within(dialog).getByLabelText("Allowed networks");
    await user.type(list, "192.0.2.1/24");
    await user.click(submit);
    expect(await within(dialog).findByText(/did you mean 192\.0\.2\.0\/24\?/)).toBeInTheDocument();
    expect(list).toHaveAttribute("aria-invalid", "true");
    expect(callsOf("CreateApiToken")).toHaveLength(0);

    // Any address: the list is disabled and the warning is shown.
    await user.click(within(dialog).getByRole("checkbox", { name: "Allow from any address" }));
    expect(list).toBeDisabled();
    expect(within(dialog).getByText(/anyone who obtains the token/)).toBeInTheDocument();
    await user.click(submit);
    // The server's field error lands on the address editor.
    expect(await within(dialog).findByText("Entry 1 is not allowed.")).toBeInTheDocument();
    expect(input).toMatchObject({ allowedCidrs: [], allowAnyAddress: true, jobIds: [JOB_ID] });
  });

  it("marks tokens allowed from any address and flags expiry and refused attempts", async () => {
    detailPage("USER", {
      ApiTokens: () =>
        ok({
          apiTokens: [
            tokenRow({
              allowAnyAddress: true,
              allowedCidrs: [],
              expiresAt: new Date(Date.now() + 3 * DAY).toISOString(),
              lastSeenRefused: true,
              lastSeenIp: "198.51.100.9",
              jobs: [
                { __typename: "ApiTokenJob", id: JOB_ID, name: "Billing DB", deleted: false },
                { __typename: "ApiTokenJob", id: OTHER_JOB, name: "Shop DB", deleted: false },
              ],
              hiddenJobCount: 1,
            }),
          ],
        }),
    });
    const item = await screen.findByTestId("api-token");
    expect(within(item).getByText("Any address")).toBeInTheDocument();
    expect(within(item).getByText("Expires soon")).toBeInTheDocument();
    expect(within(item).getByText(/Refused: address outside the allowlist/)).toBeInTheDocument();
    expect(within(item).getByText(/from 198\.51\.100\.9/)).toBeInTheDocument();
    expect(within(item).getByText("+ 2 other jobs")).toBeInTheDocument();
  });

  it("revokes a token after a confirmation", async () => {
    let revoked: Vars | undefined;
    const { user } = detailPage("USER", {
      RevokeApiToken: (v) => {
        revoked = v;
        return ok({
          revokeApiToken: { __typename: "ApiToken", id: "t-1", revokedAt: "2026-11-03T12:00:00Z" },
        });
      },
    });
    await user.click(await screen.findByRole("button", { name: "Revoke: deploy pipeline" }));
    const confirm = await screen.findByRole("dialog", { name: "Revoke deploy pipeline?" });
    expect(within(confirm).getByText(/next CI call gets 401/)).toBeInTheDocument();
    await user.click(within(confirm).getByRole("button", { name: "Revoke token" }));
    await waitFor(() => {
      expect(revoked).toEqual({ id: "t-1" });
    });
    expect(await screen.findByText("API token revoked.")).toBeInTheDocument();
  });

  it("edits the addresses of a token", async () => {
    let vars: Vars | undefined;
    const { user } = detailPage("ADMIN", {
      UpdateApiTokenAddresses: (v) => {
        vars = v;
        return ok({
          updateApiTokenAddresses: {
            __typename: "ApiToken",
            id: "t-1",
            allowedCidrs: ["203.0.113.0/24"],
            allowAnyAddress: false,
          },
        });
      },
    });
    await user.click(
      await screen.findByRole("button", { name: "Edit addresses: deploy pipeline" }),
    );
    const dialog = await screen.findByRole("dialog", { name: "Addresses of deploy pipeline" });
    const list = within(dialog).getByLabelText("Allowed networks");
    expect(list).toHaveValue("192.0.2.0/24");
    await user.clear(list);
    await user.type(list, "203.0.113.0/24");
    await user.click(within(dialog).getByRole("button", { name: "Save addresses" }));
    await waitFor(() => {
      expect(vars).toEqual({ id: "t-1", allowedCidrs: ["203.0.113.0/24"], allowAnyAddress: false });
    });
  });
});

describe("Admin → API access", () => {
  it("lists every token with its jobs and creator for admins", async () => {
    const { callsOf } = renderApp("/admin/api-access", {
      ...baseHandlers(),
      ApiTokens: () =>
        ok({
          apiTokens: [
            tokenRow(),
            tokenRow({
              id: "t-9",
              name: "old",
              revokedAt: "2026-10-20T10:00:00Z",
              jobs: [{ __typename: "ApiTokenJob", id: OTHER_JOB, name: "Shop DB", deleted: true }],
            }),
          ],
        }),
    });
    expect(await screen.findByRole("heading", { name: "API access" })).toBeInTheDocument();
    expect(await screen.findByText("deploy pipeline")).toBeInTheDocument();
    expect(screen.getAllByText(/by Olive Owner/)).toHaveLength(2);
    expect(screen.getByText("Billing DB")).toBeInTheDocument();
    expect(screen.getByText("Revoked or expired (1)")).toBeInTheDocument();
    expect(callsOf("ApiTokens")[0]?.variables).toEqual({});
    expect(screen.getByRole("link", { name: "API access" })).toHaveAttribute(
      "href",
      "/admin/api-access",
    );
    expect(screen.queryByTestId("external-api-disabled")).toBeNull();
  });

  it("notes that the external API is not enabled on this server", async () => {
    renderApp("/admin/api-access", {
      ...baseHandlers(),
      ServerInfo: () => ok(serverInfoFixture(false, false)),
      ApiTokens: () => ok({ apiTokens: [tokenRow()] }),
    });
    expect(await screen.findByText("deploy pipeline")).toBeInTheDocument();
    expect(
      await screen.findByText("The external API is not enabled on this server"),
    ).toBeInTheDocument();
  });

  it("is not reachable for backup owners", async () => {
    const { callsOf } = renderApp("/admin/api-access", {
      ...baseHandlers(meFixture({}, "USER")),
      ApiTokens: () => ok({ apiTokens: [] }),
    });
    await waitFor(() => {
      expect(screen.queryByRole("heading", { name: "API access" })).toBeNull();
    });
    expect(screen.queryByRole("link", { name: "API access" })).toBeNull();
    expect(callsOf("ApiTokens")).toHaveLength(0);
  });
});

describe("Show-once dialog", () => {
  function renderDialog(externalApiEnabled: boolean) {
    const link = new MockLink([
      {
        request: { query: ServerInfoQuery },
        result: { data: serverInfoFixture(false, externalApiEnabled) },
      },
    ]);
    const client = new ApolloClient({ cache: new InMemoryCache(), link });
    render(
      <ApolloProvider client={client}>
        <ShowOnceDialog
          issued={{ plaintext: PLAINTEXT, name: "release", jobId: JOB_ID }}
          onDone={() => undefined}
        />
      </ApolloProvider>,
    );
  }

  it("shows the curl examples when the external API is enabled", async () => {
    renderDialog(true);
    const shown = await screen.findByRole("dialog", { name: "Save the API token" });
    const examples = await within(shown).findByRole("region", { name: "curl examples" });
    expect(examples.textContent).not.toContain(PLAINTEXT);
    expect(within(shown).queryByTestId("external-api-disabled")).toBeNull();
  });

  it("shows the note instead of the examples when it is not enabled", async () => {
    renderDialog(false);
    const shown = await screen.findByRole("dialog", { name: "Save the API token" });
    expect(await within(shown).findByTestId("external-api-disabled")).toBeInTheDocument();
    expect(within(shown).queryByRole("region", { name: "curl examples" })).toBeNull();
    expect(within(shown).getByLabelText("API token")).toHaveTextContent(PLAINTEXT);
  });
});
