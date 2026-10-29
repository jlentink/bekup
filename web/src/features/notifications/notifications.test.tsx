import { screen, waitFor, within } from "@testing-library/react";
import type { FormattedExecutionResult } from "graphql";
import { beforeAll, beforeEach, describe, expect, it } from "vitest";

import { stubResizeObserver } from "@/features/connections/test-fixtures";

import {
  baseHandlers,
  gqlError,
  meFixture,
  ok,
  serverInfoFixture,
  renderApp,
  type Handler,
} from "@/test/mock-api";

import {
  codeFromFragment,
  effectiveFor,
  personalTestOutcome,
  subjectPath,
  webhookUrlProblem,
  type PreferenceRow,
} from "./model";
import { resetPendingVerification } from "./pending-verification";

beforeAll(stubResizeObserver);

const N1 = "6f1c3e0a-0000-4000-8000-0000000000e1";
const N2 = "6f1c3e0a-0000-4000-8000-0000000000e2";
const JOB = "6f1c3e0a-0000-4000-8000-0000000000b1";
const RUN = "6f1c3e0a-0000-4000-8000-0000000000d1";
const HOOK = "6f1c3e0a-0000-4000-8000-0000000000f1";

function notification(id: string, over: Record<string, unknown> = {}) {
  return {
    __typename: "Notification",
    id,
    type: "backup.failed",
    group: "BACKUP",
    severity: "ERROR",
    title: "Backup failed: Billing DB",
    body: 'The backup of job "Billing DB" failed.',
    link: `https://bekup.example.com/runs/${RUN}`,
    createdAt: "2026-10-28T03:00:00Z",
    readAt: null,
    ...over,
  };
}

function connection(nodes: ReturnType<typeof notification>[], hasNextPage = false) {
  return {
    __typename: "NotificationConnection",
    edges: nodes.map((node) => ({ __typename: "NotificationEdge", cursor: node.id, node })),
    pageInfo: { __typename: "PageInfo", hasNextPage, endCursor: hasNextPage ? "c1" : null },
  };
}

