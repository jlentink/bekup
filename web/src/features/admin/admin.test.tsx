import { screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { baseHandlers, gqlError, type Handler, meFixture, ok, renderApp } from "@/test/mock-api";

import { emptyDraft, toAuditFilter, validateDraft } from "./audit/model";
import {
  changed,
  fieldList,
  fieldRow,
  hint,
  scopeHint,
  toggleScope,
  widenings,
} from "./network/model";
import { attachableCatalog, toFilterInput, type CatalogGroup } from "./channels/filter";
import { alertPath } from "./security-alerts/alert-types";
import { needsPasswordReentry, toInput, toFormValues } from "./smtp/model";

const JOB_ID = "0b6c3f7e-0000-4000-8000-0000000000a1";
const CHANNEL_ID = "0b6c3f7e-0000-4000-8000-0000000000c1";

/* ---------------- fixtures ---------------- */

function smtp(over: Record<string, unknown> = {}) {
  return {
    __typename: "SmtpSettings",
    configured: true,
    enabled: true,
    host: "smtp.example.com",
    port: 587,
    mode: "AUTO",
    username: "mailer",
    hasPassword: true,
    fromAddress: "bekup@example.com",
    fromName: null,
    caBundlePem: null,
    connectTimeoutSeconds: 10,
    sendTimeoutSeconds: 60,
    mayBeUnencrypted: true,
    updatedAt: "2026-10-27T10:00:00Z",
    ...over,
  };
}

const CATALOG: CatalogGroup[] = [
  {
    group: "BACKUP",
    label: "Backup",
    events: [
      {
        type: "backup.failed",
        label: "Backup failed",
        group: "BACKUP",
        mandatory: false,
        perJob: true,
      },
      {
        type: "backup.succeeded",
        label: "Backup succeeded",
        group: "BACKUP",
        mandatory: false,
        perJob: true,
      },
    ],
  },
  {
    group: "SECURITY",
    label: "Security",
    events: [
      {
        type: "security.system",
        label: "Security alert",
        group: "SECURITY",
        mandatory: true,
        perJob: false,
      },
      {
        type: "security.account",
        label: "Account alert",
        group: "SECURITY",
        mandatory: true,
        perJob: false,
      },
    ],
  },
];

function catalogResponse() {
  return ok({
    notificationCatalog: CATALOG.map((g) => ({
      __typename: "NotificationEventGroup",
      ...g,
      events: g.events.map((e) => ({
        __typename: "NotificationEventInfo",
        ...e,
        severity: "INFO",
      })),
    })),
  });
}

function channel(over: Record<string, unknown> = {}) {
  return {
    __typename: "SharedChannel",
    id: CHANNEL_ID,
    name: "Ops chat",
    type: "WEBHOOK",
    addresses: [],
    displayUrl: "https://hooks.example.com:443",
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
    attachments: [],
    createdAt: "2026-10-27T10:00:00Z",
    updatedAt: "2026-10-27T10:00:00Z",
    ...over,
  };
}

function jobsResponse() {
  return ok({
    jobs: {
      __typename: "JobConnection",
      edges: [
        {
          __typename: "JobEdge",
          cursor: "c1",
          node: {
            __typename: "Job",
            id: JOB_ID,
            name: "Billing DB",
            slug: "billing-db",
            enabled: true,
            encryption: {
              __typename: "JobEncryption",
              configured: true,
              mode: "PASSPHRASE",
              optedOut: false,
              passphrase: {
                __typename: "JobPassphraseInfo",
                version: 2,
                createdAt: "2026-10-01T10:00:00Z",
              },
            },
          },
        },
      ],
      pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: "c1" },
    },
  });
}

function auditEntry(i: number) {
  return {
    __typename: "AuditLogEdge",
    cursor: `a${String(i)}`,
    node: {
      __typename: "AuditLogEntry",
      id: String(i),
      ts: "2026-10-28T09:00:00Z",
      actorKind: "USER",
      actorUserId: "6f1c3e0a-0000-4000-8000-000000000001",
      action: i === 1 ? "user.created" : "auth.login.succeeded",
      targetType: "user",
      targetId: "u-1",
      ip: "192.0.2.10",
      details: '{"role":"MAINTAINER"}',
      hash: `abcdef0123456789${String(i)}`,
    },
  };
}

