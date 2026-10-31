import { screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { baseHandlers, gqlError, type Handler, meFixture, ok, renderApp } from "@/test/mock-api";

import {
  complete,
  expected,
  groupKeys,
  originOf,
  POLL,
  splitByListening,
  summarize,
  verdict,
  weakened,
} from "./model";

const PATH = "/admin/settings/configuration";
const REQ_ID = "0b6c3f7e-0000-4000-8000-0000000000f1";
const SERVE_ID = "0b6c3f7e-0000-4000-8000-0000000000e1";
const WORKER_ID = "0b6c3f7e-0000-4000-8000-0000000000e2";

function result(over: Record<string, unknown> = {}) {
  return {
    __typename: "ConfigProcessResult",
    instanceId: SERVE_ID,
    kind: "SERVE",
    hostname: "serve-1a2b",
    version: "1.4.0",
    processStartedAt: "2026-10-28T08:00:00Z",
    finishedAt: "2026-10-28T08:00:01Z",
    requestId: null,
    trigger: null,
    listening: true,
    status: "LOADED",
    errorKind: null,
    appliedKeys: [],
    restartKeys: [],
    errorKeys: [],
    weakenedKeys: [],
    ...over,
  };
}

function request(results: unknown[], expectedProcesses = 2, listening = expectedProcesses) {
  return {
    __typename: "ConfigReloadRequest",
    id: REQ_ID,
    requestedAt: "2026-10-29T10:00:00Z",
    trigger: "UI",
    listeningProcesses: listening,
    expectedProcesses,
    results,
  };
}

function status(over: Record<string, unknown> = {}) {
  return ok({
    configStatus: {
      __typename: "ConfigStatus",
      database: {
        __typename: "DatabasePosture",
        mode: "FIELDS",
        tlsMode: "verify-full",
        insecureTransport: false,
        singleAccount: false,
      },
      liveKeys: ["logging.level", "workers.max_concurrent_runs", "retention.dry_run"],
      listeningProcesses: 3,
      processes: [
        result({
          status: "RESTART_REQUIRED",
          requestId: "0b6c3f7e-0000-4000-8000-0000000000aa",
          trigger: "UI",
          restartKeys: ["server.listen"],
          appliedKeys: ["logging.level"],
          weakenedKeys: ["security.session_idle_timeout"],
        }),
        result({ instanceId: WORKER_ID, kind: "WORKER", hostname: "worker-9f00" }),
      ],
      lastRequest: null,
      ...over,
    },
  });
}

/** Handlers with a step-up gate in front of AdminReloadConfig. */
function handlers(over: Record<string, Handler>): Record<string, Handler> {
  let stepped = false;
  const reload = over["AdminReloadConfig"];
  return {
    ...baseHandlers(),
    AdminConfigStatus: () => status(),
    BeginStepUp: () =>
      ok({
        beginStepUp: { __typename: "StepUpChallenge", methods: ["TOTP"], passkeyOptions: null },
      }),
    CompleteStepUp: () => {
      stepped = true;
      return ok({
        completeStepUp: {
          __typename: "StepUpResult",
          stepUpAt: "2026-10-29T10:00:00Z",
          aal2At: "2026-10-29T10:00:00Z",
          aal: 2,
        },
      });
    },
    ...over,
    ...(reload
      ? {
          AdminReloadConfig: (v) =>
            stepped
              ? reload(v)
              : gqlError("STEP_UP_REQUIRED", "please confirm your identity again"),
        }
      : {}),
  };
}

async function startReload(user: ReturnType<typeof renderApp>["user"]) {
  await user.click(await screen.findByRole("button", { name: "Reload configuration" }));
  const dialog = await screen.findByRole("dialog", { name: "Reload the configuration?" });
  expect(within(dialog).getByText(/invalid file is refused/)).toBeInTheDocument();
  expect(within(dialog).getByText(/alerts every admin/)).toBeInTheDocument();
  await user.click(within(dialog).getByRole("button", { name: "Reload" }));
  return dialog;
}

async function confirmStepUp(user: ReturnType<typeof renderApp>["user"]) {
  const stepUp = await screen.findByRole("dialog", { name: "Confirm your identity" });
  await user.type(within(stepUp).getByLabelText("6-digit code"), "654321");
  await user.click(within(stepUp).getByRole("button", { name: "Confirm" }));
}

const saved = { ...POLL };
beforeEach(() => {
  POLL.intervalMs = 20;
  POLL.timeoutMs = 2_000;
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
  Object.assign(POLL, saved);
  vi.unstubAllGlobals();
});

describe("config reload model", () => {
  it("summarizes outcomes, ignores startup rows and picks the worst verdict", () => {
    const s = summarize([
      { status: "APPLIED", listening: true },
      { status: "RESTART_REQUIRED", listening: true },
      { status: "LOADED", listening: true },
      { status: "UNCHANGED", listening: true },
    ]);
    expect(s).toEqual({
      applied: 1,
      restart: 1,
      unchanged: 1,
      rejected: 0,
      failed: 0,
      stopped: 0,
    });
    expect(verdict(s)).toBe("restart");
    expect(verdict({ ...s, failed: 1 })).toBe("problem");
    expect(verdict(summarize([{ status: "UNCHANGED", listening: true }]))).toBe("unchanged");
    expect(verdict(summarize([]))).toBe("none");
  });

  it("counts answers of processes that stopped since as history, never as a pending restart", () => {
    const s = summarize([
      { status: "RESTART_REQUIRED", listening: false },
      { status: "REJECTED", listening: false },
      { status: "APPLIED", listening: true },
    ]);
    expect(s).toMatchObject({ applied: 1, restart: 0, rejected: 0, stopped: 2 });
    expect(verdict(s)).toBe("applied");
    expect(verdict(summarize([{ status: "RESTART_REQUIRED", listening: false }]))).toBe("stopped");
    expect(
      splitByListening([
        { id: 1, listening: true },
        { id: 2, listening: false },
        { id: 3, listening: true },
      ]),
    ).toEqual({
      listening: [
        { id: 1, listening: true },
        { id: 3, listening: true },
      ],
      stopped: [{ id: 2, listening: false }],
    });
  });

  it("knows when a request is complete and how many answers it waits for", () => {
    expect(complete(1, 2)).toBe(false);
    expect(complete(2, 2)).toBe(true);
    expect(complete(0, 0)).toBe(true);
    expect(expected(3, 2)).toBe(3);
    expect(expected(1, 2)).toBe(2);
  });

  it("derives origins, weakened keys and key groups", () => {
    expect(originOf({ requestId: null })).toBe("startup");
    expect(originOf({ requestId: "a", trigger: "UI" }, "a")).toBe("latest");
    expect(originOf({ requestId: "b", trigger: "UI" }, "a")).toBe("ui");
    expect(originOf({ requestId: "c", trigger: "SIGNAL" }, "a")).toBe("signal");
    expect(weakened([{ weakenedKeys: ["b.x", "a.y"] }, { weakenedKeys: ["a.y"] }])).toEqual([
      "a.y",
      "b.x",
    ]);
    expect(groupKeys(["workers.b", "logging.level", "workers.a", "top", "a.b.c"])).toEqual([
      { section: "", keys: ["top"] },
      { section: "a.b", keys: ["a.b.c"] },
      { section: "logging", keys: ["logging.level"] },
      { section: "workers", keys: ["workers.a", "workers.b"] },
    ]);
  });
});

describe("configuration page", () => {
  it("lists processes with startup rows, restart, applied and weakened keys and the live keys", async () => {
    const { user } = renderApp(PATH, handlers({}));
    const rows = await screen.findAllByTestId("config-process");
    expect(rows).toHaveLength(2);
    const [serve, worker] = rows as [HTMLElement, HTMLElement];
    expect(within(serve).getByText("serve-1a2b")).toBeInTheDocument();
    expect(within(serve).getByText("Restart required")).toBeInTheDocument();
    expect(within(serve).getByRole("list", { name: "Restart needed" })).toHaveTextContent(
      "server.listen",
    );
    expect(within(serve).getByRole("list", { name: "Weakened" })).toHaveTextContent(
      "security.session_idle_timeout",
    );
    expect(within(serve).getByRole("list", { name: "Applied" })).toHaveTextContent("logging.level");
    expect(within(serve).getByText(/Earlier reload/)).toBeInTheDocument();
    expect(within(worker).getByText("Started")).toBeInTheDocument();
    expect(within(worker).getByText(/At startup/)).toBeInTheDocument();
    expect(within(worker).getByText("No changed keys")).toBeInTheDocument();
    expect(screen.getByTestId("config-process-counts")).toHaveTextContent(
      "2 listening · 0 stopped",
    );
    expect(screen.queryByTestId("config-stopped-processes")).not.toBeInTheDocument();
    expect(screen.getByText(/SIGHUP to a single process/)).toBeInTheDocument();
    expect(screen.queryByTestId("config-reload-panel")).not.toBeInTheDocument();

    await user.click(screen.getByText("Show the 3 live keys"));
    const live = screen.getByTestId("config-live-keys");
    expect(within(live).getByRole("list", { name: "workers" })).toHaveTextContent(
      "workers.max_concurrent_runs",
    );
  });

  it("groups stopped processes below, de-emphasised and without a restart call to action", async () => {
    renderApp(
      PATH,
      handlers({
        AdminConfigStatus: () =>
          status({
            processes: [
              result({ requestId: REQ_ID, trigger: "SIGNAL", status: "APPLIED" }),
              result({
                instanceId: WORKER_ID,
                kind: "WORKER",
                hostname: "worker-old",
                listening: false,
                requestId: "0b6c3f7e-0000-4000-8000-0000000000aa",
                trigger: "UI",
                status: "RESTART_REQUIRED",
                restartKeys: ["server.listen"],
              }),
            ],
          }),
      }),
    );
    const [live] = (await screen.findAllByTestId("config-process")) as [HTMLElement];
    expect(within(live).getByText(/SIGHUP/)).toBeInTheDocument();
    expect(screen.getByTestId("config-process-counts")).toHaveTextContent(
      "1 listening · 1 stopped",
    );
    const group = screen.getByTestId("config-stopped-processes");
    expect(within(group).getByText("Stopped or not listening")).toBeInTheDocument();
    const stale = within(group).getByTestId("config-process-stopped");
    expect(within(stale).getByText("worker-old")).toBeInTheDocument();
    expect(within(stale).getByText("Not listening")).toBeInTheDocument();
    expect(within(stale).getByText("Last result: Restart required")).toBeInTheDocument();
    expect(within(stale).queryByRole("list", { name: "Restart needed" })).not.toBeInTheDocument();
    expect(
      within(stale).getByRole("list", { name: "Changed, needed a restart" }),
    ).toHaveTextContent("server.listen");
    // The listening row comes first in the document.
    expect(live.compareDocumentPosition(stale) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("stops polling once every expected process answered, even with more listening", async () => {
    let polls = 0;
    const one = result({
      requestId: REQ_ID,
      trigger: "UI",
      status: "APPLIED",
      appliedKeys: ["log.level"],
    });
    const { user } = renderApp(
      PATH,
      handlers({
        // Three processes listen, but one started after the request.
        AdminReloadConfig: () => ok({ reloadConfig: request([], 1, 3) }),
        AdminConfigReloadRequest: () => {
          polls += 1;
          return ok({ configReloadRequest: request([one], 1, 3) });
        },
      }),
    );
    await startReload(user);
    await confirmStepUp(user);
    const progress = await screen.findByTestId("config-reload-progress");
    await waitFor(() => {
      expect(progress).toHaveTextContent("1 of 1 process answered");
    });
    await new Promise((r) => setTimeout(r, 80));
    expect(polls).toBe(1);
    expect(screen.queryByText(/Not every process answered/)).not.toBeInTheDocument();
  });

  it("asks for a step-up, then polls the request and shows progress and outcomes", async () => {
    let polls = 0;
    const serveApplied = result({
      requestId: REQ_ID,
      status: "APPLIED",
      appliedKeys: ["logging.level"],
      weakenedKeys: ["logging.level"],
      finishedAt: "2026-10-29T10:00:01Z",
    });
    const workerRestart = result({
      instanceId: WORKER_ID,
      kind: "WORKER",
      hostname: "worker-9f00",
      requestId: REQ_ID,
      status: "RESTART_REQUIRED",
      restartKeys: ["database.url"],
      finishedAt: "2026-10-29T10:00:02Z",
    });
    const { user, callsOf } = renderApp(
      PATH,
      handlers({
        AdminReloadConfig: () => ok({ reloadConfig: request([serveApplied]) }),
        AdminConfigReloadRequest: (v) => {
          expect(v["id"]).toBe(REQ_ID);
          polls += 1;
          return ok({
            configReloadRequest: request(
              polls < 2 ? [serveApplied] : [serveApplied, workerRestart],
            ),
          });
        },
      }),
    );
    await startReload(user);
    await confirmStepUp(user);

    const progress = await screen.findByTestId("config-reload-progress");
    expect(progress).toHaveAttribute("role", "status");
    await waitFor(() => {
      expect(progress).toHaveTextContent("2 of 2 processes answered");
    });
    expect(polls).toBe(2);
    const panel = screen.getByTestId("config-reload-panel");
    expect(within(panel).getByText("1 applied")).toBeInTheDocument();
    expect(within(panel).getByText("1 restart required")).toBeInTheDocument();
    expect(
      within(panel).getByText("Applied where possible; some changes need a restart"),
    ).toBeInTheDocument();
    expect(within(panel).getAllByTestId("config-reload-result")).toHaveLength(2);
    expect(within(panel).getByTestId("config-weakened")).toHaveTextContent("logging.level");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    // The status is refetched once the request finished.
    await waitFor(() => {
      expect(callsOf("AdminConfigStatus").length).toBeGreaterThanOrEqual(2);
    });
    // No further polling after completion.
    await new Promise((r) => setTimeout(r, 80));
    expect(polls).toBe(2);
  });

  it("shows a rejected file with its error kind and keys", async () => {
    const rejected = result({
      requestId: REQ_ID,
      status: "REJECTED",
      errorKind: "INVALID",
      errorKeys: ["server.unknown_key", "workers.max_concurrent_runs"],
    });
    const { user } = renderApp(
      PATH,
      handlers({
        AdminReloadConfig: () => ok({ reloadConfig: request([rejected], 1) }),
      }),
    );
    await startReload(user);
    await confirmStepUp(user);
    const panel = await screen.findByTestId("config-reload-panel");
    expect(within(panel).getByText("Rejected")).toBeInTheDocument();
    expect(within(panel).getByText("Unknown key or invalid value")).toBeInTheDocument();
    expect(within(panel).getByRole("list", { name: "Errors" })).toHaveTextContent(
      "server.unknown_key",
    );
    expect(
      within(panel).getByText("Some processes refused the file or could not apply it"),
    ).toBeInTheDocument();
    expect(within(panel).getByText("1 rejected")).toBeInTheDocument();
  });

  it("explains RATE_LIMITED inside the dialog", async () => {
    const { user } = renderApp(
      PATH,
      handlers({
        AdminReloadConfig: () => gqlError("RATE_LIMITED", "slow down"),
      }),
    );
    const dialog = await startReload(user);
    await confirmStepUp(user);
    expect(await within(dialog).findByTestId("config-reload-error")).toHaveTextContent(
      "A reload was just requested. Wait a few seconds and try again.",
    );
    expect(screen.queryByTestId("config-reload-panel")).not.toBeInTheDocument();
  });

  it("stops polling after the timeout and says so", async () => {
    POLL.timeoutMs = 60;
    const one = result({ requestId: REQ_ID, status: "UNCHANGED" });
    const { user } = renderApp(
      PATH,
      handlers({
        AdminReloadConfig: () => ok({ reloadConfig: request([one], 3) }),
        AdminConfigReloadRequest: () => ok({ configReloadRequest: request([one], 3) }),
      }),
    );
    await startReload(user);
    await confirmStepUp(user);
    expect(
      await screen.findByText(/Not every process answered within 30 seconds/),
    ).toBeInTheDocument();
    expect(screen.getByTestId("config-reload-progress")).toHaveTextContent(
      "1 of 3 processes answered",
    );
  });

  it("shows the latest reload request from the status", async () => {
    renderApp(
      PATH,
      handlers({
        AdminConfigStatus: () =>
          status({ lastRequest: request([result({ requestId: REQ_ID, status: "UNCHANGED" })], 1) }),
      }),
    );
    const panel = await screen.findByTestId("config-reload-panel");
    expect(within(panel).getByText("Latest reload")).toBeInTheDocument();
    expect(within(panel).getByText("Nothing changed")).toBeInTheDocument();
  });

  it("shows the app database posture and its weakenings", async () => {
    renderApp(
      PATH,
      handlers({
        AdminConfigStatus: () =>
          status({
            database: {
              __typename: "DatabasePosture",
              mode: "SINGLE_ACCOUNT",
              tlsMode: "require",
              insecureTransport: true,
              singleAccount: true,
            },
          }),
      }),
    );
    const section = await screen.findByTestId("config-database");
    expect(within(section).getByText("One account for everything")).toBeInTheDocument();
    expect(within(section).getByText("require")).toBeInTheDocument();
    expect(
      within(section).getByText("The database connection is not fully verified"),
    ).toBeInTheDocument();
    expect(within(section).getByText("Single-account mode is on")).toBeInTheDocument();
  });

  it("shows a verified two-role database without warnings", async () => {
    renderApp(PATH, handlers({}));
    const section = await screen.findByTestId("config-database");
    expect(
      within(section).getByText("Host and password files (external PostgreSQL)"),
    ).toBeInTheDocument();
    expect(within(section).queryByRole("alert")).toBeNull();
    expect(within(section).queryByText("Single-account mode is on")).toBeNull();
  });

  it("denies non-admins without querying", async () => {
    const { callsOf } = renderApp(PATH, baseHandlers(meFixture({}, "MAINTAINER")));
    expect(await screen.findByRole("heading", { name: "Admins only" })).toBeInTheDocument();
    expect(callsOf("AdminConfigStatus")).toHaveLength(0);
  });

  it("is reachable from the settings rail", async () => {
    const { user } = renderApp("/admin/settings/network", {
      ...handlers({}),
      AdminSsrfAllowlist: () => gqlError("INTERNAL", "x"),
    });
    await user.click(await screen.findByRole("link", { name: /Configuration/ }));
    expect(await screen.findByRole("heading", { name: "Configuration" })).toBeInTheDocument();
  });
});
