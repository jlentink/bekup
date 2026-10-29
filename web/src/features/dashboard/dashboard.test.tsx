import { screen, waitFor, within } from "@testing-library/react";
import type { FormattedExecutionResult } from "graphql";
import { describe, expect, it } from "vitest";

import { baseHandlers, meFixture, ok, renderApp, type Handler } from "@/test/mock-api";

const HOUR = 3_600_000;
const iso = (offset: number) => new Date(Date.now() + offset).toISOString();

function jobNode(id: string, name: string, overrides: Record<string, unknown> = {}) {
  return {
    __typename: "Job",
    id,
    name,
    enabled: true,
    nextRunAt: iso(2 * HOUR),
    scheduleDescription: "Every day at 03:00",
    missed: false,
    missedSince: null,
    connection: { __typename: "JobConnectionRef", id: "c1", name: "pg-main", deleted: false },
    encryption: {
      __typename: "JobEncryption",
      configured: true,
      mode: "PASSPHRASE",
      optedOut: false,
    },
    lastRun: {
      __typename: "Run",
      id: `run-${id}`,
      status: "SUCCESS",
      queuedAt: iso(-3 * HOUR),
      finishedAt: iso(-3 * HOUR + 60_000),
      warnings: [],
    },
    ...overrides,
  };
}

function jobs(nodes: ReturnType<typeof jobNode>[]) {
  return ok({
    jobs: {
      __typename: "JobConnection",
      edges: nodes.map((node) => ({ __typename: "JobEdge", node })),
      pageInfo: { __typename: "PageInfo", hasNextPage: false },
    },
  });
}

function runNode(id: string, jobId: string, jobName: string, status: string, extra = {}) {
  return {
    __typename: "Run",
    id,
    status,
    trigger: "SCHEDULE",
    queuedAt: iso(-HOUR),
    startedAt: iso(-HOUR),
    finishedAt: null,
    errorCode: null,
    errorMessage: null,
    warnings: [],
    job: { __typename: "RunJobRef", id: jobId, name: jobName },
    ...extra,
  };
}

const runsPage = (nodes: ReturnType<typeof runNode>[], more = false) => ({
  __typename: "RunConnection",
  edges: nodes.map((node) => ({ __typename: "RunEdge", node })),
  pageInfo: { __typename: "PageInfo", hasNextPage: more },
});

function runs({
  running = [] as ReturnType<typeof runNode>[],
  failed = [] as ReturnType<typeof runNode>[],
} = {}) {
  return ok({ running: runsPage(running), queued: runsPage([]), failed: runsPage(failed) });
}

const calendar = (events: Record<string, unknown>[] = []) =>
  ok({
    calendar: {
      __typename: "Calendar",
      events: events.map((e) => ({
        __typename: "CalendarEvent",
        status: null,
        runId: null,
        paused: false,
        startedEarly: false,
        overdue: false,
        detail: null,
        ...e,
      })),
      truncated: false,
    },
  });

const noRestores = () => ok({ restores: { __typename: "RestoreConnection", edges: [] } });
const alerts = (unread = 0) =>
  ok({
    unreadNotificationCount: unread,
    notifications: {
      __typename: "NotificationConnection",
      edges:
        unread > 0
          ? [
              {
                __typename: "NotificationEdge",
                node: {
                  __typename: "Notification",
                  id: "n1",
                  type: "backup.failed",
                  severity: "ERROR",
                  title: "Backup of Billing failed",
                  link: "https://evil.example/x",
                  createdAt: iso(-HOUR),
                },
              },
            ]
          : [],
    },
  });

function connection(id: string, name: string, overrides: Record<string, unknown> = {}) {
  return {
    __typename: "Connection",
    id,
    name,
    tlsMode: "PINNED",
    insecureTransport: false,
    pinStatus: "VALID",
    pinExpiresAt: null,
    lastTest: null,
    ...overrides,
  };
}

const infra = (conns: ReturnType<typeof connection>[], dests: Record<string, unknown>[] = []) =>
  ok({
    connections: conns,
    destinations: dests.map((d) => ({
      __typename: "Destination",
      ready: true,
      lastTest: null,
      ...d,
    })),
  });

const storage = () =>
  ok({
    destinationUsage: [
      {
        __typename: "DestinationUsage",
        destinationId: "d1",
        name: "vault-disk",
        type: "DISK",
        storedBytes: 3 * 1024 * 1024,
        artifactCount: 1,
      },
    ],
  });

