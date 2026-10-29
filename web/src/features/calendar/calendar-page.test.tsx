import { screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { baseHandlers, ok, renderApp, type Handler, type Vars } from "@/test/mock-api";

const J1 = "b0000000-0000-4000-8000-000000000001";
const J2 = "b0000000-0000-4000-8000-000000000002";
const C1 = "c0000000-0000-4000-8000-000000000001";
const C2 = "c0000000-0000-4000-8000-000000000002";
const D1 = "d0000000-0000-4000-8000-000000000001";
const R1 = "e0000000-0000-4000-8000-000000000001";

function event(over: Record<string, unknown>) {
  return {
    __typename: "CalendarEvent",
    jobId: J1,
    jobName: "Shop nightly",
    kind: "PLANNED",
    start: "2026-10-14T22:30:00Z",
    end: null,
    status: null,
    trigger: null,
    runId: null,
    destinationId: null,
    projected: false,
    paused: false,
    startedEarly: false,
    overdue: false,
    detail: null,
    ...over,
  };
}

const EVENTS = [
  event({}),
  event({
    kind: "RUN",
    status: "FAILED",
    runId: R1,
    start: "2026-10-13T02:00:00Z",
    end: "2026-10-13T02:10:00Z",
    jobId: J2,
    jobName: "CRM",
  }),
  event({
    kind: "PLANNED",
    paused: true,
    start: "2026-10-15T01:00:00Z",
    jobId: J2,
    jobName: "CRM",
  }),
  event({ kind: "SKIPPED", start: "2026-10-16T01:00:00Z", detail: "job disabled" }),
  event({
    kind: "EXPIRY",
    projected: true,
    runId: R1,
    destinationId: D1,
    start: "2026-10-20T00:00:00Z",
  }),
  event({
    kind: "EXPIRY",
    projected: false,
    runId: R1,
    destinationId: D1,
    start: "2026-10-21T00:00:00Z",
  }),
];

function handlers(truncated = false, calls: Vars[] = []): Record<string, Handler> {
  return {
    ...baseHandlers(),
    CalendarJobs: () =>
      ok({
        jobs: {
          __typename: "JobConnection",
          edges: [
            {
              __typename: "JobEdge",
              node: {
                __typename: "Job",
                id: J1,
                name: "Shop nightly",
                connection: { __typename: "JobConnectionRef", id: C1, name: "pg-main" },
                destinations: [{ __typename: "JobDestinationRef", id: D1, name: "s3-eu" }],
              },
            },
            {
              __typename: "JobEdge",
              node: {
                __typename: "Job",
                id: J2,
                name: "CRM",
                connection: { __typename: "JobConnectionRef", id: C2, name: "mysql-crm" },
                destinations: [],
              },
            },
          ],
          pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: null },
        },
      }),
    Calendar: (vars) => {
      calls.push(vars);
      return ok({
        calendar: {
          __typename: "Calendar",
          events: EVENTS,
          truncated,
          from: vars["from"],
          to: vars["to"],
          timezone: vars["timezone"],
        },
      });
    },
  };
}

const cell = (key: string) => document.querySelector<HTMLElement>(`[data-day="${key}"]`);