function deferred() {
  let resolve!: (r: FormattedExecutionResult) => void;
  const promise = new Promise<FormattedExecutionResult>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

describe("notification model", () => {
  it("only links to known in-app pages, by path", () => {
    expect(subjectPath(`https://bekup.example.com/runs/${RUN}`)).toBe(`/runs/${RUN}`);
    expect(subjectPath("https://other.example/jobs")).toBe("/jobs");
    expect(subjectPath("https://bekup.example.com/profile/security")).toBe("/profile/security");
    expect(subjectPath("javascript:alert(1)")).toBeNull();
    expect(subjectPath("https://evil.example/runs/x/../../logout")).toBeNull();
    expect(subjectPath("https://bekup.example.com/api/download/abc")).toBeNull();
    expect(subjectPath(null)).toBeNull();
  });

  it("reads the code from the fragment only", () => {
    expect(codeFromFragment("#code=abcdefghijklmnopqrstuvwx")).toBe("abcdefghijklmnopqrstuvwx");
    expect(codeFromFragment("#code=short")).toBeNull();
    expect(codeFromFragment("#code=<script>alert(1)</script>")).toBeNull();
    expect(codeFromFragment("")).toBeNull();
  });

  it("checks webhook URLs like the server's rules", () => {
    expect(webhookUrlProblem("")).toBe("empty");
    expect(webhookUrlProblem("hooks.example.com")).toBe("invalid");
    expect(webhookUrlProblem("http://hooks.example.com/x")).toBe("https");
    expect(webhookUrlProblem("http://hooks.example.com/x", true)).toBeNull();
    expect(webhookUrlProblem("https://u:p@hooks.example.com/x")).toBe("userinfo");
    expect(webhookUrlProblem("https://hooks.example.com/x#y")).toBe("fragment");
    expect(webhookUrlProblem(`https://h.example/${"a".repeat(2050)}`)).toBe("length");
    expect(webhookUrlProblem("https://hooks.example.com/services/T0/B0/xyz")).toBeNull();
  });

  it("lets a job override win except on locked cells", () => {
    const row = prefRow("backup.failed", "Backup failed", "BACKUP", {
      jobOverrides: [{ jobId: JOB, jobName: "Billing DB", channel: "EMAIL", enabled: false }],
    });
    expect(effectiveFor(row, "EMAIL")).toBe(true);
    expect(effectiveFor(row, "EMAIL", JOB)).toBe(false);
    expect(effectiveFor(row, "INBOX", JOB)).toBe(true);
  });
});

/* ---- Bell and inbox ---- */

function inboxHandlers(extra: Record<string, Handler> = {}): Record<string, Handler> {
  return {
    ...baseHandlers(),
    NotificationBell: () =>
      ok({ unreadNotificationCount: 1, notifications: connection([notification(N1)]) }),
    NotificationsInbox: () =>
      ok({ unreadNotificationCount: 1, notifications: connection([notification(N1)]) }),
    ...extra,
  };
}

describe("notification bell", () => {
  it("raises the badge live when a notification arrives", async () => {
    const stream = deferred();
    const fresh = notification(N2, {
      type: "restore.failed",
      title: "Restore failed",
      body: "A restore failed.",
      severity: "CRITICAL",
      link: null,
    });
    let arrived = false;
    const server = () =>
      ok({
        unreadNotificationCount: arrived ? 2 : 1,
        notifications: connection(arrived ? [fresh, notification(N1)] : [notification(N1)]),
      });
    const { callsOf, user } = renderApp(
      "/notifications",
      inboxHandlers({
        AccountEvents: () => stream.promise,
        NotificationBell: server,
        NotificationsInbox: server,
      }),
    );
    const bell = await screen.findByRole("button", { name: "Notifications, 1 unread" });
    expect(within(bell).getByTestId("bell-badge")).toHaveTextContent("1");
    const inboxLoads = callsOf("NotificationsInbox").length;

    arrived = true;
    stream.resolve(
      ok({ accountEvents: { __typename: "AccountEvent", session: null, notification: fresh } }),
    );

    await screen.findByRole("button", { name: "Notifications, 2 unread" });
    // The inbox on screen reloads, the dropdown lists the new entry first.
    await waitFor(() => {
      expect(callsOf("NotificationsInbox").length).toBeGreaterThan(inboxLoads);
    });
    await user.click(screen.getByRole("button", { name: "Notifications, 2 unread" }));
    const items = await screen.findAllByTestId("bell-item");
    expect(items[0]).toHaveTextContent("Restore failed");
    expect(items[1]).toHaveTextContent("Backup failed: Billing DB");
  });

  it("marks everything read from the dropdown", async () => {
    let marked = false;
    const state = () =>
      ok({
        unreadNotificationCount: marked ? 0 : 1,
        notifications: connection([
          notification(N1, marked ? { readAt: "2026-10-28T04:00:00Z" } : {}),
        ]),
      });
    const { callsOf, user } = renderApp(
      "/notifications",
      inboxHandlers({
        MarkNotificationsRead: () => {
          marked = true;
          return ok({
            markNotificationsRead: {
              __typename: "MarkNotificationsReadResult",
              marked: 1,
              unreadCount: 0,
            },
          });
        },
        NotificationBell: () => state(),
        NotificationsInbox: () => state(),
      }),
    );
    await user.click(await screen.findByRole("button", { name: "Notifications, 1 unread" }));
    await user.click(await screen.findByRole("menuitem", { name: /Mark all read/ }));
    await screen.findByRole("button", { name: "Notifications" });
    expect(callsOf("MarkNotificationsRead")[0]?.variables).toEqual({ all: true });
  });
});

describe("inbox page", () => {
  it("marks one notification read and links to its subject", async () => {
    const { callsOf, user } = renderApp(
      "/notifications",
      inboxHandlers({
        MarkNotificationsRead: () =>
          ok({
            markNotificationsRead: {
              __typename: "MarkNotificationsReadResult",
              marked: 1,
              unreadCount: 0,
            },
          }),
      }),
    );
    const row = await screen.findByTestId("inbox-row");
    expect(row).toHaveAttribute("data-unread", "true");
    expect(within(row).getByRole("link", { name: /Open the run/ })).toHaveAttribute(
      "href",
      `/runs/${RUN}`,
    );
    await user.click(
      within(row).getByRole("button", { name: 'Mark "Backup failed: Billing DB" read' }),
    );
    await waitFor(() => {
      expect(screen.getByTestId("inbox-row")).toHaveAttribute("data-unread", "false");
    });
    expect(callsOf("MarkNotificationsRead")[0]?.variables).toEqual({ ids: [N1] });
    expect(await screen.findByRole("button", { name: "Notifications" })).toBeInTheDocument();
    expect(screen.getByText("0 unread")).toBeInTheDocument();
  });

  it("filters unread and deletes", async () => {
    const { callsOf, user } = renderApp(
      "/notifications",
      inboxHandlers({ DeleteNotification: () => ok({ deleteNotification: true }) }),
    );
    await screen.findByTestId("inbox-row");
    await user.click(screen.getByRole("radio", { name: "Unread" }));
    await waitFor(() => {
      expect(callsOf("NotificationsInbox").some((c) => c.variables["unreadOnly"] === true)).toBe(
        true,
      );
    });
    await user.click(
      await screen.findByRole("button", { name: 'Delete "Backup failed: Billing DB"' }),
    );
    await waitFor(() => {
      expect(callsOf("DeleteNotification")[0]?.variables).toEqual({ id: N1 });
    });
  });
});

/* ---- Preferences ---- */

type Channel = "INBOX" | "EMAIL" | "WEBHOOK";

function prefRow(
  type: string,
  label: string,
  group: string,
  opts: {
    mandatory?: boolean;
    cells?: Partial<Record<Channel, { enabled: boolean; source?: string }>>;
    jobOverrides?: { jobId: string; jobName: string; channel: Channel; enabled: boolean }[];
  } = {},
): PreferenceRow {
  const mandatory = opts.mandatory ?? false;
  const defaults: Record<Channel, boolean> = {
    INBOX: true,
    EMAIL: mandatory || type.endsWith("failed"),
    WEBHOOK: false,
  };
  return {
    __typename: "NotificationEventPreference",
    event: {
      __typename: "NotificationEventInfo",
      type,
      label,
      group: group as PreferenceRow["event"]["group"],
      problem: type.endsWith("failed"),
      mandatory,
      priority: "NORMAL",
      severity: mandatory ? "CRITICAL" : "ERROR",
      perJob: group !== "SECURITY",
    },
    channels: (["INBOX", "EMAIL", "WEBHOOK"] as const).map((channel) => {
      const c = opts.cells?.[channel];
      const locked = mandatory && channel !== "WEBHOOK";
      return {
        __typename: "NotificationChannelPreference",
        channel,
        enabled: c?.enabled ?? defaults[channel],
        source: (locked ? "MANDATORY" : (c?.source ?? "DEFAULT")) as "DEFAULT",
        locked,
      };
    }),
    jobOverrides: (opts.jobOverrides ?? []).map((o) => ({
      __typename: "NotificationJobOverride",
      ...o,
    })),
  } as PreferenceRow;
}

function prefsFixture(rows: { backupFailed?: PreferenceRow } = {}) {
  return {
    __typename: "NotificationPreferences",
    emailVerified: false,
    activeWebhooks: 0,
    groups: [
      {
        __typename: "NotificationPreferenceGroup",
        group: "BACKUP",
        label: "Backups",
        events: [
          rows.backupFailed ?? prefRow("backup.failed", "Backup failed", "BACKUP"),
          prefRow("backup.succeeded", "Backup succeeded", "BACKUP"),
        ],
      },
      {
        __typename: "NotificationPreferenceGroup",
        group: "SECURITY",
        label: "Security",
        events: [prefRow("security.account", "Security alert", "SECURITY", { mandatory: true })],
      },
    ],
  };
}

function channelsFixture(webhooks: Record<string, unknown>[] = []) {
  return {
    myNotificationChannels: {
      __typename: "MyNotificationChannels",
      email: "ada@example.com",
      emailVerified: false,
      emailVerifiedAt: null,
      maxWebhooks: 5,
      webhooks,
    },
  };
}

function webhookFixture(over: Record<string, unknown> = {}) {
  return {
    __typename: "PersonalWebhook",
    id: HOOK,
    label: "Team Slack",
    displayUrl: "https://hooks.slack.com/…",
    createdAt: "2026-10-01T10:00:00Z",
    state: {
      __typename: "NotificationChannelState",
      enabled: true,
      disabledAt: null,
      disabledReason: null,
      disabledMessage: null,
      consecutiveFailures: 0,
      permanentFailures: 0,
      lastFailureAt: null,
      lastFailureCode: null,
    },
    ...over,
  };
}

function settingsHandlers(extra: Record<string, Handler> = {}): Record<string, Handler> {
  return {
    ...baseHandlers(),
    ServerInfo: () => ok(serverInfoFixture(false)),
    MyNotificationChannels: () => ok(channelsFixture()),
    MyNotificationPreferences: () => ok({ myNotificationPreferences: prefsFixture() }),
    NotificationJobOptions: () =>
      ok({
        jobs: {
          __typename: "JobConnection",
          edges: [
            { __typename: "JobEdge", node: { __typename: "Job", id: JOB, name: "Billing DB" } },
          ],
        },
      }),
    ...extra,
  };
}

describe("preference matrix", () => {
  it("toggles a cell and shows locked security rows", async () => {
    const { callsOf, user } = renderApp(
      "/profile/notifications",
      settingsHandlers({
        SetNotificationPreference: () =>
          ok({
            setNotificationPreference: prefRow("backup.failed", "Backup failed", "BACKUP", {
              cells: { EMAIL: { enabled: false, source: "OVERRIDE" } },
            }),
          }),
      }),
    );
    const email = await screen.findByRole("switch", { name: "Backup failed: E-mail" });
    expect(email).toBeChecked();
    await user.click(email);
    await waitFor(() => {
      expect(screen.getByRole("switch", { name: "Backup failed: E-mail" })).not.toBeChecked();
    });
    expect(callsOf("SetNotificationPreference")[0]?.variables).toEqual({
      event: "backup.failed",
      channel: "EMAIL",
      enabled: false,
      jobId: null,
    });

    const locked = screen.getByRole("switch", { name: "Security alert: E-mail" });
    expect(locked).toBeChecked();
    expect(locked).toBeDisabled();
    expect(screen.getByRole("switch", { name: "Security alert: Webhooks" })).toBeEnabled();
    expect(screen.getByText(/always reach your inbox and e-mail so an attack/)).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "Backup failed: Push" })).toBeDisabled();
  });

  it("sets a per-job override", async () => {
    const { callsOf, user } = renderApp(
      "/profile/notifications",
      settingsHandlers({
        SetNotificationPreference: () =>
          ok({
            setNotificationPreference: prefRow("backup.failed", "Backup failed", "BACKUP", {
              jobOverrides: [
                { jobId: JOB, jobName: "Billing DB", channel: "WEBHOOK", enabled: true },
              ],
            }),
          }),
      }),
    );
    const picker = await screen.findByLabelText("Choose a job");
    await waitFor(() => {
      expect(within(picker).getByRole("option", { name: "Billing DB" })).toBeInTheDocument();
    });
    await user.selectOptions(picker, JOB);
    await user.click(screen.getByRole("button", { name: "Edit overrides" }));
    const region = await screen.findByRole("region", { name: "Overrides for Billing DB" });
    // Security events are not per job.
    expect(within(region).queryByText("Security alert")).not.toBeInTheDocument();
    const cell = within(region).getByRole("switch", {
      name: "Backup failed: Webhooks for Billing DB",
    });
    expect(cell).not.toBeChecked();
    await user.click(cell);
    await waitFor(() => {
      expect(
        within(region).getByRole("switch", { name: "Backup failed: Webhooks for Billing DB" }),
      ).toBeChecked();
    });
    expect(callsOf("SetNotificationPreference")[0]?.variables).toEqual({
      event: "backup.failed",
      channel: "WEBHOOK",
      enabled: true,
      jobId: JOB,
    });
    // The per-event cell is untouched; the job now has an override.
    expect(screen.getByRole("switch", { name: "Backup failed: Webhooks" })).not.toBeChecked();
    expect(screen.getByRole("button", { name: "Billing DB" })).toBeInTheDocument();
  });

  it("opens the override editor of the job linked from the job editor", async () => {
    renderApp(`/profile/notifications?job=${JOB}`, settingsHandlers());
    const region = await screen.findByRole("region", { name: "Overrides for this job" });
    expect(
      within(region).getByRole("switch", { name: "Backup failed: Webhooks for this job" }),
    ).toBeInTheDocument();
  });

  it("resets to the defaults after confirmation", async () => {
    const changed = prefRow("backup.failed", "Backup failed", "BACKUP", {
      cells: { EMAIL: { enabled: false, source: "OVERRIDE" } },
      jobOverrides: [{ jobId: JOB, jobName: "Billing DB", channel: "INBOX", enabled: false }],
    });
    const { callsOf, user } = renderApp(
      "/profile/notifications",
      settingsHandlers({
        MyNotificationPreferences: () =>
          ok({ myNotificationPreferences: prefsFixture({ backupFailed: changed }) }),
        ResetNotificationPreferences: () => ok({ resetNotificationPreferences: prefsFixture() }),
      }),
    );
    expect(await screen.findByRole("switch", { name: "Backup failed: E-mail" })).not.toBeChecked();
    // Per job: remove the overrides of Billing DB.
    await user.click(screen.getByRole("button", { name: "Billing DB" }));
    await user.click(screen.getByRole("button", { name: "Remove overrides" }));
    let dialog = await screen.findByRole("dialog", { name: "Remove the overrides of Billing DB?" });
    await user.click(within(dialog).getByRole("button", { name: "Remove overrides" }));
    await waitFor(() => {
      expect(callsOf("ResetNotificationPreferences")[0]?.variables).toEqual({ jobId: JOB });
    });

    await user.click(screen.getByRole("button", { name: "Reset to defaults" }));
    dialog = await screen.findByRole("dialog", { name: "Reset all notification preferences?" });
    expect(callsOf("ResetNotificationPreferences")).toHaveLength(1);
    await user.click(within(dialog).getByRole("button", { name: "Reset to defaults" }));
    await waitFor(() => {
      expect(callsOf("ResetNotificationPreferences")[1]?.variables).toEqual({ jobId: null });
    });
    expect(await screen.findByRole("switch", { name: "Backup failed: E-mail" })).toBeChecked();
  });
});