function restoreNode(id: string, status: string) {
  return {
    __typename: "Restore",
    id,
    status,
    createdAt: iso(-60_000),
    startedAt: status === "QUEUED" ? null : iso(-30_000),
    sourceDatabase: "billing",
    targetDatabase: "billing_copy",
    kind: "DATABASE",
    job: { __typename: "RunJobRef", id: "j1", name: "Billing DB" },
    target: { __typename: "RestoreTargetRef", id: "c1", name: "pg-main" },
  };
}

const systemHealth = () =>
  ok({
    systemHealth: {
      __typename: "SystemHealth",
      checkedAt: iso(0),
      runner: {
        __typename: "RunnerHealth",
        up: true,
        dockerOk: false,
        version: "1.0.0",
        activeRuns: 1,
        maxConcurrentRuns: 4,
      },
      certificates: [
        {
          __typename: "CertificateExpiry",
          component: "runner_client",
          notAfter: iso(3 * 24 * HOUR),
        },
      ],
      queue: [{ __typename: "QueueDepth", queue: "backup", state: "available", jobs: 2 }],
      earliestPinNotAfter: null,
      oldestLastSuccess: iso(-HOUR),
      schedulerEnabled: true,
      lastSchedulerTick: iso(-60_000),
    },
  });

/** Everything an admin's dashboard requests, with a healthy two-job setup. */
function handlers(
  overrides: Record<string, Handler> = {},
  role: "ADMIN" | "MAINTAINER" | "USER" = "ADMIN",
) {
  return {
    ...baseHandlers(meFixture({}, role)),
    DashboardJobs: () => jobs([jobNode("j1", "Billing DB"), jobNode("j2", "Shop DB")]),
    DashboardRuns: () => runs(),
    DashboardCalendar: () => calendar(),
    DashboardRestores: noRestores,
    DashboardAlerts: () => alerts(),
    DashboardInfra: () =>
      infra([connection("c1", "pg-main")], [{ id: "d1", name: "vault-disk", type: "DISK" }]),
    DashboardStorage: storage,
    DashboardSystemHealth: systemHealth,
    ...overrides,
  } satisfies Record<string, Handler>;
}

describe("dashboard roles", () => {
  it("shows admins every tile including system health", async () => {
    const { callsOf } = renderApp("/", handlers());
    expect(await screen.findByTestId("tile-system")).toBeInTheDocument();
    expect(screen.getByTestId("tile-job-health")).toBeInTheDocument();
    expect(screen.getByTestId("tile-security")).toBeInTheDocument();
    expect(screen.getByTestId("tile-storage")).toBeInTheDocument();
    expect(await screen.findByTestId("fact-runner")).toHaveTextContent("Docker unavailable");
    expect(screen.getByTestId("fact-runner")).toHaveAttribute("data-level", "critical");
    expect(screen.getByTestId("cert-row")).toHaveAttribute("data-level", "critical");
    expect(await screen.findByTestId("storage-bytes")).toHaveTextContent("3.00 MiB");
    expect(screen.getByTestId("storage-bytes")).not.toHaveTextContent("≥");
    expect(callsOf("DashboardStorage")).toHaveLength(1);
    expect(callsOf("DashboardSystemHealth")).toHaveLength(1);
  });

  it("shows maintainers security and storage but not system health", async () => {
    const { callsOf } = renderApp("/", handlers({}, "MAINTAINER"));
    expect(await screen.findByTestId("tile-security")).toBeInTheDocument();
    expect(screen.getByTestId("tile-storage")).toBeInTheDocument();
    expect(screen.queryByTestId("tile-system")).toBeNull();
    expect(callsOf("DashboardSystemHealth")).toHaveLength(0);
  });

  it("shows backup owners only their jobs and no staff tiles", async () => {
    const { callsOf } = renderApp(
      "/",
      handlers({ DashboardJobs: () => jobs([jobNode("j1", "My DB")]) }, "USER"),
    );
    const tile = await screen.findByTestId("tile-job-health");
    expect(within(tile).getByRole("heading", { name: "Your jobs" })).toBeInTheDocument();
    expect(await within(tile).findAllByTestId("health-card")).toHaveLength(1);
    expect(within(tile).getByText("My DB")).toBeInTheDocument();
    expect(screen.queryByTestId("tile-security")).toBeNull();
    expect(screen.queryByTestId("tile-storage")).toBeNull();
    expect(screen.queryByTestId("tile-system")).toBeNull();
    // No staff-only operations, and no owner filter: the server scopes the jobs.
    for (const op of ["DashboardInfra", "DashboardStorage", "DashboardSystemHealth"])
      expect(callsOf(op)).toHaveLength(0);
    expect(callsOf("DashboardJobs")[0]?.variables).toEqual({ first: 200 });
  });
});