function chainStatus(over: Record<string, unknown> = {}) {
  return ok({
    auditChainStatus: {
      __typename: "AuditChainStatus",
      lastVerification: {
        __typename: "AuditChainVerification",
        checkedAt: "2026-10-28T09:30:00Z",
        ok: true,
        full: false,
        finding: null,
        verifiedThroughId: "41",
      },
      headId: "41",
      headHash: "abcdef0123456789abcdef",
      headAt: "2026-10-28T09:20:00Z",
      latestSignedHead: {
        __typename: "AuditSignedHead",
        chainId: "40",
        signedAt: "2026-10-28T09:00:00Z",
        keyId: "0011223344556677",
      },
      ...over,
    },
  });
}

/** Handlers with a step-up gate in front of `gated` operations. */
function withStepUp(handlers: Record<string, Handler>, gated: readonly string[]) {
  let stepped = false;
  const out: Record<string, Handler> = {
    ...baseHandlers(),
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
          aal2At: "2026-10-28T10:00:00Z",
          aal: 2,
        },
      });
    },
  };
  for (const [name, h] of Object.entries(handlers)) {
    out[name] = gated.includes(name)
      ? (v) => (stepped ? h(v) : gqlError("STEP_UP_REQUIRED", "please confirm your identity again"))
      : h;
  }
  return out;
}

async function confirmStepUp(user: ReturnType<typeof renderApp>["user"]) {
  const stepUp = await screen.findByRole("dialog", { name: "Confirm your identity" });
  await user.type(within(stepUp).getByLabelText("6-digit code"), "654321");
  await user.click(within(stepUp).getByRole("button", { name: "Confirm" }));
}

beforeEach(() => {
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
  vi.unstubAllGlobals();
});

/* ---------------- model units ---------------- */

describe("admin models", () => {
  it("excludes security and mandatory events from shared-channel filters", () => {
    const allowed = attachableCatalog(CATALOG);
    expect(allowed.map((g) => g.group)).toEqual(["BACKUP"]);
    expect(
      toFilterInput({ groups: ["SECURITY", "BACKUP"], events: ["security.system"] }, CATALOG),
    ).toEqual({ groups: ["BACKUP"], events: [] });
    expect(
      toFilterInput({ groups: [], events: ["backup.failed", "security.account"] }, CATALOG),
    ).toEqual({ groups: [], events: ["backup.failed"] });
  });

  it("builds audit filters (prefix vs exact) and validates input", () => {
    expect(toAuditFilter({ ...emptyDraft, action: "auth." })).toEqual({ actionPrefix: "auth." });
    expect(toAuditFilter({ ...emptyDraft, action: "cli.admin*" })).toEqual({
      actionPrefix: "cli.admin",
    });
    expect(toAuditFilter({ ...emptyDraft, action: "user.created", actorKind: "CLI" })).toEqual({
      action: "user.created",
      actorKind: "CLI",
    });
    expect(validateDraft({ ...emptyDraft, actorUserId: "nope" }).actorUserId).toBeDefined();
    expect(
      validateDraft({ ...emptyDraft, from: "2026-10-02T00:00", to: "2026-10-01T00:00" }).to,
    ).toBeDefined();
  });

  it("forces the SMTP password again for a new endpoint", () => {
    const s = { ...smtp(), mode: "AUTO" as const };
    const v = toFormValues(s);
    expect(needsPasswordReentry(s, v)).toBe(false);
    expect(needsPasswordReentry(s, { ...v, host: "other.example.com" })).toBe(true);
    expect(toInput({ ...v, passwordAction: "clear" }).clearPassword).toBe(true);
    expect(toInput(v).password).toBeUndefined();
  });

  it("finds widening allowlist entries and scopes and gives early hints", () => {
    const D = ["DESTINATIONS", "SMTP", "SHARED_WEBHOOKS"] as const;
    const e = (value: string, scopes: readonly (typeof D)[number][] | string[] = D) =>
      ({ value, scopes }) as Parameters<typeof widenings>[1][number];
    expect(
      widenings("hosts", [e("minio.lan")], [e("MinIO.lan."), e("smtp.lan", ["SMTP"])]),
    ).toEqual([{ kind: "hosts", value: "smtp.lan", isNew: true, scopes: ["SMTP"] }]);
    expect(widenings("cidrs", [e("10.0.0.0/8")], [e("10.0.0.0/8")])).toEqual([]);
    // A kept entry that gains a scope widens; one that loses a scope does not.
    expect(
      widenings(
        "cidrs",
        [e("10.0.0.0/8")],
        [e("10.0.0.0/8", ["PERSONAL_WEBHOOKS", "DESTINATIONS", "SMTP", "SHARED_WEBHOOKS"])],
      ),
    ).toEqual([
      { kind: "cidrs", value: "10.0.0.0/8", isNew: false, scopes: ["PERSONAL_WEBHOOKS"] },
    ]);
    expect(widenings("cidrs", [e("10.0.0.0/8")], [e("10.0.0.0/8", ["SMTP"])])).toEqual([]);
    expect(changed("cidrs", [e("a/8"), e("b/8")], [e("b/8"), e("a/8")])).toBe(false);
    expect(changed("cidrs", [e("a/8")], [])).toBe(true);
    expect(changed("cidrs", [e("a/8")], [e("a/8", ["SMTP"])])).toBe(true);
    expect(toggleScope(["SMTP"], "DESTINATIONS", true)).toEqual(["DESTINATIONS", "SMTP"]);
    expect(toggleScope(["SMTP", "DESTINATIONS"], "SMTP", false)).toEqual(["DESTINATIONS"]);
    expect(scopeHint([])).toMatch(/at least one/);
    expect(scopeHint(["SMTP"])).toBeNull();
    expect(hint("cidrs", "0.0.0.0/0")).toMatch(/every address/);
    expect(hint("cidrs", "10.0.0.1")).toMatch(/CIDR notation/);
    expect(hint("cidrs", "10.20.0.0/16")).toBeNull();
    expect(hint("hosts", "https://x.example")).toMatch(/host name only/);
    expect(hint("hosts", "10.0.0.1")).toMatch(/CIDR list/);
    expect(hint("hosts", "*.com")).toMatch(/two labels/);
    expect(hint("hosts", "*.corp.example")).toBeNull();
    expect(fieldRow("hosts.3")).toEqual({ kind: "hosts", index: 3, part: "value" });
    expect(fieldRow("cidrEntries.2.value")).toEqual({ kind: "cidrs", index: 2, part: "value" });
    expect(fieldRow("hostEntries.0.scopes")).toEqual({ kind: "hosts", index: 0, part: "scopes" });
    expect(fieldRow("cidrEntries.1")).toEqual({ kind: "cidrs", index: 1, part: "value" });
    expect(fieldRow("cidrEntries.x.value")).toBeNull();
    expect(fieldRow("cidrs")).toBeNull();
    expect(fieldList("cidrEntries")).toBe("cidrs");
    expect(fieldList("hosts")).toBe("hosts");
    expect(fieldList("cidrEntries.0")).toBeNull();
  });

  it("keeps alert links inside the app", () => {
    expect(alertPath("https://bekup.example.com/connections")).toBe("/connections");
    expect(alertPath("https://evil.example/admin/users")).toBe("/admin/users");
    expect(alertPath("javascript:alert(1)")).toBeNull();
    expect(alertPath("https://bekup.example.com/elsewhere/x")).toBeNull();
  });
});

