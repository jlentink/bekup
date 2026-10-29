import { screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { Role } from "@/graphql/gen/graphql";
import { baseHandlers, gqlError, meFixture, ok, renderApp, type Handler } from "@/test/mock-api";

const ART = "a0000000-0000-4000-8000-000000000001";
const JOB = "b0000000-0000-4000-8000-000000000001";
const RUN = "d0000000-0000-4000-8000-000000000001";
const T1 = "c0000000-0000-4000-8000-000000000001";
const T2 = "c0000000-0000-4000-8000-000000000002";
const RID = "e0000000-0000-4000-8000-000000000001";
const KEY = "AGE-SECRET-KEY-1QYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQQ";
const NOW = "2026-10-28T10:00:00Z";
/**
 * The first render loads the lazy route chunk; allow for a busy test run
 * (above the global asyncUtilTimeout of src/test/setup.ts).
 */
const SLOW = { timeout: 15_000 };

const privileges = (required = false) => ({
  __typename: "TargetPrivileges",
  status: required ? "PRIVILEGED" : "NOT_PRIVILEGED",
  reasons: required ? ["superuser"] : [],
  acknowledgementRequired: required,
});

function source(over: Record<string, unknown> = {}) {
  return {
    __typename: "RestoreSource",
    artifactId: ART,
    jobId: JOB,
    runId: RUN,
    kind: "DATABASE",
    database: "shop",
    engine: "POSTGRES",
    versionKey: "17",
    serverVersion: "17.2",
    needsIdentity: false,
    ...over,
  };
}

function target(over: Record<string, unknown> = {}) {
  return {
    __typename: "RestoreTarget",
    id: T1,
    name: "pg-main",
    engine: "POSTGRES",
    versionKey: "17",
    original: true,
    temporary: false,
    expiresAt: null,
    ready: true,
    notReadyReason: null,
    insecureTransport: false,
    newDatabaseAllowed: true,
    existingRestricted: false,
    ...over,
  };
}

function restore(over: Record<string, unknown> = {}) {
  return {
    __typename: "Restore",
    id: RID,
    job: { __typename: "RunJobRef", id: JOB, name: "Shop nightly", slug: "shop", deleted: false },
    artifactId: ART,
    runId: RUN,
    kind: "DATABASE",
    sourceDatabase: "shop",
    target: {
      __typename: "RestoreTargetRef",
      id: T1,
      name: "pg-main",
      engine: "POSTGRES",
      temporary: false,
      deleted: false,
    },
    targetDatabase: "shop",
    mode: "OVERWRITE",
    keepOwners: false,
    status: "QUEUED",
    requestedBy: null,
    createdAt: NOW,
    startedAt: null,
    finishedAt: null,
    durationSeconds: null,
    stagedBytes: 0,
    errorCode: null,
    errorMessage: null,
    warnings: [],
    logLines: 0,
    logLinesDropped: 0,
    updatedAt: NOW,
    ...over,
  };
}

interface Opts {
  role?: Role;
  src?: Record<string, unknown>;
  targets?: Record<string, unknown>[];
  privileged?: boolean;
  previewOver?: Record<string, unknown>;
  factors?: { totp: boolean; passkeys: number };
  extra?: Record<string, Handler>;
}

function handlers({
  role = "ADMIN",
  src = {},
  targets = [target()],
  privileged = false,
  previewOver = {},
  factors = { totp: true, passkeys: 0 },
  extra = {},
}: Opts = {}): Record<string, Handler> {
  return {
    ...baseHandlers(meFixture({}, role)),
    RestoreTargets: () =>
      ok({
        restoreTargets: {
          __typename: "RestoreTargets",
          source: source(src),
          targets,
        },
      }),
    RestoreTargetDatabases: () =>
      ok({
        restoreTargetDatabases: {
          __typename: "RestoreTargetDatabases",
          databases: ["shop", "shop_staging"],
          warnings: [],
          listedAt: NOW,
          targetPrivileges: privileges(privileged),
        },
      }),
    RestorePreview: (vars) => {
      const input = vars["input"] as Record<string, unknown>;
      const overwrite = input["mode"] === "OVERWRITE";
      const globals = src["kind"] === "GLOBALS";
      return ok({
        restorePreview: {
          __typename: "RestorePreview",
          mode: globals ? null : input["mode"],
          targetDatabase: globals ? null : input["targetDatabase"],
          warnings: globals
            ? [
                {
                  __typename: "RestoreWarning",
                  code: "globals_change_roles",
                  message: "A globals restore changes roles on the whole server.",
                },
              ]
            : [
                {
                  __typename: "RestoreWarning",
                  code: "restore_hardening",
                  message: "bekup hardens the restored database.",
                },
                ...(overwrite
                  ? [
                      {
                        __typename: "RestoreWarning",
                        code: "overwrite_swap",
                        message: "shop is replaced as a whole.",
                      },
                    ]
                  : []),
              ],
          confirmText: globals
            ? "restore globals into pg-main"
            : overwrite
              ? input["targetDatabase"]
              : null,
          crossSource: false,
          targetPrivileges: privileges(privileged),
          target: {
            __typename: "RestoreTarget",
            id: T1,
            name: "pg-main",
            engine: "POSTGRES",
            temporary: false,
            insecureTransport: false,
          },
          source: {
            __typename: "RestoreSource",
            artifactId: ART,
            kind: src["kind"] ?? "DATABASE",
            database: globals ? null : "shop",
            engine: "POSTGRES",
            versionKey: "17",
            serverVersion: "17.2",
            needsIdentity: src["needsIdentity"] ?? false,
          },
          ...previewOver,
        },
      });
    },
    RestoreFactorStatus: () =>
      ok({
        myTwoFactorStatus: { __typename: "TwoFactorStatus", totpEnabled: factors.totp },
        myPasskeys: Array.from({ length: factors.passkeys }, (_, i) => ({
          __typename: "Passkey",
          id: `f0000000-0000-4000-8000-00000000000${String(i)}`,
        })),
      }),
    BeginStepUp: () =>
      ok({
        beginStepUp: { __typename: "StepUpChallenge", methods: ["TOTP"], passkeyOptions: null },
      }),
    CompleteStepUp: () =>
      ok({
        completeStepUp: { __typename: "StepUpResult", stepUpAt: NOW, aal2At: NOW, aal: 2 },
      }),
    RestoreArtifact: () => ok({ restoreArtifact: restore() }),
    RestoreGlobals: () =>
      ok({
        restoreGlobals: restore({
          kind: "GLOBALS",
          sourceDatabase: null,
          targetDatabase: null,
          mode: null,
        }),
      }),
    RestoreDetail: () => ok({ restore: restore() }),
    RestoreLogs: () =>
      ok({
        restoreLogs: {
          __typename: "RestoreLogPage",
          restoreId: RID,
          lines: [],
          endId: null,
          hasMore: false,
          dropped: 0,
        },
      }),
    ...extra,
  };
}

const cont = () => screen.getByRole("button", { name: /Continue/ });

async function toTarget(user: ReturnType<typeof renderApp>["user"]) {
  // Wait for the source step to load: no fixed sleep and no optional find
  // (a find that may never match would burn the whole async timeout).
  await waitFor(() => {
    expect(cont()).toBeEnabled();
  }, SLOW);
  await user.click(cont());
  await screen.findByRole("heading", { name: "Target", level: 2 });
}

describe("restore wizard", () => {
  it("shows owners only their allowed targets, without temporary connections", async () => {
    const { user } = renderApp(
      `/restore?artifactId=${ART}`,
      handlers({ role: "USER", targets: [target({ existingRestricted: true })] }),
    );
    await screen.findByText("shop", undefined, SLOW);
    await toTarget(user);
    expect(screen.getAllByRole("radio", { name: /pg-main/ })).toHaveLength(1);
    expect(screen.getByText(/only the databases your own jobs back up/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /temporary connection/ })).toBeNull();
  });

  it("offers admins a temporary connection and refuses targets that are not ready", async () => {
    const { user } = renderApp(
      `/restore?artifactId=${ART}`,
      handlers({
        targets: [
          target(),
          target({
            id: T2,
            name: "pg-old",
            ready: false,
            notReadyReason: "certificate pin missing",
          }),
        ],
      }),
    );
    await screen.findByText("shop", undefined, SLOW);
    await toTarget(user);
    expect(screen.getByRole("radio", { name: /pg-old/ })).toBeDisabled();
    expect(screen.getByText(/certificate pin missing/)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Create a temporary connection" }),
    ).toBeInTheDocument();
  });

  it("walks an overwrite with a privileged target: warnings, acknowledgement, typed name, AAL2 step-up, progress", async () => {
    let restoreDone = false;
    const { user, callsOf } = renderApp(
      `/restore?artifactId=${ART}`,
      handlers({
        privileged: true,
        extra: {
          RestoreUpdated: () => {
            restoreDone = true;
            return ok({
              restoreUpdated: restore({
                status: "SUCCEEDED",
                logLines: 2,
                durationSeconds: 42,
                updatedAt: "2026-10-28T10:05:00Z",
                warnings: [
                  {
                    __typename: "RestoreOutcomeWarning",
                    code: "secdef_neutralized",
                    message: "SECURITY DEFINER functions were switched to SECURITY INVOKER.",
                  },
                ],
              }),
            });
          },
          RestoreLogs: () =>
            ok({
              restoreLogs: {
                __typename: "RestoreLogPage",
                restoreId: RID,
                lines: restoreDone
                  ? [
                      {
                        __typename: "RunLogLine",
                        id: "1",
                        time: NOW,
                        level: "WARN",
                        stream: "WORKER",
                        line: "1 SECURITY DEFINER function(s) or procedure(s) were switched",
                      },
                      {
                        __typename: "RunLogLine",
                        id: "2",
                        time: NOW,
                        level: "WARN",
                        stream: "WORKER",
                        line: "switched to SECURITY INVOKER: public.esc()",
                      },
                    ]
                  : [],
                endId: restoreDone ? "2" : null,
                hasMore: false,
                dropped: 0,
              },
            }),
        },
      }),
    );
    await screen.findByText("shop", undefined, SLOW);
    await toTarget(user);

    await user.click(screen.getByRole("radio", { name: /pg-main/ }));
    await user.click(screen.getByRole("radio", { name: /Replace an existing database/ }));
    await user.click(await screen.findByRole("radio", { name: "shop" }));
    await user.click(cont());

    await screen.findByRole("heading", { name: "Options", level: 2 });
    await user.click(cont());

    // Preview: every warning, destructive ones first and marked; the admin acknowledgement gates Continue.
    await screen.findByRole("heading", { name: "Preview", level: 2 });
    await screen.findByRole("heading", { name: /Before you continue/ });
    const swap = document.querySelector('[data-code="overwrite_swap"]');
    expect(swap).toHaveAttribute("data-severity", "destructive");
    expect(document.querySelector('[data-code="restore_hardening"]')).toHaveTextContent(
      /GRANT CONNECT/,
    );
    expect(screen.getByText(/Privileged target role/)).toBeInTheDocument();
    expect(cont()).toBeDisabled();
    await user.click(screen.getByRole("checkbox", { name: /I trust this backup's source/ }));
    expect(cont()).toBeEnabled();
    await user.click(cont());

    // Confirm: the exact database name, then the AAL2 step-up before the request.
    await screen.findByRole("heading", { name: "Confirm", level: 2 });
    const submit = await screen.findByRole("button", { name: "Verify and start the restore" });
    await waitFor(() => {
      expect(callsOf("RestoreFactorStatus")).toHaveLength(1);
    });
    expect(submit).toBeDisabled();
    await user.type(screen.getByLabelText("Database name to confirm"), "Shop");
    expect(submit).toBeDisabled();
    await user.clear(screen.getByLabelText("Database name to confirm"));
    await user.type(screen.getByLabelText("Database name to confirm"), "shop");
    await waitFor(() => {
      expect(submit).toBeEnabled();
    });
    await user.click(submit);

    const dialog = await screen.findByRole("dialog", { name: "Confirm your identity" });
    expect(callsOf("RestoreArtifact")).toHaveLength(0);
    await user.type(within(dialog).getByLabelText("6-digit code"), "654321");
    await user.click(within(dialog).getByRole("button", { name: "Confirm" }));

    await screen.findByRole("heading", { name: "Progress", level: 2 });
    expect(callsOf("RestoreArtifact")).toHaveLength(1);
    expect(callsOf("RestoreArtifact")[0]?.variables["input"]).toEqual({
      artifactId: ART,
      targetConnectionId: T1,
      mode: "OVERWRITE",
      targetDatabase: "shop",
      confirmTargetName: "shop",
      keepOwners: false,
      acknowledgePrivilegedTarget: true,
    });

    // Progress: live status from restoreUpdated, the log and the neutralized routines.
    expect(await screen.findByText("Restore complete")).toBeInTheDocument();
    expect(await screen.findByText("public.esc()")).toBeInTheDocument();
    expect(screen.getByText("Switched to SECURITY INVOKER")).toBeInTheDocument();
  });

  it("blocks non-admins on a privileged target", async () => {
    const { user } = renderApp(
      `/restore?artifactId=${ART}`,
      handlers({ role: "MAINTAINER", privileged: true }),
    );
    await screen.findByText("shop", undefined, SLOW);
    await toTarget(user);
    await user.click(screen.getByRole("radio", { name: /pg-main/ }));
    await user.type(screen.getByLabelText("New database name"), "shop_copy");
    await user.click(cont());
    await user.click(cont());
    await screen.findByText(/Only an admin can restore into this connection/);
    expect(screen.queryByRole("checkbox", { name: /I trust/ })).toBeNull();
    expect(cont()).toBeDisabled();
  });

  it("sends the age identity only with the restore request and clears it", async () => {
    const { user, callsOf } = renderApp(
      `/restore?artifactId=${ART}`,
      handlers({ src: { needsIdentity: true } }),
    );
    await screen.findByText("shop", undefined, SLOW);
    await toTarget(user);
    await user.click(screen.getByRole("radio", { name: /pg-main/ }));
    await user.type(screen.getByLabelText("New database name"), "shop_copy");
    await user.click(cont());

    await screen.findByRole("heading", { name: "Options", level: 2 });
    expect(cont()).toBeDisabled();
    const field = screen.getByLabelText("Age identity");
    await user.type(field, "nonsense");
    expect(screen.getByText("This is not an age identity.")).toBeInTheDocument();
    await user.clear(field);
    await user.click(field);
    await user.paste(KEY);
    expect(cont()).toBeEnabled();
    await user.click(cont());

    await screen.findByRole("heading", { name: "Preview", level: 2 });
    await waitFor(() => {
      expect(callsOf("RestorePreview")).toHaveLength(1);
    });
    expect(JSON.stringify(callsOf("RestorePreview"))).not.toContain("AGE-SECRET-KEY");
    await user.click(await screen.findByRole("button", { name: /Continue/ }));

    const submit = await screen.findByRole("button", { name: "Verify and start the restore" });
    await waitFor(() => {
      expect(submit).toBeEnabled();
    });
    await user.click(submit);
    const dialog = await screen.findByRole("dialog", { name: "Confirm your identity" });
    await user.type(within(dialog).getByLabelText("6-digit code"), "654321");
    await user.click(within(dialog).getByRole("button", { name: "Confirm" }));
    await screen.findByRole("heading", { name: "Progress", level: 2 });

    const sent = callsOf("RestoreArtifact");
    expect(sent).toHaveLength(1);
    expect(sent[0]?.variables["input"]).toMatchObject({ ageIdentity: KEY, mode: "NEW_DB" });
    // Never persisted by the page.
    for (const store of [window.localStorage, window.sessionStorage]) {
      for (let i = 0; i < store.length; i++) {
        expect(store.getItem(store.key(i) ?? "")).not.toContain("AGE-SECRET-KEY");
      }
    }
    expect(document.body.textContent).not.toContain(KEY);
    // Back on the options the field is empty again.
    expect(screen.queryByLabelText("Age identity")).toBeNull();
  });

  it("guides accounts without a second factor to enrollment", async () => {
    const { user, callsOf } = renderApp(
      `/restore?artifactId=${ART}`,
      handlers({ factors: { totp: false, passkeys: 0 } }),
    );
    await screen.findByText("shop", undefined, SLOW);
    await toTarget(user);
    await user.click(screen.getByRole("radio", { name: /pg-main/ }));
    await user.type(screen.getByLabelText("New database name"), "shop_copy");
    await user.click(cont());
    await user.click(cont());
    await screen.findByRole("heading", { name: "Preview", level: 2 });
    await user.click(await screen.findByRole("button", { name: /Continue/ }));
    expect(
      await screen.findByText("Set up a passkey or an authenticator app first"),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open two-factor settings" })).toHaveAttribute(
      "href",
      "/profile/security",
    );
    expect(screen.getByRole("button", { name: "Verify and start the restore" })).toBeDisabled();
    expect(callsOf("BeginStepUp")).toHaveLength(0);
  });

  it("shows the enrollment guide when the server answers REQUIRES_2FA_ENROLLMENT", async () => {
    const { user } = renderApp(
      `/restore?artifactId=${ART}`,
      handlers({
        extra: {
          RestoreArtifact: () => gqlError("REQUIRES_2FA_ENROLLMENT", "a second factor is required"),
        },
      }),
    );
    await screen.findByText("shop", undefined, SLOW);
    await toTarget(user);
    await user.click(screen.getByRole("radio", { name: /pg-main/ }));
    await user.type(screen.getByLabelText("New database name"), "shop_copy");
    await user.click(cont());
    await user.click(cont());
    await screen.findByRole("heading", { name: "Preview", level: 2 });
    await user.click(await screen.findByRole("button", { name: /Continue/ }));
    const submit = await screen.findByRole("button", { name: "Verify and start the restore" });
    await waitFor(() => {
      expect(submit).toBeEnabled();
    });
    await user.click(submit);
    const dialog = await screen.findByRole("dialog", { name: "Confirm your identity" });
    await user.type(within(dialog).getByLabelText("6-digit code"), "654321");
    await user.click(within(dialog).getByRole("button", { name: "Confirm" }));
    expect(
      await screen.findByText("Set up a passkey or an authenticator app first"),
    ).toBeInTheDocument();
  });

  it("restores globals (admins) only with the exact phrase", async () => {
    const { user, callsOf } = renderApp(
      `/restore?artifactId=${ART}`,
      handlers({ src: { kind: "GLOBALS", database: null } }),
    );
    await screen.findByText("roles and tablespaces", undefined, SLOW);
    await toTarget(user);
    expect(screen.queryByRole("radio", { name: /Replace an existing database/ })).toBeNull();
    await user.click(screen.getByRole("radio", { name: /pg-main/ }));
    await user.click(cont());
    // No options step for globals.
    await screen.findByRole("heading", { name: "Preview", level: 2 });
    expect(document.querySelector('[data-code="globals_change_roles"]')).toHaveAttribute(
      "data-severity",
      "destructive",
    );
    await user.click(cont());
    const submit = await screen.findByRole("button", { name: "Verify and start the restore" });
    const phrase = screen.getByLabelText("Confirmation phrase");
    await user.type(phrase, "restore globals into pg-mai");
    expect(submit).toBeDisabled();
    await user.type(phrase, "n");
    await waitFor(() => {
      expect(submit).toBeEnabled();
    });
    await user.click(submit);
    const dialog = await screen.findByRole("dialog", { name: "Confirm your identity" });
    await user.type(within(dialog).getByLabelText("6-digit code"), "654321");
    await user.click(within(dialog).getByRole("button", { name: "Confirm" }));
    await screen.findByRole("heading", { name: "Progress", level: 2 });
    expect(callsOf("RestoreGlobals")[0]?.variables["input"]).toEqual({
      artifactId: ART,
      targetConnectionId: T1,
      confirmPhrase: "restore globals into pg-main",
    });
  });
});