describe("dashboard tiles", () => {
  it("links each job to its page and marks failures, overdue and unencrypted jobs", async () => {
    renderApp(
      "/",
      handlers({
        DashboardJobs: () =>
          jobs([
            jobNode("j1", "Billing DB", {
              lastRun: {
                __typename: "Run",
                id: "r-failed",
                status: "FAILED",
                queuedAt: iso(-HOUR),
                finishedAt: iso(-HOUR),
                warnings: [],
              },
            }),
            jobNode("j2", "Shop DB", { nextRunAt: iso(-HOUR) }),
            jobNode("j3", "Logs DB", {
              encryption: {
                __typename: "JobEncryption",
                configured: true,
                mode: "NONE",
                optedOut: true,
              },
            }),
          ]),
      }),
    );
    const tile = await screen.findByTestId("tile-job-health");
    const cards = await within(tile).findAllByTestId("health-card");
    expect(cards.map((c) => c.getAttribute("data-state"))).toEqual([
      "failing",
      "overdue",
      "healthy",
    ]);
    expect(cards[0]).toHaveAttribute("href", "/jobs/j1");
    expect(within(cards[2]!).getByText("Unencrypted")).toBeInTheDocument();
    expect(await screen.findByTestId("figure-attention")).toHaveTextContent("2");
    const security = await screen.findByTestId("tile-security");
    const finding = await within(security).findByTestId("security-finding");
    expect(finding).toHaveAttribute("data-kind", "unencrypted");
    expect(within(finding).getByRole("link", { name: "Logs DB" })).toHaveAttribute(
      "href",
      "/jobs/j3",
    );
  });

  it("lists failures and missed backups and the next 24 hours", async () => {
    const { callsOf } = renderApp(
      "/",
      handlers({
        DashboardJobs: () =>
          jobs([
            jobNode("j1", "Billing DB"),
            jobNode("j2", "Shop DB", {
              missed: true,
              missedSince: iso(-HOUR),
              lastRun: {
                __typename: "Run",
                id: "run-j2",
                status: "SKIPPED",
                queuedAt: iso(-3 * HOUR),
                finishedAt: iso(-3 * HOUR),
                warnings: [],
              },
            }),
          ]),
        DashboardRuns: () =>
          runs({
            failed: [
              runNode("rf", "j1", "Billing DB", "FAILED", {
                finishedAt: iso(-2 * HOUR),
                errorMessage: "dump failed",
              }),
            ],
          }),
        DashboardCalendar: () =>
          calendar([{ jobId: "j1", jobName: "Billing DB", kind: "PLANNED", start: iso(2 * HOUR) }]),
      }),
    );
    const attention = await screen.findByTestId("tile-attention");
    const items = await within(attention).findAllByTestId("attention-item");
    expect(items.map((i) => i.getAttribute("data-kind"))).toEqual(["missed", "failed"]);
    expect(within(items[0]!).getByRole("link", { name: "Shop DB" })).toHaveAttribute(
      "href",
      "/jobs/j2",
    );
    expect(within(items[1]!).getByRole("link", { name: "Billing DB" })).toHaveAttribute(
      "href",
      "/runs/rf",
    );
    const upcoming = screen.getByTestId("tile-upcoming");
    expect(await within(upcoming).findByTestId("upcoming-run")).toHaveTextContent("Billing DB");
    // Missed comes from the server's missed-backup check (Job.missed).
    const shop = (await screen.findAllByTestId("health-card")).find((c) =>
      c.textContent.includes("Shop DB"),
    );
    expect(shop).toHaveAttribute("data-state", "missed");
    // Only the upcoming calendar slice is read; no past-skips query.
    expect(callsOf("DashboardCalendar")).toHaveLength(1);
  });

  it("never links an alert outside the app", async () => {
    renderApp("/", handlers({ DashboardAlerts: () => alerts(1) }));
    const tile = await screen.findByTestId("tile-alerts");
    expect(await within(tile).findByText("Backup of Billing failed")).toBeInTheDocument();
    expect(within(tile).queryByRole("link", { name: "Backup of Billing failed" })).toBeNull();
    expect(within(tile).getByTestId("unread-count")).toHaveTextContent("1");
  });
});