/* ---------------- pages ---------------- */

describe("admin route guard", () => {
  it.each(["/admin/settings", "/admin/channels", "/admin/audit", "/admin/settings/recovery"])(
    "shows non-admins the admin-only page at %s",
    async (path) => {
      const { callsOf } = renderApp(path, {
        ...baseHandlers(meFixture({}, "MAINTAINER")),
      });
      expect(await screen.findByRole("heading", { name: "Admins only" })).toBeInTheDocument();
      expect(callsOf("AdminSmtpSettings")).toHaveLength(0);
      expect(callsOf("AdminSharedChannels")).toHaveLength(0);
      expect(callsOf("AdminAuditLog")).toHaveLength(0);
      expect(callsOf("AdminJobs")).toHaveLength(0);
    },
  );
});

describe("SMTP settings", () => {
  function handlers() {
    return withStepUp(
      {
        AdminSmtpSettings: () => ok({ smtpSettings: smtp() }),
        AdminUpdateSmtpSettings: (v) => {
          const input = v["input"] as Record<string, unknown>;
          return ok({
            updateSmtpSettings: smtp({
              mode: input["mode"],
              mayBeUnencrypted: input["mode"] === "AUTO" || input["mode"] === "NONE",
            }),
          });
        },
        AdminSendTestEmail: () =>
          ok({
            sendTestEmail: {
              __typename: "SmtpTestResult",
              ok: true,
              code: null,
              message: null,
              detail: null,
              encrypted: true,
              tlsVersion: "TLS 1.3",
            },
          }),
      },
      ["AdminUpdateSmtpSettings"],
    );
  }

  it("shows the unencrypted badge for AUTO and saves STARTTLS with a step-up", async () => {
    const { user, callsOf } = renderApp("/admin/settings", handlers());
    await screen.findByLabelText("Host");
    expect(screen.getAllByTestId("smtp-unencrypted-badge").length).toBeGreaterThan(0);

    await user.click(screen.getByRole("radio", { name: "STARTTLS" }));
    expect(screen.queryByRole("alert", { name: /may be sent unencrypted/i })).toBeNull();
    await user.click(screen.getByRole("radio", { name: "None" }));
    expect(
      await screen.findByRole("alert", { name: /E-mail may be sent unencrypted/ }),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: "STARTTLS" }));

    await user.click(screen.getByRole("button", { name: "Save settings" }));
    await confirmStepUp(user);
    expect(await screen.findByText("SMTP settings saved.")).toBeInTheDocument();
    const input = callsOf("AdminUpdateSmtpSettings").at(-1)?.variables["input"] as Record<
      string,
      unknown
    >;
    expect(input["mode"]).toBe("STARTTLS");
    expect(input).not.toHaveProperty("password");
    await waitFor(() => {
      expect(screen.queryAllByTestId("smtp-unencrypted-badge")).toHaveLength(0);
    });
  });

  it("requires the password again when the host changes", async () => {
    const { user, callsOf } = renderApp("/admin/settings", handlers());
    const host = await screen.findByLabelText("Host");
    await user.clear(host);
    await user.type(host, "relay.example.com");
    expect(screen.getByText(/enter the password again/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save settings" }));
    expect(await screen.findByText(/Enter the password for the new endpoint/)).toBeInTheDocument();
    expect(callsOf("AdminUpdateSmtpSettings")).toHaveLength(0);
  });

  it("shows the test e-mail result with encryption and TLS version", async () => {
    const { user, callsOf } = renderApp("/admin/settings", handlers());
    await user.click(await screen.findByRole("button", { name: "Send test e-mail" }));
    const result = await screen.findByTestId("smtp-test-result");
    expect(within(result).getByText("Delivered to the relay")).toBeInTheDocument();
    expect(within(result).getByText("Encrypted")).toBeInTheDocument();
    expect(within(result).getByText("TLS 1.3")).toBeInTheDocument();
    expect(callsOf("AdminSendTestEmail")[0]?.variables).toEqual({ to: "ada@example.com" });
  });
});

