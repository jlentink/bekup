import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import {
  ART_ID,
  emptyLogs,
  JOB_ID,
  noTestRestores,
  RUN_ID,
  runDetail,
  runItemStats,
  runJobContext,
} from "@/features/jobs/test-fixtures";
import { baseHandlers, gqlError, meFixture, ok, renderApp, type Vars } from "@/test/mock-api";

import { restoreCommandFor } from "./download-model";
import { appendLines, compareIds, type LogLine } from "./log-model";
import { LogViewer } from "./log-viewer";
import { groupIssues } from "./completeness-model";

function line(id: number, text = `line ${String(id)}`): LogLine {
  return {
    id: String(id),
    time: "2026-10-28T03:00:00Z",
    level: "INFO",
    stream: "STDOUT",
    line: text,
  };
}

const runHandlers = (run = runDetail()) => ({
  RunDetail: () => ok({ run }),
  RunJobContext: () => ok(runJobContext),
  RunTestRestores: () => ok(noTestRestores),
  RunItemStats: () => ok(runItemStats(null)),
  RunLogs: () => ok(emptyLogs),
});

describe("log helpers", () => {
  it("compares ids numerically and drops replayed lines", () => {
    expect(compareIds("9", "10")).toBeLessThan(0);
    expect(compareIds("10", "10")).toBe(0);
    const a = appendLines([], [line(1), line(2)]);
    const b = appendLines(a, [line(2), line(3)]);
    expect(b.map((l) => l.id)).toEqual(["1", "2", "3"]);
    expect(appendLines(b, [line(1)])).toBe(b);
  });
});

describe("LogViewer", () => {
  it("renders only a window, follows the tail and pauses on toggle", async () => {
    const user = userEvent.setup();
    const many = Array.from({ length: 500 }, (_, i) => line(i + 1));
    const { rerender } = render(<LogViewer lines={many.slice(0, 200)} live />);
    const log = screen.getByRole("log");
    expect(log).toHaveAttribute("data-follow", "true");
    // Virtualized: far fewer rows than lines; the tail is visible.
    expect(screen.getAllByTestId("log-line").length).toBeLessThan(80);
    expect(screen.getByText("line 200")).toBeInTheDocument();

    rerender(<LogViewer lines={many} live />);
    expect(screen.getByText("line 500")).toBeInTheDocument();
    expect(screen.getByText("500 lines")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Follow" }));
    expect(screen.getByRole("log")).toHaveAttribute("data-follow", "false");
    expect(screen.getByRole("button", { name: "Follow" })).toHaveAttribute("aria-pressed", "false");
  });

  it("copies the whole log", async () => {
    const user = userEvent.setup();
    const writes: string[] = [];
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: (t: string) => (writes.push(t), Promise.resolve()) },
    });
    render(<LogViewer lines={[line(1, "hello"), line(2, "world")]} />);
    await user.click(screen.getByRole("button", { name: "Copy log" }));
    expect(writes[0]).toContain("hello");
    expect(writes[0]).toContain("world");
  });
});

describe("download helpers", () => {
  it("fills safe names into the restore command only", () => {
    expect(
      restoreCommandFor("pg_restore -d <database> <dump-file>", "billing-20261028.dump", "billing"),
    ).toBe("pg_restore -d billing billing-20261028.dump");
    expect(restoreCommandFor("mysql <database> < <dump-file>", "a b.sql", "x; rm -rf /")).toBe(
      "mysql <database> < 'a b.sql'",
    );
  });
});

describe("completeness evidence", () => {
  it("groups item warnings by code and ignores notices", () => {
    const run = runDetail();
    const issues = groupIssues(run as unknown as Parameters<typeof groupIssues>[0]);
    expect(issues).toHaveLength(1);
    expect(issues[0]?.code).toBe("insufficient_privileges");
    expect(issues[0]?.entries).toHaveLength(1);
  });
});