describe("live updates", () => {
  it("applies run events from the page's one stream", async () => {
    let emit: (r: FormattedExecutionResult) => void = () => undefined;
    const event = new Promise<FormattedExecutionResult>((resolve) => {
      emit = resolve;
    });
    const { callsOf, subscriptions } = renderApp(
      "/",
      handlers({ DashboardActivity: () => event }, "USER"),
    );
    const activity = await screen.findByTestId("tile-activity");
    expect(await within(activity).findByText(/Nothing is running/)).toBeInTheDocument();
    await waitFor(() => {
      expect(callsOf("DashboardActivity")).toHaveLength(1);
    });
    expect(callsOf("DashboardActivity")[0]?.variables).toEqual({});
    // D-197 budget: the shell's account stream plus one page stream.
    expect(subscriptions().sort()).toEqual(["AccountEvents", "DashboardActivity"]);

    emit(
      ok({
        activityUpdated: {
          __typename: "ActivityEvent",
          run: { ...runNode("r-live", "j2", "Shop DB", "RUNNING"), startedAt: iso(-60_000) },
          restore: null,
        },
      }),
    );
    const row = await within(activity).findByTestId("active-run");
    expect(within(row).getByRole("link", { name: "Shop DB" })).toHaveAttribute(
      "href",
      "/runs/r-live",
    );
    const shop = (await screen.findAllByTestId("health-card")).find((c) =>
      c.textContent.includes("Shop DB"),
    );
    expect(shop).toHaveAttribute("data-state", "running");
    expect(screen.getByTestId("figure-running")).toHaveTextContent("1");
  });
});

describe("live restores", () => {
  it("merges restore events from the same stream instead of polling", async () => {
    let emit: (r: FormattedExecutionResult) => void = () => undefined;
    const event = new Promise<FormattedExecutionResult>((resolve) => {
      emit = resolve;
    });
    const { callsOf } = renderApp(
      "/",
      handlers({
        DashboardRestores: () =>
          ok({
            restores: {
              __typename: "RestoreConnection",
              edges: [{ __typename: "RestoreEdge", node: restoreNode("rs1", "STAGING") }],
            },
          }),
        DashboardActivity: () => event,
      }),
    );
    const activity = await screen.findByTestId("tile-activity");
    expect(await within(activity).findAllByTestId("active-restore")).toHaveLength(1);
    await waitFor(() => {
      expect(callsOf("DashboardActivity")).toHaveLength(1);
    });
    emit(
      ok({
        activityUpdated: {
          __typename: "ActivityEvent",
          run: null,
          restore: restoreNode("rs2", "QUEUED"),
        },
      }),
    );
    await waitFor(() => {
      expect(within(activity).getAllByTestId("active-restore")).toHaveLength(2);
    });
    expect(callsOf("DashboardRestores")).toHaveLength(1);
  });
});

describe("empty states", () => {
  it("guides a fresh install to create the first connection", async () => {
    renderApp(
      "/",
      handlers({
        DashboardJobs: () => jobs([]),
        DashboardInfra: () => infra([], []),
      }),
    );
    const guide = await screen.findByTestId("getting-started");
    expect(within(guide).getByRole("link", { name: /Add a connection/ })).toHaveAttribute(
      "href",
      "/connections/new",
    );
    expect(within(guide).getAllByTestId("setup-step")).toHaveLength(3);
    expect(screen.queryByTestId("tile-job-health")).toBeNull();
    expect(screen.queryByLabelText("Summary")).toBeNull();
  });

  it("tells maintainers an administrator adds connections", async () => {
    renderApp(
      "/",
      handlers(
        { DashboardJobs: () => jobs([]), DashboardInfra: () => infra([], []) },
        "MAINTAINER",
      ),
    );
    const guide = await screen.findByTestId("getting-started");
    expect(within(guide).queryByRole("link", { name: /Add a connection/ })).toBeNull();
    expect(within(guide).getAllByText("An administrator adds these.")).toHaveLength(2);
    expect(within(guide).getByRole("link", { name: /Create a job/ })).toBeInTheDocument();
  });

  it("tells a backup owner without jobs what to expect", async () => {
    renderApp("/", handlers({ DashboardJobs: () => jobs([]) }, "USER"));
    expect(await screen.findByText("No jobs assigned to you yet")).toBeInTheDocument();
  });
});