describe("calendar page", () => {
  it("renders the month with every event kind and links", async () => {
    const calls: Vars[] = [];
    renderApp("/calendar?date=2026-10-14&tz=UTC", handlers(false, calls));
    expect(await screen.findByRole("grid")).toBeInTheDocument();
    const day14 = await waitFor(() => {
      const c = cell("2026-10-14");
      expect(c).not.toBeNull();
      expect(within(c!).getAllByRole("link")).toHaveLength(1);
      return c!;
    });
    expect(within(day14).getByRole("link")).toHaveAccessibleName(/Planned, Shop nightly, 22:30/);
    const run = within(cell("2026-10-13")!).getByRole("link");
    expect(run).toHaveAttribute("href", `/runs/${R1}`);
    expect(run).toHaveAttribute("data-variant", "run-failed");
    expect(within(cell("2026-10-15")!).getByRole("link")).toHaveAttribute("data-variant", "paused");
    expect(within(cell("2026-10-16")!).getByRole("link")).toHaveAccessibleName(
      /Skipped.*job disabled/,
    );
    expect(within(cell("2026-10-20")!).getByRole("link")).toHaveAccessibleName(
      /Expiry \(projected\)/,
    );
    expect(within(cell("2026-10-21")!).getByRole("link")).toHaveAccessibleName(/Expires \(exact\)/);
    expect(calls[0]).toMatchObject({
      from: "2026-09-28T00:00:00.000Z",
      to: "2026-11-02T00:00:00.000Z",
      timezone: "UTC",
      jobIds: null,
    });
    expect(screen.queryByText("Some events are not shown")).toBeNull();
  });

  it("shows the truncated notice", async () => {
    renderApp("/calendar?date=2026-10-14&tz=UTC", handlers(true));
    expect(await screen.findByText("Some events are not shown")).toBeInTheDocument();
  });

  it("buckets by the chosen time zone", async () => {
    const calls: Vars[] = [];
    const { user } = renderApp("/calendar?date=2026-10-14&tz=UTC", handlers(false, calls));
    await waitFor(() => {
      expect(within(cell("2026-10-14")!).queryAllByRole("link")).toHaveLength(1);
    });
    await user.selectOptions(screen.getByLabelText("Time zone"), "Asia/Tokyo");
    await waitFor(() => {
      expect(calls.at(-1)).toMatchObject({
        timezone: "Asia/Tokyo",
        from: "2026-09-27T15:00:00.000Z",
      });
    });
    // 22:30 UTC on the 14th is 07:30 on the 15th in Tokyo.
    await waitFor(() => {
      expect(within(cell("2026-10-15")!).getAllByRole("link")[0]).toHaveAccessibleName(
        /Shop nightly, 07:30/,
      );
    });
    expect(within(cell("2026-10-14")!).queryAllByRole("link")).toHaveLength(0);
  });

  it("filters by connection and destination through job ids", async () => {
    const calls: Vars[] = [];
    const { user } = renderApp("/calendar?date=2026-10-14&tz=UTC", handlers(false, calls));
    await screen.findByRole("option", { name: "mysql-crm" });
    await user.selectOptions(screen.getByLabelText("Connection"), C2);
    await waitFor(() => {
      expect(calls.at(-1)).toMatchObject({ jobIds: [J2] });
    });
    await user.selectOptions(screen.getByLabelText("Connection"), "");
    await user.selectOptions(screen.getByLabelText("Destination"), D1);
    await waitFor(() => {
      expect(calls.at(-1)).toMatchObject({ jobIds: [J1] });
    });
  });

  it("renders week and day views", async () => {
    const calls: Vars[] = [];
    const { user } = renderApp("/calendar?date=2026-10-14&tz=UTC", handlers(false, calls));
    await screen.findByRole("grid");
    await user.click(screen.getByRole("button", { name: "Week" }));
    await waitFor(() => {
      expect(calls.at(-1)).toMatchObject({
        from: "2026-10-12T00:00:00.000Z",
        to: "2026-10-19T00:00:00.000Z",
      });
    });
    expect(document.querySelectorAll('[role="gridcell"]')).toHaveLength(7);
    await user.click(screen.getByRole("button", { name: "Day" }));
    await waitFor(() => {
      expect(calls.at(-1)).toMatchObject({
        from: "2026-10-14T00:00:00.000Z",
        to: "2026-10-15T00:00:00.000Z",
      });
    });
    expect(screen.getByText("22:00")).toBeInTheDocument();
  });

  it("navigates days with the arrow keys and opens a day with Enter", async () => {
    const { user, router } = renderApp("/calendar?date=2026-10-31&tz=UTC", handlers());
    await screen.findByRole("grid");
    const start = cell("2026-10-31")!;
    expect(start).toHaveAttribute("tabindex", "0");
    start.focus();
    await user.keyboard("{ArrowLeft}");
    await waitFor(() => {
      expect(cell("2026-10-30")).toHaveFocus();
    });
    await user.keyboard("{ArrowUp}");
    await waitFor(() => {
      expect(cell("2026-10-23")).toHaveFocus();
    });
    // Beyond the month the view follows.
    await user.keyboard("{PageDown}");
    await waitFor(() => {
      expect(router.state.location.search).toContain("date=2026-11-23");
    });
    await waitFor(() => {
      expect(cell("2026-11-23")).toHaveFocus();
    });
    await user.keyboard("{Enter}");
    await waitFor(() => {
      expect(router.state.location.search).toContain("view=day");
    });
  });
});
