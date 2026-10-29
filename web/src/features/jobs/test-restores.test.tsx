import { screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { baseHandlers, gqlError, meFixture, ok, renderApp, type Vars } from "@/test/mock-api";

import { diffSummary, type TestRestoreDb } from "./test-restore-model";
import {
  CONN_ID,
  emptyRuns,
  JOB_ID,
  jobDetail,
  RUN_ID,
  stepUpHandlers,
  testRestoreDetail,
  testRestoreNode,
  TR_ID,
} from "./test-fixtures";

const SCRATCH_ID = "0b6c3f7e-0000-4000-8000-0000000000c9";

const list = (nodes: unknown[], hasNextPage = false) =>
  ok({
    testRestores: {
      __typename: "TestRestoreConnection",
      edges: nodes.map((node, i) => ({
        __typename: "TestRestoreEdge",
        cursor: String(i),
        node,
      })),
      pageInfo: { __typename: "PageInfo", hasNextPage, endCursor: hasNextPage ? "c1" : null },
    },
  });

function scratchConnection(
  privileges: unknown,
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  return {
    __typename: "Connection",
    id: SCRATCH_ID,
    name: "scratch-pg",
    engine: "POSTGRES",
    temporary: false,
    ready: true,
    lastTest: privileges
      ? {
          __typename: "ConnectionLastTest",
          at: "2026-10-27T09:00:00Z",
          ok: true,
          stale: false,
          privileges,
        }
      : null,
    ...overrides,
  };
}

const privileged = {
  __typename: "TargetPrivileges",
  status: "PRIVILEGED",
  reasons: ["superuser"],
  acknowledgementRequired: true,
};

// Both queries read Query.connections (one cache field): answer alike.
const editorHandlers = (connections: Record<string, unknown>[]) => ({
  JobDetail: () => ok({ job: jobDetail() }),
  JobEditorOptions: () =>
    ok({ connections: connections.map((c) => ({ versionKey: "17", ...c })), destinations: [] }),
  PreviewSchedule: () => gqlError("VALIDATION", "x"),
  UserPickerCandidates: () =>
    ok({ userDirectory: { __typename: "UserDirectoryConnection", edges: [] } }),
  TestRestoreTargets: () => ok({ connections }),
});

describe("diffSummary", () => {
  it("counts findings, orders FAIL first and reports unlisted ones", () => {
    const dbs = testRestoreDetail().databases as unknown as TestRestoreDb[];
    const s = diffSummary([...dbs, { ...dbs[1]!, id: "d3", findingsTotal: 4, findings: [] }]);
    expect(s.fail).toBe(2);
    expect(s.warn).toBe(1);
    expect(s.unlisted).toBe(4);
    expect(s.codes.map((c) => c.severity)).toEqual(["FAIL", "FAIL", "WARN"]);
  });
});

describe("job page test restores", () => {
  it("shows the latest verdict with its diff summary and links", async () => {
    renderApp(`/jobs/${JOB_ID}`, {
      ...baseHandlers(),
      JobDetail: () => ok({ job: jobDetail() }),
      RunsList: () => ok(emptyRuns),
      LatestTestRestore: () => list([testRestoreNode()]),
      TestRestoreDetail: () => ok({ testRestore: testRestoreDetail() }),
    });
    const section = await screen.findByRole("region", { name: "Test restores" });
    expect(await within(section).findByText("failed")).toBeInTheDocument();
    const diff = await within(section).findByRole("list", { name: "Differences" });
    expect(within(diff).getByText("Row count differs")).toBeInTheDocument();
    expect(within(diff).getByText("Table missing in the restore")).toBeInTheDocument();
    expect(within(section).getByRole("link", { name: "Show the differences" })).toHaveAttribute(
      "href",
      `/jobs/${JOB_ID}/test-restores/${TR_ID}`,
    );
    expect(within(section).getByRole("link", { name: "All test restores" })).toHaveAttribute(
      "href",
      `/jobs/${JOB_ID}/test-restores`,
    );
  });
});

describe("test restore history and detail", () => {
  it("lists a job's test restores with links to the details", async () => {
    const { callsOf } = renderApp(`/jobs/${JOB_ID}/test-restores`, {
      ...baseHandlers(meFixture({}, "USER")),
      JobDetail: () => ok({ job: jobDetail() }),
      JobTestRestores: () =>
        list([
          testRestoreNode(),
          testRestoreNode({
            id: "tr-2",
            status: "PASSED",
            trigger: "MANUAL",
            failed: 0,
            passed: 2,
            requestedBy: {
              __typename: "RunActor",
              userId: "u-9",
              displayName: "Max Maintainer",
              email: "max@example.com",
            },
          }),
        ]),
    });
    const table = await screen.findByRole("table");
    expect(within(table).getAllByRole("row")).toHaveLength(3);
    expect(within(table).getByText("Max Maintainer")).toBeInTheDocument();
    expect(within(table).getAllByText(/scratch-pg/)).toHaveLength(2);
    expect(callsOf("JobTestRestores")[0]?.variables).toMatchObject({ jobId: JOB_ID });
    // Backup owners cannot start one.
    expect(screen.queryByRole("button", { name: "Run test restore now" })).toBeNull();
  });

  it("shows one test restore with every database's findings", async () => {
    renderApp(`/jobs/${JOB_ID}/test-restores/${TR_ID}`, {
      ...baseHandlers(),
      TestRestoreDetail: () => ok({ testRestore: testRestoreDetail() }),
    });
    expect(await screen.findByRole("heading", { name: /Billing DB/ })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open the run" })).toHaveAttribute(
      "href",
      `/runs/${RUN_ID}`,
    );
    const dbs = screen.getAllByTestId("test-restore-db");
    expect(dbs).toHaveLength(2);
    const billing = dbs[0]!;
    expect(within(billing).getByText("public.invoices")).toBeInTheDocument();
    expect(within(billing).getByText("-100")).toBeInTheDocument();
    expect(within(billing).getByText("+2")).toBeInTheDocument();
    expect(within(dbs[1]!).getByText("0 findings")).toBeInTheDocument();
  });
});

describe("test-restore settings", () => {
  it("needs an admin acknowledgement for a privileged target and a fresh second factor", async () => {
    let stepped = false;
    const sent: Vars[] = [];
    const { user } = renderApp(`/jobs/${JOB_ID}/edit`, {
      ...baseHandlers(),
      ...editorHandlers([
        scratchConnection(privileged),
        scratchConnection(null, { id: "other", name: "mysql-x", engine: "MYSQL" }),
        scratchConnection(null, { id: "tmp", name: "temp-x", temporary: true }),
      ]),
      ...stepUpHandlers(() => {
        stepped = true;
      }),
      SetJobTestRestore: (vars) => {
        sent.push(vars);
        if (!stepped) return gqlError("STEP_UP_REQUIRED", "please confirm your identity again");
        return ok({
          setJobTestRestore: {
            __typename: "Job",
            id: JOB_ID,
            testRestore: { ...jobDetail().testRestore, enabled: true },
          },
        });
      },
    });
    const form = await screen.findByRole("form", { name: "Test-restore settings" });
    const select = within(form).getByRole("combobox", { name: "Scratch target" });
    // Only permanent connections with the job's engine are offered.
    await waitFor(() => {
      expect(within(select).getByRole("option", { name: "scratch-pg" })).toBeInTheDocument();
    });
    expect(within(select).queryByRole("option", { name: /mysql-x|temp-x/ })).toBeNull();

    await user.click(within(form).getByLabelText("Scheduled test restores"));
    expect(within(form).getByText(/Choose a scratch target/)).toBeInTheDocument();
    await user.selectOptions(select, SCRATCH_ID);
    const verdict = within(form).getByTestId("target-verdict");
    expect(within(verdict).getByText("Privileged role")).toBeInTheDocument();
    expect(within(verdict).getByText("Superuser")).toBeInTheDocument();
    await user.click(within(form).getByLabelText(/I accept that SQL in the tested backups/));
    await user.click(within(form).getByRole("button", { name: "Save test-restore settings" }));

    const stepUp = await screen.findByRole("dialog", { name: "Confirm your identity" });
    await user.type(within(stepUp).getByLabelText("6-digit code"), "123456");
    await user.click(within(stepUp).getByRole("button", { name: "Confirm" }));
    expect(await screen.findByText("Test-restore settings saved.")).toBeInTheDocument();
    expect(sent).toHaveLength(2);
    expect(sent[1]).toMatchObject({
      jobId: JOB_ID,
      input: {
        enabled: true,
        targetConnectionId: SCRATCH_ID,
        acknowledgePrivilegedTarget: true,
      },
    });
  });

  it.each([
    ["MANUAL", { kind: "MANUAL", every: null, unit: null, anchor: null, cron: null }],
    ["CRON", { kind: "CRON", every: null, unit: null, anchor: null, cron: "0 4 * * 0" }],
    ["INTERVAL", { kind: "INTERVAL", every: 1, unit: "WEEKS", anchor: "2026-11-01T04:00" }],
    ["missing", undefined],
  ])("renders the editor for a %s test-restore schedule", async (_, schedule) => {
    const base = jobDetail();
    const testRestore: Record<string, unknown> = { ...base.testRestore, enabled: true };
    if (schedule) {
      testRestore["schedule"] = {
        __typename: "Schedule",
        weekday: null,
        dayOfMonth: null,
        timezone: "UTC",
        ...schedule,
      };
    } else {
      delete testRestore["schedule"];
    }
    renderApp(`/jobs/${JOB_ID}/edit`, {
      ...baseHandlers(),
      ...editorHandlers([]),
      JobDetail: () => ok({ job: { ...base, testRestore } }),
    });
    const form = await screen.findByRole("form", { name: "Test-restore settings" });
    expect(within(form).getByText("When test restores run")).toBeInTheDocument();
    expect(within(form).getAllByRole("radio").length).toBeGreaterThan(0);
  });

  it("tells maintainers that a privileged target needs an admin", async () => {
    const { user } = renderApp(`/jobs/${JOB_ID}/edit`, {
      ...baseHandlers(meFixture({}, "MAINTAINER")),
      ...editorHandlers([scratchConnection(privileged)]),
    });
    const form = await screen.findByRole("form", { name: "Test-restore settings" });
    const select = within(form).getByRole("combobox", { name: "Scratch target" });
    await waitFor(() => {
      expect(within(select).getByRole("option", { name: "scratch-pg" })).toBeInTheDocument();
    });
    await user.selectOptions(select, SCRATCH_ID);
    expect(within(form).getByText(/only an admin can designate it/)).toBeInTheDocument();
    expect(within(form).queryByLabelText(/I accept that SQL/)).toBeNull();
  });

  it("shows the stored designation verdict and acknowledgement of the current target", async () => {
    renderApp(`/jobs/${JOB_ID}/edit`, {
      ...baseHandlers(),
      ...editorHandlers([scratchConnection(null, { id: CONN_ID, name: "billing-pg" })]),
      JobDetail: () =>
        ok({
          job: jobDetail({
            testRestore: {
              ...jobDetail().testRestore,
              target: {
                __typename: "JobConnectionRef",
                id: CONN_ID,
                name: "billing-pg",
                engine: "POSTGRES",
                deleted: false,
              },
              targetPrivileges: { ...privileged, status: "UNKNOWN", reasons: ["roles_granted"] },
              privilegedTargetAcknowledgedAt: "2026-10-20T10:00:00Z",
            },
          }),
        }),
    });
    const verdict = await screen.findByTestId("target-verdict");
    expect(within(verdict).getByText("Privileges unknown")).toBeInTheDocument();
    expect(within(verdict).getByText("probed when it was designated")).toBeInTheDocument();
    expect(screen.getByText(/Acknowledged by an admin/)).toBeInTheDocument();
  });
});

describe("owners panel", () => {
  it("adds owners chosen in the shared UserPicker in one grant", async () => {
    const assigned: Vars[] = [];
    const entry = (id: string, displayName: string, role = "USER") => ({
      __typename: "UserDirectoryEdge",
      node: {
        __typename: "UserDirectoryEntry",
        id,
        email: `${id}@example.com`,
        displayName,
        role,
        disabled: false,
      },
    });
    const { user } = renderApp(`/jobs/${JOB_ID}/edit`, {
      ...baseHandlers(),
      ...editorHandlers([]),
      UserPickerCandidates: () =>
        ok({
          userDirectory: {
            __typename: "UserDirectoryConnection",
            edges: [
              entry("u-1", "Olive Owner"),
              entry("u-2", "Uma User"),
              entry("u-3", "Vic User"),
            ],
          },
        }),
      AssignJobOwners: (vars) => {
        assigned.push(vars);
        return ok({ assignJobOwners: [] });
      },
    });
    const list = await screen.findByRole("list", { name: "Add backup owners" });
    // Olive already owns the job and is not offered again.
    await within(list).findByRole("button", { name: /Uma User/ });
    expect(within(list).queryByRole("button", { name: /Olive Owner/ })).toBeNull();
    await user.click(within(list).getByRole("button", { name: /Uma User/ }));
    await user.click(within(list).getByRole("button", { name: /Vic User/ }));
    await user.click(screen.getByRole("button", { name: "Add 2 owners" }));
    expect(await screen.findByText("2 owners added.")).toBeInTheDocument();
    expect(assigned).toEqual([{ jobId: JOB_ID, users: ["u-2", "u-3"] }]);
  });
});