describe("shared channels", () => {
  it("creates a webhook and shows the signing key exactly once", async () => {
    let created = false;
    const { user, callsOf } = renderApp(
      "/admin/channels",
      withStepUp(
        {
          AdminSharedChannels: () => ok({ sharedChannels: created ? [channel()] : [] }),
          AdminNotificationCatalog: catalogResponse,
          AdminCreateSharedChannel: () => {
            created = true;
            return ok({
              createSharedChannel: {
                __typename: "SharedChannelWithKey",
                channel: channel(),
                signingKey: "whsec-one-time-value",
              },
            });
          },
        },
        ["AdminCreateSharedChannel"],
      ),
    );
    await screen.findByText("No shared channels yet");
    await user.click(screen.getByRole("button", { name: "New channel" }));
    const dlg = await screen.findByRole("dialog", { name: "New shared channel" });
    await user.type(within(dlg).getByLabelText("Name"), "Ops chat");
    await user.type(within(dlg).getByLabelText("Webhook URL"), "https://hooks.example.com/T0/B0/x");
    await user.click(within(dlg).getByRole("button", { name: "Create channel" }));
    await confirmStepUp(user);

    const keyDlg = await screen.findByRole("dialog", { name: "Ops chat is ready" });
    expect(within(keyDlg).getByText("whsec-one-time-value")).toBeInTheDocument();
    expect(within(keyDlg).getByRole("link", { name: /verify the signature/ })).toHaveAttribute(
      "href",
      expect.stringContaining("verifying-the-signature"),
    );
    const done = within(keyDlg).getByRole("button", { name: "Done" });
    expect(done).toBeDisabled();
    await user.click(within(keyDlg).getByLabelText("I have stored the key"));
    await user.click(done);
    await waitFor(() => {
      expect(screen.queryByText("whsec-one-time-value")).toBeNull();
    });
    expect(callsOf("AdminCreateSharedChannel").at(-1)?.variables).toEqual({
      input: { name: "Ops chat", type: "WEBHOOK", url: "https://hooks.example.com/T0/B0/x" },
    });
    // The masked URL only: scheme, host and port.
    expect(await screen.findByText("https://hooks.example.com:443")).toBeInTheDocument();
  });

  it("attaches to a job with an event filter that never offers security events", async () => {
    const { user, callsOf } = renderApp("/admin/channels", {
      ...baseHandlers(),
      AdminSharedChannels: () => ok({ sharedChannels: [channel()] }),
      AdminNotificationCatalog: catalogResponse,
      AdminJobs: jobsResponse,
      AdminAttachSharedChannel: () =>
        ok({
          attachSharedChannel: channel({
            attachments: [
              {
                __typename: "SharedChannelAttachment",
                jobId: JOB_ID,
                jobName: "Billing DB",
                groups: [],
                events: ["backup.failed"],
              },
            ],
          }),
        }),
    });
    await user.click(await screen.findByRole("button", { name: "Attach to a job" }));
    const dlg = await screen.findByRole("dialog", { name: "Attach to a job" });
    const filter = within(dlg).getByTestId("event-filter");
    expect(within(filter).getByText("Backup failed")).toBeInTheDocument();
    expect(within(filter).queryByText("Security alert")).toBeNull();
    expect(within(filter).queryByText(/security\./)).toBeNull();

    await user.click(await within(dlg).findByRole("radio", { name: /Billing DB/ }));
    await user.click(within(filter).getByRole("checkbox", { name: /Backup failed/ }));
    await user.click(within(dlg).getByRole("button", { name: "Attach" }));
    expect(await screen.findByText("Channel attached.")).toBeInTheDocument();
    expect(callsOf("AdminAttachSharedChannel")[0]?.variables).toEqual({
      jobId: JOB_ID,
      channelId: CHANNEL_ID,
      filter: { groups: [], events: ["backup.failed"] },
    });
  });
});

