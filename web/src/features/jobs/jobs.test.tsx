import { screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { baseHandlers, gqlError, meFixture, ok, renderApp, type Vars } from "@/test/mock-api";

import { draftProblem, draftToInput, defaultDraft, draftFromSchedule } from "./schedule";
import {
  CONN_ID,
  DEST_ID,
  emptyRuns,
  JOB_ID,
  jobDetail,
  jobListNode,
  jobsList,
  noTestRestores,
} from "./test-fixtures";

const detailHandlers = (job = jobDetail()) => ({
  JobDetail: () => ok({ job }),
  RunsList: () => ok(emptyRuns),
  LatestTestRestore: () => ok(noTestRestores),
});

describe("schedule helpers", () => {
  it("sends only the fields of the kind", () => {
    const d = { ...defaultDraft(), timezone: "Europe/Amsterdam", anchorDate: "2026-11-01" };
    expect(draftToInput({ ...d, kind: "MANUAL" })).toEqual({ kind: "MANUAL" });
    expect(draftToInput({ ...d, kind: "CRON", cron: " 15  2 * * 1-5 " })).toEqual({
      kind: "CRON",
      cron: "15 2 * * 1-5",
      timezone: "Europe/Amsterdam",
    });
    expect(
      draftToInput({ ...d, every: "2", unit: "WEEKS", weekday: "monday", dayOfMonth: "5" }),
    ).toEqual({
      kind: "INTERVAL",
      every: 2,
      unit: "WEEKS",
      anchor: "2026-11-01T03:00",
      timezone: "Europe/Amsterdam",
      weekday: "monday",
    });
  });

  it("round-trips a stored schedule", () => {
    const s = {
      kind: "INTERVAL" as const,
      every: 3,
      unit: "MONTHS" as const,
      anchor: "2026-01-31T04:30",
      dayOfMonth: 31,
      timezone: "UTC",
    };
    expect(draftToInput(draftFromSchedule(s))).toEqual({ ...s });
  });

  it.each([
    [
      "MANUAL with null fields",
      {
        kind: "MANUAL",
        every: null,
        unit: null,
        anchor: null,
        weekday: null,
        dayOfMonth: null,
        cron: null,
        timezone: null,
      },
      { kind: "MANUAL" },
    ],
    [
      "CRON without an anchor",
      { kind: "CRON", every: null, unit: null, anchor: null, cron: "0 4 * * 0", timezone: "UTC" },
      { kind: "CRON", cron: "0 4 * * 0", timezone: "UTC" },
    ],
    [
      "INTERVAL weeks with a weekday",
      {
        kind: "INTERVAL",
        every: 1,
        unit: "WEEKS",
        anchor: "2026-11-01T04:00",
        weekday: "sunday",
        timezone: "UTC",
      },
      {
        kind: "INTERVAL",
        every: 1,
        unit: "WEEKS",
        anchor: "2026-11-01T04:00",
        weekday: "sunday",
        timezone: "UTC",
      },
    ],
  ] as const)("builds a draft for every kind: %s", (_, stored, input) => {
    expect(draftToInput(draftFromSchedule(stored))).toEqual(input);
  });

  it("falls back to the default draft for a missing or partial schedule", () => {
    const d = defaultDraft();
    expect(draftFromSchedule(undefined)).toEqual(d);
    expect(draftFromSchedule(null)).toEqual(d);
    const partial = draftFromSchedule({ anchor: "garbage" });
    expect(partial.kind).toBe(d.kind);
    expect(partial.anchorDate).toBe(d.anchorDate);
    expect(draftProblem(partial)).toBeNull();
  });

  it("checks drafts locally", () => {
    const d = defaultDraft();
    expect(draftProblem(d)).toBeNull();
    expect(draftProblem({ ...d, every: "0" })).toMatch(/1 or more/);
    expect(draftProblem({ ...d, anchorTime: "25:00" })).toMatch(/HH:MM/);
    expect(draftProblem({ ...d, kind: "CRON", cron: "* * *" })).toMatch(/5 fields/);
    expect(draftProblem({ ...d, kind: "MANUAL", every: "x" })).toBeNull();
  });
});

describe("jobs list", () => {
  it("shows staff columns, the hazard badge and the toggle with confirmation", async () => {
    let enabled = true;
    const { user, callsOf } = renderApp("/jobs", {
      ...baseHandlers(),
      JobFilterConnections: () => ok({ connections: [] }),
      JobsList: () =>
        ok(
          jobsList([
            jobListNode({ enabled }),
            jobListNode({
              id: "j2",
              name: "Legacy",
              slug: "legacy",
              encryption: {
                __typename: "JobEncryption",
                configured: true,
                mode: "NONE",
                optedOut: true,
              },
              lastRun: null,
            }),
          ]),
        ),
      DisableJob: (v: Vars) => {
        enabled = false;
        return ok({
          disableJob: { __typename: "Job", id: v["id"], enabled: false, nextRunAt: null },
        });
      },
    });
    const row = await screen.findByTestId("job-row-billing-db");
    expect(within(row).getByText("Every day at 03:00 (UTC)")).toBeInTheDocument();
    expect(within(row).getByText("Olive Owner")).toBeInTheDocument();
    expect(within(row).getByText("Succeeded")).toBeInTheDocument();
    const legacy = screen.getByTestId("job-row-legacy");
    expect(within(legacy).getByText("Unencrypted")).toBeInTheDocument();
    expect(within(legacy).getByText("Never run")).toBeInTheDocument();

    await user.click(within(row).getByRole("switch", { name: "Scheduled runs of Billing DB" }));
    const dialog = await screen.findByRole("dialog", { name: "Disable Billing DB?" });
    expect(callsOf("DisableJob")).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "Disable job" }));
    await waitFor(() => {
      expect(callsOf("DisableJob")).toHaveLength(1);
    });
    await waitFor(() => {
      expect(within(row).getByRole("switch")).toHaveAttribute("aria-checked", "false");
    });
  });

  it("passes search and state filters to the server", async () => {
    const { user, callsOf } = renderApp("/jobs", {
      ...baseHandlers(),
      JobFilterConnections: () => ok({ connections: [] }),
      JobsList: () => ok(jobsList([jobListNode()])),
    });
    await screen.findByTestId("job-row-billing-db");
    await user.type(screen.getByRole("searchbox", { name: "Search jobs" }), "bill");
    await user.selectOptions(screen.getByRole("combobox", { name: "State" }), "off");
    await waitFor(() => {
      const last = callsOf("JobsList").at(-1);
      expect(last?.variables["filter"]).toEqual({ search: "bill", enabled: false });
    });
  });

  it("hides owners, toggles and staff filters from backup owners", async () => {
    renderApp("/jobs", {
      ...baseHandlers(meFixture({}, "USER")),
      JobsList: () => ok(jobsList([jobListNode({ owners: null })])),
    });
    const row = await screen.findByTestId("job-row-billing-db");
    expect(within(row).queryByRole("switch")).toBeNull();
    expect(screen.queryByRole("columnheader", { name: "Owners" })).toBeNull();
    expect(screen.queryByRole("combobox", { name: "Connection" })).toBeNull();
    expect(screen.queryByRole("link", { name: /New job/ })).toBeNull();
  });
});