/* ---- Personal channels ---- */

describe("personal webhooks", () => {
  const KEY = "bkwh_9Qx3kLm2Vt7Rz1Np5Hs8Fd4Gc6Bj0Aw";

  it("validates the URL, shows the signing key once and never caches it", async () => {
    let added = false;
    const { callsOf, client, user } = renderApp(
      "/profile/notifications",
      settingsHandlers({
        MyNotificationChannels: () => ok(channelsFixture(added ? [webhookFixture()] : [])),
        AddMyWebhook: () => {
          added = true;
          return ok({
            addMyWebhook: {
              __typename: "PersonalWebhookWithKey",
              webhook: {
                __typename: "PersonalWebhook",
                id: HOOK,
                label: "Team Slack",
                displayUrl: "https://hooks.slack.com/…",
              },
              signingKey: KEY,
            },
          });
        },
      }),
    );
    await user.click(await screen.findByRole("button", { name: "Add a webhook" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a webhook" });
    await user.type(within(dialog).getByLabelText("Label"), "Team Slack");
    const url = within(dialog).getByLabelText("URL");
    await user.type(url, "http://hooks.slack.com/services/x");
    await user.click(within(dialog).getByRole("button", { name: "Add webhook" }));
    expect(
      await within(dialog).findByText("Webhook URLs must start with https://."),
    ).toBeInTheDocument();
    expect(callsOf("AddMyWebhook")).toHaveLength(0);

    await user.clear(url);
    await user.type(url, "https://hooks.slack.com/services/x");
    await user.click(within(dialog).getByRole("button", { name: "Add webhook" }));

    const keyDialog = await screen.findByRole("dialog", { name: "Team Slack is ready" });
    expect(within(keyDialog).getByTestId("signing-key")).toHaveTextContent(KEY);
    expect(
      within(keyDialog).getByRole("link", { name: "How to verify the signature" }),
    ).toHaveAttribute("href", expect.stringContaining("#verifying-the-signature"));
    expect(JSON.stringify(client.cache.extract())).not.toContain(KEY);

    const done = within(keyDialog).getByRole("button", { name: "Done" });
    expect(done).toBeDisabled();
    await user.click(within(keyDialog).getByLabelText("I have stored the key"));
    await user.click(done);
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
    expect(screen.queryByText(KEY)).not.toBeInTheDocument();
    expect(await screen.findByTestId("webhook-row")).toHaveTextContent("Team Slack");
    expect(document.body.textContent).not.toContain(KEY);
  });

  it("tests, and re-enables an auto-disabled webhook", async () => {
    const disabled = webhookFixture({
      state: {
        ...webhookFixture().state,
        enabled: false,
        disabledAt: "2026-10-27T10:00:00Z",
        disabledReason: "permanent_failures",
        disabledMessage: "Disabled after 5 permanent failures.",
        permanentFailures: 5,
        lastFailureAt: "2026-10-27T10:00:00Z",
        lastFailureCode: "http_status",
      },
    });
    const { callsOf, user } = renderApp(
      "/profile/notifications",
      settingsHandlers({
        MyNotificationChannels: () => ok(channelsFixture([disabled])),
        TestMyWebhook: () =>
          ok({
            testMyWebhook: {
              __typename: "NotificationTestResult",
              ok: false,
              code: "failed",
            },
          }),
        ReenableMyWebhook: () => ok({ reenableMyWebhook: webhookFixture() }),
      }),
    );
    const row = await screen.findByTestId("webhook-row");
    expect(within(row).getByText("Disabled after 5 permanent failures.")).toBeInTheDocument();
    await user.click(within(row).getByRole("button", { name: "Send test" }));
    const result = await within(row).findByTestId("webhook-test-result");
    expect(result).toHaveTextContent("Delivery failed");
    expect(result).toHaveAttribute("data-outcome", "failed");
    expect(result).not.toHaveTextContent(/HTTP/);
    await user.click(within(row).getByRole("button", { name: "Enable again" }));
    await waitFor(() => {
      expect(callsOf("ReenableMyWebhook")[0]?.variables).toEqual({ id: HOOK });
    });
  });
});

describe("personal webhook test outcomes", () => {
  it.each([
    [{ ok: true, code: null }, "ok"],
    [{ ok: true, code: "ok" }, "ok"],
    [{ ok: false, code: "not_allowed" }, "not_allowed"],
    [{ ok: false, code: "failed" }, "failed"],
    [{ ok: false, code: "http_status" }, "failed"],
    [{ ok: false, code: null }, "failed"],
  ])("%j → %s", (result, want) => {
    expect(personalTestOutcome(result)).toBe(want);
  });

  it.each([
    ["ok", true, "Delivered", /accepted the test/],
    ["not_allowed", false, "Address not allowed", /Ask an admin/],
    ["failed", false, "Delivery failed", /Check the URL/],
  ])("shows %s with its help text", async (code, okFlag, title, help) => {
    const { user } = renderApp(
      "/profile/notifications",
      settingsHandlers({
        MyNotificationChannels: () => ok(channelsFixture([webhookFixture()])),
        TestMyWebhook: () =>
          ok({
            testMyWebhook: { __typename: "NotificationTestResult", ok: okFlag, code },
          }),
      }),
    );
    const row = await screen.findByTestId("webhook-row");
    await user.click(within(row).getByRole("button", { name: "Send test" }));
    const result = await within(row).findByTestId("webhook-test-result");
    expect(result).toHaveAttribute("data-outcome", code);
    expect(result).toHaveTextContent(title);
    expect(result).toHaveTextContent(help);
  });
});

/* ---- /verify-email ---- */

describe("verify-email page", () => {
  const CODE = "Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MGFiY2RlZg";

  beforeEach(() => {
    resetPendingVerification();
  });

  it("verifies with the fragment code, clears it from history and never puts it in a URL", async () => {
    const { callsOf, calls, router } = renderApp(`/verify-email#code=${CODE}`, {
      ...baseHandlers(),
      VerifyEmail: () =>
        ok({
          verifyEmail: {
            __typename: "User",
            id: "6f1c3e0a-0000-4000-8000-000000000001",
            emailVerified: true,
            emailVerifiedAt: "2026-10-28T10:00:00Z",
          },
        }),
    });
    expect(await screen.findByRole("heading", { name: "Address verified" })).toBeInTheDocument();
    expect(router.state.location.hash).toBe("");
    expect(router.state.location.search).toBe("");
    expect(router.state.historyAction).toBe("REPLACE");
    expect(callsOf("VerifyEmail")).toHaveLength(1);
    expect(callsOf("VerifyEmail")[0]?.variables).toEqual({ code: CODE });
    // Only the mutation's body carries the code.
    for (const c of calls.filter((x) => x.name !== "VerifyEmail")) {
      expect(JSON.stringify(c.variables)).not.toContain(CODE);
    }
    expect(window.location.href).not.toContain(CODE);
  });

  it("explains a used or expired code", async () => {
    renderApp(`/verify-email#code=${CODE}`, {
      ...baseHandlers(),
      VerifyEmail: () => gqlError("VALIDATION", "invalid or expired verification code"),
    });
    expect(
      await screen.findByRole("heading", { name: "This link does not work" }),
    ).toBeInTheDocument();
    expect(screen.getByText(/expired \(links last 24 hours\)/)).toBeInTheDocument();
  });

  it("asks a signed-out visitor to sign in, keeping the code out of every URL", async () => {
    let signedIn = false;
    const { router, callsOf, user } = renderApp(`/verify-email#code=${CODE}`, {
      ...baseHandlers(),
      Me: () =>
        signedIn ? ok(meFixture()) : gqlError("UNAUTHENTICATED", "authentication required"),
      VerifyEmail: () =>
        ok({
          verifyEmail: {
            __typename: "User",
            id: "6f1c3e0a-0000-4000-8000-000000000001",
            emailVerified: true,
            emailVerifiedAt: "2026-10-28T10:00:00Z",
          },
        }),
    });
    expect(await screen.findByRole("heading", { name: "Sign in to verify" })).toBeInTheDocument();
    expect(router.state.location.hash).toBe("");
    const link = screen.getByRole("link", { name: "Sign in and continue" });
    expect(link).toHaveAttribute("href", `/login?next=${encodeURIComponent("/verify-email")}`);
    await user.click(link);
    await waitFor(() => {
      expect(router.state.location.pathname).toBe("/login");
    });
    expect(router.state.location.search + router.state.location.hash).not.toContain(CODE);
    expect(callsOf("VerifyEmail")).toHaveLength(0);

    // The sign-in returns to /verify-email (no fragment): the code waited in memory.
    signedIn = true;
    await router.navigate("/verify-email");
    expect(await screen.findByRole("heading", { name: "Address verified" })).toBeInTheDocument();
    expect(callsOf("VerifyEmail")[0]?.variables).toEqual({ code: CODE });
  });

  it("asks for a new link when there is no code", async () => {
    renderApp("/verify-email", baseHandlers());
    expect(
      await screen.findByRole("heading", { name: "No verification code" }),
    ).toBeInTheDocument();
  });
});