describe("audit log", () => {
  it("filters, pages and opens the redacted details", async () => {
    const { user, callsOf } = renderApp("/admin/audit", {
      ...baseHandlers(),
      AdminAuditChain: () => chainStatus(),
      AdminAuditLog: (v) =>
        ok({
          auditLog: {
            __typename: "AuditLogConnection",
            edges: v["after"] ? [auditEntry(2)] : [auditEntry(1)],
            pageInfo: {
              __typename: "PageInfo",
              hasNextPage: !v["after"],
              endCursor: v["after"] ? "a2" : "a1",
            },
          },
        }),
    });
    expect(await screen.findByText("Last verification passed")).toBeInTheDocument();
    expect(screen.getByTestId("audit-chain-status")).toHaveAttribute("data-state", "ok");
    expect(screen.getByTestId("audit-signed-head")).toHaveTextContent("#40");
    await screen.findByRole("button", { name: /Details of user\.created/ });

    await user.click(screen.getByRole("button", { name: "Next" }));
    await screen.findByRole("button", { name: /Details of auth\.login\.succeeded/ });
    expect(callsOf("AdminAuditLog").at(-1)?.variables).toMatchObject({ after: "a1", first: 50 });

    await user.click(screen.getByRole("button", { name: "auth." }));
    await waitFor(() => {
      expect(callsOf("AdminAuditLog").at(-1)?.variables).toEqual({
        filter: { actionPrefix: "auth." },
        first: 50,
        after: null,
      });
    });

    await user.type(screen.getByLabelText("Target type"), "user");
    await user.click(screen.getByRole("button", { name: "Apply filters" }));
    await waitFor(() => {
      expect(callsOf("AdminAuditLog").at(-1)?.variables["filter"]).toEqual({
        actionPrefix: "auth.",
        targetType: "user",
      });
    });

    await user.click(await screen.findByRole("button", { name: /Details of user\.created/ }));
    const drawer = await screen.findByRole("dialog", { name: "user.created" });
    expect(within(drawer).getByTestId("audit-details")).toHaveTextContent('"role": "MAINTAINER"');
  });

  it("flags a failed chain verification and filters its entries", async () => {
    const { user, callsOf } = renderApp("/admin/audit", {
      ...baseHandlers(),
      AdminAuditChain: () =>
        chainStatus({
          lastVerification: {
            __typename: "AuditChainVerification",
            checkedAt: "2026-10-28T08:00:00Z",
            ok: false,
            full: true,
            finding: "hash_mismatch:9",
            verifiedThroughId: "8",
          },
        }),
      AdminAuditLog: () =>
        ok({
          auditLog: {
            __typename: "AuditLogConnection",
            edges: [],
            pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: null },
          },
        }),
    });
    expect(await screen.findByText("Chain verification failed")).toBeInTheDocument();
    expect(screen.getByTestId("audit-chain-status")).toHaveAttribute("data-state", "failed");
    expect(screen.getByTestId("audit-chain-finding")).toHaveTextContent("hash_mismatch:9");
    await user.click(screen.getByRole("button", { name: "Show audit.verify.failed entries" }));
    await waitFor(() => {
      expect(callsOf("AdminAuditLog").at(-1)?.variables["filter"]).toEqual({
        action: "audit.verify.failed",
      });
    });
  });

  it("never claims a verification before one ran", async () => {
    renderApp("/admin/audit", {
      ...baseHandlers(),
      AdminAuditChain: () =>
        chainStatus({ lastVerification: null, latestSignedHead: null, headId: null }),
      AdminAuditLog: () =>
        ok({
          auditLog: {
            __typename: "AuditLogConnection",
            edges: [],
            pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: null },
          },
        }),
    });
    expect(await screen.findByText("Not verified yet")).toBeInTheDocument();
    expect(screen.getByTestId("audit-chain-status")).toHaveAttribute("data-state", "never");
    expect(screen.getByTestId("audit-signed-head")).toHaveTextContent(/None yet/);
  });
});