describe("job editor", () => {
  const editorHandlers = {
    JobEditorOptions: () =>
      ok({
        connections: [
          {
            __typename: "Connection",
            id: CONN_ID,
            name: "billing-pg",
            engine: "POSTGRES",
            versionKey: "17",
            temporary: false,
          },
          {
            __typename: "Connection",
            id: "tmp",
            name: "Temporary: x",
            engine: "POSTGRES",
            versionKey: "17",
            temporary: true,
          },
        ],
        destinations: [
          {
            __typename: "Destination",
            id: DEST_ID,
            name: "vault-s3",
            type: "S3",
            ready: true,
            notReadyReason: null,
          },
        ],
      }),
  };

  it("previews the schedule live and shows server refusals", async () => {
    const { user, callsOf } = renderApp("/jobs/new", {
      ...baseHandlers(),
      ...editorHandlers,
      PreviewSchedule: (v: Vars) => {
        const input = v["input"] as { kind: string; cron?: string };
        if (input.kind === "CRON" && input.cron === "61 * * * *") {
          return gqlError("VALIDATION", "minute 61 is out of range", "cron");
        }
        return ok({
          previewSchedule: {
            __typename: "SchedulePreview",
            description:
              input.kind === "CRON"
                ? "At minute 15 past every hour (UTC)"
                : "Every day at 03:00 (UTC)",
            next: [
              "2026-10-29T03:00:00Z",
              "2026-10-30T03:00:00Z",
              "2026-10-31T03:00:00Z",
              "2026-11-01T03:00:00Z",
              "2026-11-02T03:00:00Z",
            ],
            schedule: { __typename: "Schedule", kind: input.kind, timezone: "UTC" },
          },
        });
      },
    });
    const preview = await screen.findByRole("region", { name: "Next 5 runs" });
    await waitFor(() => {
      expect(within(preview).getByTestId("schedule-description")).toHaveTextContent(
        "Every day at 03:00",
      );
    });
    expect(within(preview).getAllByRole("listitem")).toHaveLength(5);
    expect(callsOf("PreviewSchedule")[0]?.variables["count"]).toBe(5);

    await user.click(screen.getByRole("radio", { name: /Cron/ }));
    const cron = screen.getByLabelText("Cron expression");
    await user.clear(cron);
    await user.type(cron, "61 * * * *");
    await waitFor(() => {
      expect(within(preview).getByRole("alert")).toHaveTextContent("Minute 61 is out of range.");
    });
    // The builder reports its validity to the form in an effect.
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Create job" })).toBeDisabled();
    });

    await user.click(screen.getByRole("radio", { name: /Manual/ }));
    expect(within(preview).getByText(/only runs when someone starts it/)).toBeInTheDocument();
  });

  it("offers only permanent connections and shows the deep-verification note", async () => {
    renderApp("/jobs/new", {
      ...baseHandlers(),
      ...editorHandlers,
      PreviewSchedule: () => gqlError("VALIDATION", "x"),
    });
    const select = await screen.findByRole("combobox", { name: "Connection" });
    await waitFor(() => {
      expect(within(select).getByRole("option", { name: /billing-pg/ })).toBeInTheDocument();
    });
    expect(within(select).queryByRole("option", { name: /Temporary/ })).toBeNull();
    expect(screen.getByText(/Deep only adds exactness for PostgreSQL/)).toBeInTheDocument();
  });

  it("creates a job and shows the generated passphrase once", async () => {
    const { user, callsOf } = renderApp("/jobs/new", {
      ...baseHandlers(meFixture({}, "MAINTAINER")),
      ...editorHandlers,
      PreviewSchedule: () =>
        ok({
          previewSchedule: {
            __typename: "SchedulePreview",
            description: "Every day at 03:00 (UTC)",
            next: ["2026-10-29T03:00:00Z"],
            schedule: { __typename: "Schedule", kind: "INTERVAL", timezone: "UTC" },
          },
        }),
      CreateJob: () =>
        ok({
          createJob: {
            __typename: "CreateJobResult",
            job: { __typename: "Job", id: JOB_ID, name: "Billing DB", slug: "billing-db" },
            generatedPassphrase: "correct-horse-battery-staple-xyz",
            generatedKeypair: null,
          },
        }),
      JobsList: () => ok(jobsList([])),
      ...detailHandlers(),
    });
    await user.type(await screen.findByLabelText("Name"), "Billing DB");
    const conn = screen.getByRole("combobox", { name: "Connection" });
    await waitFor(() => {
      expect(within(conn).getByRole("option", { name: /billing-pg/ })).toBeInTheDocument();
    });
    await user.selectOptions(conn, CONN_ID);
    await user.selectOptions(screen.getByRole("combobox", { name: "Add a destination" }), DEST_ID);
    // Maintainers get the default (generated passphrase) only.
    expect(screen.queryByRole("radiogroup", { name: "Key" })).toBeNull();
    await waitFor(() => {
      expect(screen.getByTestId("schedule-description")).toBeInTheDocument();
    });
    // New jobs default to "Select only" and need at least one database.
    expect(screen.getByRole("radio", { name: "Select only" })).toBeChecked();
    expect(screen.getByRole("radio", { name: "Exclude" })).not.toBeChecked();
    await user.click(screen.getByRole("button", { name: "Create job" }));
    expect(await screen.findByText("Select at least one database.")).toBeInTheDocument();
    expect(callsOf("CreateJob")).toHaveLength(0);
    await user.type(screen.getByRole("textbox", { name: "Add a database by name" }), "billing");
    await user.click(screen.getByRole("button", { name: "Add" }));
    await user.click(screen.getByRole("button", { name: "Create job" }));

    const dialog = await screen.findByRole("dialog", { name: "Save the new passphrase" });
    expect(within(dialog).getByText("correct-horse-battery-staple-xyz")).toBeInTheDocument();
    const input = callsOf("CreateJob")[0]?.variables["input"] as Record<string, unknown>;
    expect(input["encryption"]).toBeUndefined();
    expect(input["destinationIds"]).toEqual([DEST_ID]);
    expect(input["retention"]).toEqual({ keepLast: 7, keepDays: 30 });
    expect(input["dbMode"]).toBe("SELECTED");
    expect(input["databases"]).toEqual(["billing"]);
    expect(input["exclude"]).toBeUndefined();
    const done = within(dialog).getByRole("button", { name: "Done" });
    expect(done).toBeDisabled();
    await user.click(within(dialog).getByLabelText("I've saved it somewhere safe"));
    await user.click(done);
    expect(
      await screen.findByRole("heading", { level: 1, name: "Billing DB" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("correct-horse-battery-staple-xyz")).toBeNull();
  });
});

