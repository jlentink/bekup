import { screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { linkedJob } from "@/features/notifications/model";
import { baseHandlers, gqlError, meFixture, ok, renderApp, type Handler } from "@/test/mock-api";

import { personalOverridesPath } from "./notifications-panel-utils";
import { JOB_ID, jobDetail } from "./test-fixtures";

const OPS_ID = "0b6c3f7e-0000-4000-8000-0000000000c1";
const TEAM_ID = "0b6c3f7e-0000-4000-8000-0000000000c2";

const catalog = () =>
  ok({
    notificationCatalog: [
      {
        __typename: "NotificationEventGroup",
        group: "BACKUP",
        label: "Backup",
        events: [
          {
            __typename: "NotificationEventInfo",
            type: "backup.failed",
            label: "Backup failed",
            group: "BACKUP",
            mandatory: false,
            perJob: true,
            severity: "ERROR",
          },
          {
            __typename: "NotificationEventInfo",
            type: "backup.succeeded",
            label: "Backup succeeded",
            group: "BACKUP",
            mandatory: false,
            perJob: true,
            severity: "INFO",
          },
        ],
      },
      {
        __typename: "NotificationEventGroup",
        group: "SECURITY",
        label: "Security",
        events: [
          {
            __typename: "NotificationEventInfo",
            type: "security.system",
            label: "Security alert",
            group: "SECURITY",
            mandatory: true,
            perJob: false,
            severity: "CRITICAL",
          },
        ],
      },
    ],
  });

function channel(id: string, name: string, attachments: Record<string, unknown>[] = []) {
  return {
    __typename: "SharedChannel",
    id,
    name,
    type: id === OPS_ID ? "WEBHOOK" : "EMAIL_LIST",
    addresses: [],
    displayUrl: null,
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
    attachments,
    createdAt: "2026-10-27T10:00:00Z",
    updatedAt: "2026-10-27T10:00:00Z",
  };
}

const attachment = (events: string[]) => ({
  __typename: "SharedChannelAttachment",
  jobId: JOB_ID,
  jobName: "Billing DB",
  groups: [],
  events,
});

function editorHandlers(role: "ADMIN" | "MAINTAINER", extra: Record<string, Handler> = {}) {
  return {
    ...baseHandlers(meFixture({}, role)),
    JobDetail: () => ok({ job: jobDetail() }),
    JobEditorOptions: () => ok({ connections: [], destinations: [] }),
    TestRestoreTargets: () => ok({ connections: [] }),
    PreviewSchedule: () => gqlError("VALIDATION", "x"),
    UserPickerCandidates: () =>
      ok({ userDirectory: { __typename: "UserDirectoryConnection", edges: [] } }),
    ...extra,
  };
}

function jobAttachment(id: string, name: string, over: Record<string, unknown> = {}) {
  const webhook = id === OPS_ID;
  return {
    __typename: "JobSharedChannelAttachment",
    channelId: id,
    name,
    type: webhook ? "WEBHOOK" : "EMAIL_LIST",
    displayUrl: webhook ? "https://hooks.example.com:443" : null,
    enabled: true,
    groups: [],
    events: [],
    ...over,
  };
}

const attachmentsOf = (list: Record<string, unknown>[] | null) =>
  ok({ job: { __typename: "Job", id: JOB_ID, sharedChannelAttachments: list } });

async function notificationsSection() {
  const heading = await screen.findByRole("heading", { name: "Notifications" });
  const section = heading.closest("section");
  if (!section) throw new Error("no notifications section");
  return section;
}

describe("job editor notifications", () => {
  it("lets admins see, attach, re-filter and detach shared channels", async () => {
    let attached: Record<string, unknown>[] = [
      jobAttachment(OPS_ID, "Ops chat", { events: ["backup.failed"] }),
    ];
    const { user, callsOf } = renderApp(
      `/jobs/${JOB_ID}/edit`,
      editorHandlers("ADMIN", {
        JobSharedChannelAttachments: () => attachmentsOf(attached),
        AdminSharedChannels: () =>
          ok({
            sharedChannels: [
              channel(OPS_ID, "Ops chat", [attachment(["backup.failed"])]),
              channel(TEAM_ID, "DBA team"),
            ],
          }),
        AdminNotificationCatalog: catalog,
        AdminAttachSharedChannel: (v) => {
          attached = [
            ...attached,
            jobAttachment(TEAM_ID, "DBA team", { events: ["backup.succeeded"] }),
          ];
          return ok({
            attachSharedChannel:
              v["channelId"] === TEAM_ID
                ? channel(TEAM_ID, "DBA team", [attachment(["backup.succeeded"])])
                : channel(OPS_ID, "Ops chat", [attachment(["backup.succeeded"])]),
          });
        },
        AdminDetachSharedChannel: () => {
          attached = attached.filter((a) => a["channelId"] !== OPS_ID);
          return ok({ detachSharedChannel: channel(OPS_ID, "Ops chat") });
        },
      }),
    );
    const section = await notificationsSection();
    const list = await within(section).findByRole("list", {
      name: "Shared channels for this job",
    });
    expect(within(list).getByText("Ops chat")).toBeInTheDocument();
    expect(within(list).getByText("https://hooks.example.com:443")).toBeInTheDocument();
    expect(await within(list).findByText("Backup failed")).toBeInTheDocument();
    expect(within(list).queryByText("DBA team")).toBeNull();

    // Attach the other channel with an event filter; security events are never offered.
    await user.selectOptions(await within(section).findByLabelText("Shared channel"), TEAM_ID);
    await user.click(within(section).getByRole("button", { name: "Attach" }));
    const dlg = await screen.findByRole("dialog", { name: "Events from Billing DB" });
    expect(within(dlg).queryByText("Security alert")).toBeNull();
    await user.click(within(dlg).getByRole("checkbox", { name: /Backup succeeded/ }));
    await user.click(within(dlg).getByRole("button", { name: "Save filter" }));
    expect(await screen.findByText("Channel attached.")).toBeInTheDocument();
    expect(callsOf("AdminAttachSharedChannel")[0]?.variables).toEqual({
      jobId: JOB_ID,
      channelId: TEAM_ID,
      filter: { groups: [], events: ["backup.succeeded"] },
    });
    expect(await within(list).findByText("DBA team")).toBeInTheDocument();
    expect(within(list).getByText("E-mail list")).toBeInTheDocument();

    // Detach behind a confirmation.
    await user.click(within(section).getByRole("button", { name: "Detach Ops chat" }));
    const confirm = await screen.findByRole("dialog", { name: "Detach Ops chat from this job?" });
    await user.click(within(confirm).getByRole("button", { name: "Detach" }));
    expect(await screen.findByText("Channel detached.")).toBeInTheDocument();
    expect(callsOf("AdminDetachSharedChannel")[0]?.variables).toEqual({
      jobId: JOB_ID,
      channelId: OPS_ID,
    });
    await waitFor(() => {
      expect(within(list).queryByText("Ops chat")).toBeNull();
    });

    expect(
      within(section).getByRole("link", { name: "Your own notifications for this job" }),
    ).toHaveAttribute("href", personalOverridesPath(JOB_ID));
  });

  it("shows maintainers the attached channels read-only, never the admin-only channel list", async () => {
    const { callsOf } = renderApp(
      `/jobs/${JOB_ID}/edit`,
      editorHandlers("MAINTAINER", {
        JobSharedChannelAttachments: () =>
          attachmentsOf([
            jobAttachment(OPS_ID, "Ops chat", { groups: ["BACKUP"], enabled: false }),
            jobAttachment(TEAM_ID, "DBA team"),
          ]),
        AdminNotificationCatalog: catalog,
      }),
    );
    const section = await notificationsSection();
    const list = await within(section).findByRole("list", {
      name: "Shared channels for this job",
    });
    const rows = within(list).getAllByTestId("job-shared-channel");
    expect(rows).toHaveLength(2);
    const [ops, team] = rows as [HTMLElement, HTMLElement];
    expect(ops).toHaveTextContent("Ops chat");
    expect(ops).toHaveTextContent("https://hooks.example.com:443");
    expect(within(ops).getByText("disabled")).toBeInTheDocument();
    expect(await within(ops).findByText("Backup")).toBeInTheDocument();
    expect(team).toHaveTextContent("All job events");
    expect(within(team).queryByText("disabled")).toBeNull();
    expect(within(section).queryByRole("button", { name: /Detach|Change the events/ })).toBeNull();
    expect(within(section).queryByRole("button", { name: "Attach" })).toBeNull();
    expect(
      within(section).getByText(/Shared channels \(team e-mail lists and webhooks\)/),
    ).toBeInTheDocument();
    expect(
      within(section).getByRole("link", { name: "Your own notifications for this job" }),
    ).toBeInTheDocument();
    expect(callsOf("AdminSharedChannels")).toHaveLength(0);
  });

  it("shows no shared channel list when the API withholds it (backup owners)", async () => {
    const { callsOf } = renderApp(
      `/jobs/${JOB_ID}/edit`,
      editorHandlers("MAINTAINER", {
        JobSharedChannelAttachments: () => attachmentsOf(null),
        AdminNotificationCatalog: catalog,
      }),
    );
    const section = await notificationsSection();
    await waitFor(() => {
      expect(callsOf("JobSharedChannelAttachments").length).toBeGreaterThan(0);
      expect(section.querySelector('[data-slot="skeleton"]')).toBeNull();
    });
    expect(
      await within(section).findByRole("link", { name: "Your own notifications for this job" }),
    ).toBeInTheDocument();
    expect(within(section).queryByText("Shared channels for this job")).toBeNull();
    expect(within(section).queryByText(/No shared channel receives/)).toBeNull();
  });
});

describe("linkedJob", () => {
  const ID = "6f1c3e0a-0000-4000-8000-0000000000aa";
  it.each([
    ["", null, [], null],
    ["?job=not-a-uuid", null, [], null],
    [`?job=${ID}`, null, [], { jobId: ID, jobName: "this job" }],
    [`?job=${ID}`, { jobName: "  Billing  " }, [], { jobId: ID, jobName: "Billing" }],
    [`?job=${ID}`, { jobName: 42 }, [], { jobId: ID, jobName: "this job" }],
    [
      `?job=${ID}`,
      { jobName: "stale" },
      [{ jobId: ID, jobName: "Known" }],
      { jobId: ID, jobName: "Known" },
    ],
  ])("%s %j → %j", (search, state, existing, want) => {
    expect(linkedJob(search, state, existing, "this job")).toEqual(want);
  });
});