describe("SSRF allowlist", () => {
  const DEFAULTS = ["DESTINATIONS", "SMTP", "SHARED_WEBHOOKS"];
  const entry = (value: string, scopes: string[] = DEFAULTS) => ({
    __typename: "SsrfAllowlistEntry",
    value,
    scopes,
  });
  function allowlist(over: Record<string, unknown> = {}) {
    return {
      __typename: "SsrfAllowlist",
      cidrEntries: [entry("10.20.0.0/16")],
      hostEntries: [],
      configScopes: DEFAULTS,
      defaultScopes: DEFAULTS,
      personalWebhooksWidened: false,
      configCidrs: ["192.168.10.0/24"],
      configHosts: ["smtp.internal.example"],
      loopbackAllowed: false,
      ignoredEntries: 0,
      maxEntries: 256,
      updatedAt: "2026-10-27T10:00:00Z",
      ...over,
    };
  }
  interface EntryInput {
    value: string;
    scopes: string[];
  }

  it("shows bootstrap entries read-only and saves a widening change with default scopes after acknowledgement and step-up", async () => {
    const { user, callsOf } = renderApp(
      "/admin/settings/network",
      withStepUp(
        {
          AdminSsrfAllowlist: () => ok({ ssrfAllowlist: allowlist() }),
          AdminUpdateSsrfAllowlist: (v) => {
            const input = v["input"] as { cidrEntries: EntryInput[]; hostEntries: EntryInput[] };
            return ok({
              updateSsrfAllowlist: allowlist({
                cidrEntries: input.cidrEntries.map((e) => entry(e.value, e.scopes)),
                hostEntries: input.hostEntries.map((e) => entry(e.value, e.scopes)),
                updatedAt: "2026-10-28T10:00:00Z",
              }),
            });
          },
        },
        ["AdminUpdateSsrfAllowlist"],
      ),
    );
    expect(await screen.findByDisplayValue("10.20.0.0/16")).toBeInTheDocument();
    expect(screen.getByTestId("allowlist-config-cidrs")).toHaveTextContent("192.168.10.0/24");
    expect(screen.getByTestId("allowlist-config-hosts")).toHaveTextContent("smtp.internal.example");
    const configScopes = screen.getByTestId("allowlist-config-scopes-cidrs");
    expect(configScopes).toHaveTextContent("Shared webhooks");
    expect(configScopes).toHaveTextContent("Never to personal webhooks.");
    expect(within(configScopes).queryByRole("checkbox")).toBeNull();
    expect(screen.queryByTestId("personal-widened-badge")).toBeNull();
    const save = screen.getByRole("button", { name: "Save allowlist" });
    expect(save).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Add host name" }));
    await user.type(screen.getByLabelText("Host name 1"), "minio.internal.example");
    const uses = screen.getByRole("group", { name: "Uses of host name 1" });
    expect(within(uses).getByRole("checkbox", { name: /Destinations/ })).toBeChecked();
    expect(within(uses).getByRole("checkbox", { name: /Personal webhooks/ })).not.toBeChecked();
    const warning = await screen.findByTestId("allowlist-widening");
    expect(warning).toHaveTextContent("minio.internal.example");
    expect(screen.queryByTestId("allowlist-personal-widening")).toBeNull();
    expect(save).toBeDisabled();
    await user.click(within(warning).getByRole("checkbox"));
    await user.click(save);
    await confirmStepUp(user);
    expect(
      await screen.findByText("Allowlist saved; every bekup process applies it within seconds."),
    ).toBeInTheDocument();
    expect(callsOf("AdminUpdateSsrfAllowlist").at(-1)?.variables["input"]).toEqual({
      cidrEntries: [{ value: "10.20.0.0/16", scopes: DEFAULTS }],
      hostEntries: [{ value: "minio.internal.example", scopes: DEFAULTS }],
      expectedUpdatedAt: "2026-10-27T10:00:00Z",
    });
    await waitFor(() => {
      expect(screen.queryByTestId("allowlist-widening")).toBeNull();
    });
  });

  it("asks for a separate acknowledgement before granting personal webhooks and shows the badge after", async () => {
    const { user, callsOf } = renderApp(
      "/admin/settings/network",
      withStepUp(
        {
          AdminSsrfAllowlist: () => ok({ ssrfAllowlist: allowlist() }),
          AdminUpdateSsrfAllowlist: (v) => {
            const input = v["input"] as { cidrEntries: EntryInput[]; hostEntries: EntryInput[] };
            return ok({
              updateSsrfAllowlist: allowlist({
                cidrEntries: input.cidrEntries.map((e) => entry(e.value, e.scopes)),
                personalWebhooksWidened: true,
                updatedAt: "2026-10-28T10:00:00Z",
              }),
            });
          },
        },
        ["AdminUpdateSsrfAllowlist"],
      ),
    );
    const uses = await screen.findByRole("group", { name: "Uses of CIDR 1" });
    await user.click(within(uses).getByRole("checkbox", { name: /Personal webhooks/ }));
    const general = await screen.findByTestId("allowlist-widening");
    expect(general).toHaveTextContent("Personal webhooks");
    const personal = screen.getByTestId("allowlist-personal-widening");
    expect(personal).toHaveTextContent("10.20.0.0/16");
    expect(personal).toHaveTextContent(/every account/);
    const save = screen.getByRole("button", { name: "Save allowlist" });
    await user.click(within(general).getByRole("checkbox"));
    expect(save).toBeDisabled();
    await user.click(within(personal).getByRole("checkbox"));
    expect(save).toBeEnabled();
    await user.click(save);
    await confirmStepUp(user);
    expect(await screen.findByTestId("personal-widened-badge")).toHaveTextContent(
      "Personal webhooks widened",
    );
    expect(screen.getByTestId("personal-widened-note")).toBeInTheDocument();
    expect(callsOf("AdminUpdateSsrfAllowlist").at(-1)?.variables["input"]).toMatchObject({
      cidrEntries: [{ value: "10.20.0.0/16", scopes: [...DEFAULTS, "PERSONAL_WEBHOOKS"] }],
    });
  });

  it("blocks an entry without scopes and narrows without a warning", async () => {
    const { user } = renderApp("/admin/settings/network", {
      ...baseHandlers(),
      AdminSsrfAllowlist: () => ok({ ssrfAllowlist: allowlist() }),
    });
    const uses = await screen.findByRole("group", { name: "Uses of CIDR 1" });
    await user.click(within(uses).getByRole("checkbox", { name: /Destinations/ }));
    const save = screen.getByRole("button", { name: "Save allowlist" });
    expect(save).toBeEnabled();
    expect(screen.queryByTestId("allowlist-widening")).toBeNull();
    for (const name of [/SMTP/, /Shared webhooks/]) {
      await user.click(within(uses).getByRole("checkbox", { name }));
    }
    expect(screen.getByText("Pick at least one use this entry allows.")).toBeInTheDocument();
    expect(save).toBeDisabled();
  });

  it("puts VALIDATION errors on their row and scopes and offers a reload on CONFLICT", async () => {
    let call = 0;
    const { user } = renderApp("/admin/settings/network", {
      ...baseHandlers(),
      AdminSsrfAllowlist: () => ok({ ssrfAllowlist: allowlist({ ignoredEntries: 1 }) }),
      AdminUpdateSsrfAllowlist: () => {
        call++;
        if (call === 1) {
          return gqlError(
            "VALIDATION",
            'CIDR "169.254.0.0/16" overlaps the always-denied range',
            "cidrEntries.1.value",
          );
        }
        if (call === 2) {
          return gqlError("VALIDATION", "unknown scope for this entry", "cidrEntries.0.scopes");
        }
        return gqlError("CONFLICT", "the allowlist was changed in the meantime");
      },
    });
    expect(await screen.findByText("Some stored entries are not applied")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Add CIDR" }));
    await user.type(screen.getByLabelText("CIDR 2"), "169.254.0.0/16");
    await user.click(within(screen.getByTestId("allowlist-widening")).getByRole("checkbox"));
    await user.click(screen.getByRole("button", { name: "Save allowlist" }));
    expect(await screen.findByText(/overlaps the always-denied range/)).toBeInTheDocument();
    expect(screen.getByLabelText("CIDR 2")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByLabelText("CIDR 1")).not.toHaveAttribute("aria-invalid");

    await user.click(screen.getByRole("button", { name: "Save allowlist" }));
    expect(await screen.findByText("unknown scope for this entry")).toBeInTheDocument();
    expect(screen.getByRole("group", { name: "Uses of CIDR 1" })).toHaveAttribute(
      "aria-invalid",
      "true",
    );
    expect(screen.getByLabelText("CIDR 1")).not.toHaveAttribute("aria-invalid");

    await user.click(screen.getByRole("button", { name: "Save allowlist" }));
    expect(await screen.findByText("Changed in the meantime")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reload" })).toBeInTheDocument();
  });
});

describe("security alerts", () => {
  it("asks the server for the SECURITY group only", async () => {
    const { user, callsOf } = renderApp("/admin/settings/security-alerts", {
      ...baseHandlers(),
      AdminSecurityAlertTypes: () =>
        ok({
          securityAlertTypes: [
            {
              __typename: "SecurityAlertType",
              kind: "ssrf_allowlist_widened",
              severity: "ERROR",
              description: "The SSRF allowlist widened",
              repeat: "every time",
            },
          ],
        }),
      AdminSecurityInbox: () =>
        ok({
          notifications: {
            __typename: "NotificationConnection",
            edges: [
              {
                __typename: "NotificationEdge",
                cursor: "n1",
                node: {
                  __typename: "Notification",
                  id: "n1",
                  type: "security.system",
                  group: "SECURITY",
                  severity: "ERROR",
                  title: "The SSRF allowlist was widened",
                  body: "Review the change in the audit log.",
                  link: null,
                  createdAt: "2026-10-28T09:00:00Z",
                  readAt: null,
                },
              },
            ],
            pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: "n1" },
          },
        }),
    });
    expect(await screen.findByText("The SSRF allowlist was widened")).toBeInTheDocument();
    // The alert reference comes from the server, not a hard-coded list.
    const row = (await screen.findByText("ssrf_allowlist_widened")).closest("tr") as HTMLElement;
    expect(within(row).getByText("The SSRF allowlist widened")).toBeInTheDocument();
    expect(within(row).getByText("every time")).toBeInTheDocument();
    expect(callsOf("AdminSecurityInbox")[0]?.variables).toEqual({
      filter: { group: "SECURITY", unreadOnly: false },
      first: 50,
      after: null,
    });
    await user.click(screen.getByRole("button", { name: "Unread only" }));
    await waitFor(() => {
      expect(callsOf("AdminSecurityInbox").at(-1)?.variables["filter"]).toEqual({
        group: "SECURITY",
        unreadOnly: true,
      });
    });
  });
});

describe("recovery", () => {
  it("exports a job's passphrases only after the warning and a step-up", async () => {
    const createObjectURL = vi.fn(() => "blob:fake");
    const revokeObjectURL = vi.fn();
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL, revokeObjectURL }));
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => undefined);

    const { user, callsOf } = renderApp(
      "/admin/settings/recovery",
      withStepUp(
        {
          AdminJobs: jobsResponse,
          ExportJobPassphrases: () =>
            ok({
              exportJobPassphrases: {
                __typename: "JobPassphraseExport",
                fileName: "billing-db-passphrases.txt",
                content: "v1 x\nv2 y\n",
                versions: [1, 2],
              },
            }),
        },
        ["ExportJobPassphrases"],
      ),
    );
    expect(await screen.findByText(/bekup keys export-public/)).toBeInTheDocument();
    await user.click(
      await screen.findByRole("button", { name: "Export the passphrases of Billing DB" }),
    );
    const dlg = await screen.findByRole("dialog", { name: /Export the passphrases of Billing DB/ });
    const confirm = within(dlg).getByRole("button", { name: "Confirm identity and export" });
    expect(confirm).toBeDisabled();
    await user.click(within(dlg).getByLabelText(/this file decrypts the job's backups/));
    await user.click(confirm);
    expect(callsOf("ExportJobPassphrases")).toHaveLength(1);
    expect(click).not.toHaveBeenCalled();

    await confirmStepUp(user);
    expect(await screen.findByText(/Passphrases exported \(2 versions\)/)).toBeInTheDocument();
    expect(callsOf("ExportJobPassphrases")).toHaveLength(2);
    expect(click).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("v1 x")).toBeNull();
    click.mockRestore();
  });
});