describe("job detail", () => {
  it("runs an extra run and reports an active run", async () => {
    let active = false;
    const { user, callsOf } = renderApp(`/jobs/${JOB_ID}`, {
      ...baseHandlers(),
      ...detailHandlers(),
      RunJobNow: () =>
        active
          ? gqlError("CONFLICT", "a run of this job is already active")
          : ok({
              runJobNow: {
                __typename: "RunJobNowResult",
                runId: "r-new",
                queuedAt: "2026-10-28T10:00:00Z",
                job: { __typename: "Job", id: JOB_ID, nextRunAt: "2026-10-29T03:00:00Z" },
              },
            }),
    });
    await user.click(await screen.findByRole("button", { name: /Run now/ }));
    let dialog = await screen.findByRole("dialog", { name: "Start an extra run now?" });
    expect(within(dialog).getByText(/schedule does not change/)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Start now" }));
    expect(await screen.findByText("Extra run queued.")).toBeInTheDocument();
    expect(callsOf("RunJobNow")).toHaveLength(1);

    active = true;
    await user.click(screen.getByRole("button", { name: /Run now/ }));
    dialog = await screen.findByRole("dialog", { name: "Start an extra run now?" });
    await user.click(within(dialog).getByRole("button", { name: "Start now" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(/already queued or running/);
  });

  it("explains which occurrence moves and handles 'already started early'", async () => {
    let early = 0;
    const { user } = renderApp(`/jobs/${JOB_ID}`, {
      ...baseHandlers(),
      ...detailHandlers(),
      JobNextOccurrences: () =>
        ok({
          previewSchedule: {
            __typename: "SchedulePreview",
            next: ["2026-10-29T03:00:00Z", "2026-10-30T03:00:00Z", "2026-10-31T03:00:00Z"],
          },
        }),
      StartNextRunEarly: () => {
        early++;
        return early === 1
          ? ok({
              startNextRunEarly: {
                __typename: "RunJobNowResult",
                runId: "r-early",
                queuedAt: "2026-10-28T10:00:00Z",
                job: { __typename: "Job", id: JOB_ID, nextRunAt: "2026-10-30T03:00:00Z" },
              },
            })
          : gqlError("VALIDATION", "the next planned run was already started early", "id");
      },
    });
    await user.click(await screen.findByRole("button", { name: "Start next planned run early" }));
    let dialog = await screen.findByRole("dialog", { name: "Start the next planned run now?" });
    const ledger = within(dialog).getByTestId("early-ledger");
    expect(within(ledger).getByText("Starts now")).toBeInTheDocument();
    expect(await within(ledger).findByTestId("early-following")).toHaveAttribute(
      "datetime",
      "2026-10-30T03:00:00Z",
    );
    await user.click(within(dialog).getByRole("button", { name: "Start now" }));
    expect(await screen.findByText(/Started early. Next planned run:/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Start next planned run early" }));
    dialog = await screen.findByRole("dialog", { name: "Start the next planned run now?" });
    await user.click(within(dialog).getByRole("button", { name: "Start now" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      /Only one occurrence can be pulled forward/,
    );
  });

  it("disables starting early for a disabled job and shows the banner", async () => {
    renderApp(`/jobs/${JOB_ID}`, {
      ...baseHandlers(),
      ...detailHandlers(jobDetail({ enabled: false, nextRunAt: null })),
    });
    expect(
      await screen.findByRole("button", { name: "Start next planned run early" }),
    ).toBeDisabled();
    expect(screen.getByText(/This job is disabled/)).toBeInTheDocument();
  });

  it("shows owners staff controls only to staff", async () => {
    renderApp(`/jobs/${JOB_ID}`, {
      ...baseHandlers(meFixture({}, "USER")),
      ...detailHandlers(jobDetail({ owners: null })),
    });
    expect(await screen.findByRole("button", { name: /Run now/ })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Edit" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Delete" })).toBeNull();
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.queryByText("Owners")).toBeNull();
  });
});

describe("encryption panel", () => {
  it("lets admins generate a passphrase behind a step-up and shows it once", async () => {
    let stepped = false;
    const { user, callsOf } = renderApp(`/jobs/${JOB_ID}/edit`, {
      ...baseHandlers(),
      JobDetail: () => ok({ job: jobDetail() }),
      JobEditorOptions: () => ok({ connections: [], destinations: [] }),
      TestRestoreTargets: () => ok({ connections: [] }),
      PreviewSchedule: () => gqlError("VALIDATION", "x"),
      UserPickerCandidates: () =>
        ok({ userDirectory: { __typename: "UserDirectoryConnection", edges: [] } }),
      GenerateJobPassphrase: () =>
        stepped
          ? ok({
              generateJobPassphrase: {
                __typename: "GeneratedJobPassphrase",
                version: 2,
                passphrase: "new-pass-phrase-v2",
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
    await user.click(await screen.findByRole("button", { name: "Generate passphrase" }));
    const confirm = await screen.findByRole("dialog", { name: "Generate a new passphrase?" });
    await user.click(within(confirm).getByRole("button", { name: "Generate passphrase" }));
    const stepUp = await screen.findByRole("dialog", { name: "Confirm your identity" });
    await user.type(within(stepUp).getByLabelText("6-digit code"), "123456");
    await user.click(within(stepUp).getByRole("button", { name: "Confirm" }));
    const shown = await screen.findByRole("dialog", { name: "Save the new passphrase" });
    expect(within(shown).getByText("new-pass-phrase-v2")).toBeInTheDocument();
    expect(callsOf("GenerateJobPassphrase")).toHaveLength(2);
  });

  it("shows maintainers the status without key actions", async () => {
    renderApp(`/jobs/${JOB_ID}/edit`, {
      ...baseHandlers(meFixture({}, "MAINTAINER")),
      JobDetail: () => ok({ job: jobDetail() }),
      JobEditorOptions: () => ok({ connections: [], destinations: [] }),
      TestRestoreTargets: () => ok({ connections: [] }),
      PreviewSchedule: () => gqlError("VALIDATION", "x"),
      UserPickerCandidates: () =>
        ok({
          userDirectory: {
            __typename: "UserDirectoryConnection",
            edges: [
              {
                __typename: "UserDirectoryEdge",
                node: {
                  __typename: "UserDirectoryEntry",
                  id: "u-2",
                  email: "uma@example.com",
                  displayName: "Uma User",
                  role: "USER",
                  disabled: false,
                },
              },
              {
                __typename: "UserDirectoryEdge",
                node: {
                  __typename: "UserDirectoryEntry",
                  id: "u-3",
                  email: "max@example.com",
                  displayName: "Max Maintainer",
                  role: "MAINTAINER",
                  disabled: false,
                },
              },
            ],
          },
        }),
    });
    expect(await screen.findByText(/Only administrators can change keys/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Generate passphrase" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Turn encryption off" })).toBeNull();
    // Maintainers pick owners with the shared UserPicker over the staff
    // directory (D-184); only role USER can be chosen (D-067).
    const list = await screen.findByRole("list", { name: "Add backup owners" });
    const uma = await within(list).findByRole("button", { name: /Uma User/ });
    expect(uma).not.toHaveAttribute("aria-disabled");
    expect(within(list).getByRole("button", { name: /Max Maintainer/ })).toHaveAttribute(
      "aria-disabled",
      "true",
    );
  });
});