describe("run detail", () => {
  it("shows the schema version of each database with words for its status", async () => {
    const base = runDetail();
    const item = base.items[0]!;
    const run = runDetail({
      items: [
        item,
        {
          ...item,
          id: "item-2",
          database: "ledger",
          position: 1,
          schemaVersion: null,
          schemaVersionStatus: "UNAVAILABLE",
          artifacts: [],
          warnings: [],
        },
      ],
    });
    renderApp(`/runs/${RUN_ID}`, { ...baseHandlers(), ...runHandlers(run) });
    const items = await screen.findAllByTestId("run-item");
    expect(within(items[0]!).getByTestId("schema-version")).toHaveTextContent("20241001120000");
    expect(within(items[1]!).getByTestId("schema-version")).toHaveTextContent("Unavailable");
  });

  it("styles warnings and notices apart and shows the evidence", async () => {
    renderApp(`/runs/${RUN_ID}`, { ...baseHandlers(), ...runHandlers() });
    const warnings = await screen.findByRole("region", { name: /^Warnings/ });
    const notices = screen.getByRole("region", { name: /^Notices/ });
    expect(warnings).toHaveAttribute("data-kind", "warning");
    expect(notices).toHaveAttribute("data-kind", "notice");
    expect(within(notices).getByText("Databases excluded")).toBeInTheDocument();
    expect(within(warnings).getByText("Insufficient privileges")).toBeInTheDocument();
    expect(await screen.findByTestId("evidence-exactness")).toHaveTextContent(
      /Estimates \(planner statistics\)/,
    );
    expect(screen.getByText(/covers 1 of 1/)).toBeInTheDocument();
    const artifact = screen.getByTestId("artifact");
    expect(within(artifact).getByRole("link", { name: /Restore/ })).toHaveAttribute(
      "href",
      `/restore?artifactId=${ART_ID}`,
    );
  });

  it("renders the per-item statistics with count badges and findings (D-187)", async () => {
    renderApp(`/runs/${RUN_ID}`, {
      ...baseHandlers(),
      ...runHandlers(),
      RunItemStats: () => ok(runItemStats()),
    });
    const card = await screen.findByTestId("item-stats");
    expect(screen.queryByText(/bekup verify/)).toBeNull();
    expect(within(card).getByText("deep")).toBeInTheDocument();
    expect(within(card).getByText(/3 of 5 listed/)).toBeInTheDocument();
    expect(within(card).getByText("1 exact · 1 estimated · 1 unknown")).toBeInTheDocument();
    // Truncated list and findings: warnings and notices styled apart.
    expect(within(card).getByRole("note")).toHaveTextContent(/Only 3 of 5 tables are listed/);
    const warning = within(card).getByText("private.t1").closest("[data-kind]");
    expect(warning).toHaveAttribute("data-kind", "warning");
    const notice = within(card).getByText("scratch").closest("[data-kind]");
    expect(notice).toHaveAttribute("data-kind", "notice");
    expect(within(card).getByText("and more")).toBeInTheDocument();
    // Per-table rows with their exactness.
    const rows = within(card).getAllByRole("row");
    const invoices = rows.find((r) => r.textContent.includes("public.invoices"));
    const events = rows.find((r) => r.textContent.includes("public.events"));
    const log = rows.find((r) => r.textContent.includes("audit.log"));
    if (!invoices || !events || !log) throw new Error("rows missing");
    expect(within(invoices).getByText("exact")).toHaveAttribute("data-count", "EXACT");
    expect(within(events).getByText("estimate")).toHaveAttribute("data-count", "ESTIMATE");
    expect(within(events).getByText(/≈/)).toBeInTheDocument();
    expect(within(log).getByText("unknown")).toHaveAttribute("data-count", "UNKNOWN");
    expect(within(log).getByText("—")).toBeInTheDocument();
    expect(within(card).getByText("materialized views")).toBeInTheDocument();
  });

  it("explains that statistics follow once an active run finishes", async () => {
    const { callsOf } = renderApp(`/runs/${RUN_ID}`, {
      ...baseHandlers(),
      ...runHandlers(runDetail({ status: "RUNNING", finishedAt: null })),
    });
    expect(
      await screen.findByText("Statistics appear once the run has finished."),
    ).toBeInTheDocument();
    expect(callsOf("RunItemStats")).toHaveLength(0);
  });

  it("follows an active run on one page stream (runActivity, D-197)", async () => {
    const { callsOf, subscriptions } = renderApp(`/runs/${RUN_ID}`, {
      ...baseHandlers(),
      ...runHandlers(runDetail({ status: "RUNNING", finishedAt: null })),
      RunActivity: () =>
        ok({
          runActivity: {
            __typename: "RunActivity",
            run: null,
            logs: {
              __typename: "RunLogBatch",
              runId: RUN_ID,
              endId: "7",
              done: false,
              lines: [{ __typename: "RunLogLine", ...line(7, "dumping billing") }],
            },
          },
        }),
    });
    expect(await screen.findByText("dumping billing")).toBeInTheDocument();
    expect(callsOf("RunActivity")).toHaveLength(1);
    expect(callsOf("RunActivity")[0]?.variables).toEqual({ runId: RUN_ID });
    // The shell's shared account stream plus one page stream, nothing else.
    expect(subscriptions().sort()).toEqual(["AccountEvents", "RunActivity"]);
  });

  it("opens no stream for a finished run", async () => {
    const { subscriptions } = renderApp(`/runs/${RUN_ID}`, {
      ...baseHandlers(),
      ...runHandlers(),
    });
    expect(await screen.findByTestId("artifact")).toBeInTheDocument();
    expect(subscriptions()).toEqual(["AccountEvents"]);
  });

  it("lets admins download behind a step-up and shows the restore command", async () => {
    let stepped = false;
    const { user, callsOf } = renderApp(`/runs/${RUN_ID}`, {
      ...baseHandlers(),
      ...runHandlers(),
      CreateDownloadTicket: (v: Vars) =>
        stepped
          ? ok({
              createDownloadTicket: {
                __typename: "DownloadTicket",
                url: "/api/download/opaque",
                expiresAt: new Date(Date.now() + 60_000).toISOString(),
                mode: v["mode"],
                fileName: "billing-db-billing-20261028T030002Z.dump",
                format: "dump",
                restoreCommand: "pg_restore --no-owner -d <database> <dump-file>",
              },
            })
          : gqlError("STEP_UP_REQUIRED", "please confirm your identity again"),
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
            aal2At: null,
            aal: 1,
          },
        });
      },
    });
    const artifact = await screen.findByTestId("artifact");
    await user.click(within(artifact).getByRole("button", { name: "Download" }));
    const dialog = await screen.findByRole("dialog", { name: "billing" });
    expect(within(dialog).queryByLabelText("age private key")).toBeNull();
    await user.click(within(dialog).getByRole("button", { name: "Create link" }));
    const stepUp = await screen.findByRole("dialog", { name: "Confirm your identity" });
    await user.type(within(stepUp).getByLabelText("6-digit code"), "123456");
    await user.click(within(stepUp).getByRole("button", { name: "Confirm" }));
    const ready = await screen.findByRole("dialog", { name: "Your link is ready" });
    expect(within(ready).getByRole("link", { name: /Download billing-db/ })).toHaveAttribute(
      "href",
      "/api/download/opaque",
    );
    expect(within(ready).getByTestId("restore-command")).toHaveTextContent(
      "pg_restore --no-owner -d billing billing-db-billing-20261028T030002Z.dump",
    );
    expect(callsOf("CreateDownloadTicket").at(-1)?.variables).toEqual({
      artifactId: ART_ID,
      mode: "PLAIN",
    });
  });

  it("asks for the age identity for plain downloads of recipient backups", async () => {
    const base = runDetail();
    const item = base.items[0]!;
    const run = runDetail({
      items: [
        {
          ...item,
          artifacts: [
            {
              ...item.artifacts[0]!,
              encryption: {
                __typename: "ArtifactEncryption",
                mode: "RECIPIENT",
                passphraseVersion: null,
                recipientVersion: 1,
              },
            },
          ],
        },
      ],
    });
    const { user } = renderApp(`/runs/${RUN_ID}`, {
      ...baseHandlers(meFixture({}, "USER")),
      ...runHandlers(run),
    });
    const artifact = await screen.findByTestId("artifact");
    await user.click(within(artifact).getByRole("button", { name: "Download" }));
    const dialog = await screen.findByRole("dialog", { name: "billing" });
    const create = within(dialog).getByRole("button", { name: "Create link" });
    expect(create).toBeDisabled();
    await user.type(within(dialog).getByLabelText("age private key"), "not a key");
    expect(within(dialog).getByText(/Enter one AGE-SECRET-KEY-1/)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("radio", { name: /Raw file/ }));
    expect(within(dialog).queryByLabelText("age private key")).toBeNull();
    expect(create).toBeEnabled();
  });

  it("never offers downloads to maintainers but lets them cancel an active run", async () => {
    const { user, callsOf } = renderApp(`/runs/${RUN_ID}`, {
      ...baseHandlers(meFixture({}, "MAINTAINER")),
      ...runHandlers(runDetail({ status: "RUNNING", finishedAt: null })),
      CancelRun: () =>
        ok({
          cancelRun: {
            __typename: "RunCancellation",
            runId: RUN_ID,
            status: "RUNNING",
            retryCanceled: false,
          },
        }),
    });
    const artifact = await screen.findByTestId("artifact");
    expect(within(artifact).queryByRole("button", { name: "Download" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Cancel run" }));
    const dialog = await screen.findByRole("dialog", { name: "Cancel this run?" });
    await user.click(within(dialog).getByRole("button", { name: "Cancel run" }));
    await waitFor(() => {
      expect(callsOf("CancelRun")).toHaveLength(1);
    });
  });

  it("hides cancel from backup owners", async () => {
    renderApp(`/runs/${RUN_ID}`, {
      ...baseHandlers(meFixture({}, "USER")),
      ...runHandlers(runDetail({ status: "RUNNING", finishedAt: null })),
    });
    expect(await screen.findByTestId("artifact")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Cancel run" })).toBeNull();
    expect(screen.getByRole("button", { name: "Download" })).toBeInTheDocument();
  });

  it("lists runs with job links on /runs", async () => {
    renderApp("/runs", {
      ...baseHandlers(),
      RunsList: () =>
        ok({
          runs: {
            __typename: "RunConnection",
            edges: [
              {
                __typename: "RunEdge",
                cursor: "c1",
                node: {
                  __typename: "Run",
                  id: RUN_ID,
                  status: "PARTIAL",
                  trigger: "EARLY",
                  scheduledFor: "2026-10-29T03:00:00Z",
                  queuedAt: "2026-10-28T10:00:00Z",
                  startedAt: "2026-10-28T10:00:01Z",
                  finishedAt: "2026-10-28T10:02:00Z",
                  durationSeconds: 119,
                  bytesStored: 1024,
                  attempt: 1,
                  job: { __typename: "RunJobRef", id: JOB_ID, name: "Billing DB", deleted: false },
                  warnings: [{ __typename: "RunWarning", code: "stats_partial" }],
                  items: [{ __typename: "RunItem", id: "i1" }],
                },
              },
            ],
            pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: null },
          },
        }),
    });
    const row = await screen.findByTestId("run-row");
    expect(within(row).getByText("Partial")).toBeInTheDocument();
    expect(within(row).getByText("Started early")).toBeInTheDocument();
    expect(within(row).getByText("1 warning")).toBeInTheDocument();
    expect(within(row).getByRole("link", { name: "Billing DB" })).toHaveAttribute(
      "href",
      `/jobs/${JOB_ID}`,
    );
  });
});
