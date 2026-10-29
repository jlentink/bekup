#!/usr/bin/env node
// CSP regression check (task 0.8, see docs/DECISIONS.md "CSP for the web UI").
//
// 1. Builds the UI with the dev-only /design style guide enabled into
//    .csp-check-dist/ (never shipped).
// 2. Serves it with the production Content-Security-Policy.
// 3. Drives headless Chrome through every page and overlay component and
//    fails if the browser reports any CSP violation.
// 4. Opens a same-origin event stream (POST /graphql with Accept:
//    text/event-stream, the subscription transport of task 2.13) the way
//    graphql-sse does: `connect-src 'self'` must allow it and the browser
//    must send its Origin (the server's Origin check; the stub server here
//    refuses any other origin with 403, like serve). A cross-origin stream
//    must be blocked by connect-src.
//
// 5. Answers the JSON GraphQL operations of the sign-in and profile flows
//    with a small fake API, so the walk-through covers the real pages:
//    login → second factor → shell, the step-up dialog, TOTP enrollment
//    with the inline QR code and the one-time recovery codes.
//
// Usage: pnpm csp-check            (uses the installed Google Chrome)
//        CHROME_PATH=/path/to/chrome pnpm csp-check
import { spawnSync } from "node:child_process";
import { existsSync, readFileSync, statSync } from "node:fs";
import { createServer } from "node:http";
import { extname, join, normalize, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

import { chromium } from "playwright-core";

const STEP_TIMEOUT_MS = 10_000;

/** Keep in sync with docs/DECISIONS.md and internal/httpserver (task 1.1). */
export const CSP = [
  "default-src 'self'",
  "script-src 'self'",
  "style-src 'self'",
  "img-src 'self'",
  "font-src 'self'",
  "connect-src 'self'",
  "object-src 'none'",
  "base-uri 'none'",
  "form-action 'self'",
  "frame-ancestors 'none'",
  "worker-src 'none'",
  "manifest-src 'self'",
  "require-trusted-types-for 'script'",
  "trusted-types 'none'",
].join("; ");

const root = resolve(fileURLToPath(new URL("..", import.meta.url)));
const outDir = join(root, ".csp-check-dist");

function build() {
  const res = spawnSync("pnpm", ["exec", "vite", "build", "--logLevel", "warn"], {
    cwd: root,
    stdio: "inherit",
    env: { ...process.env, VITE_ENABLE_DESIGN: "true", BEKUP_WEB_OUT_DIR: outDir },
  });
  if (res.status !== 0) throw new Error("vite build failed");
}

function assertNoInlineCode() {
  const html = readFileSync(join(outDir, "index.html"), "utf8");
  const problems = [];
  if (/<script(?![^>]*\bsrc=)[^>]*>/i.test(html)) problems.push("inline <script> in index.html");
  if (/<style[\s>]/i.test(html)) problems.push("inline <style> in index.html");
  if (/\sstyle=/i.test(html)) problems.push("style= attribute in index.html");
  if (/\son[a-z]+=/i.test(html)) problems.push("inline event handler in index.html");
  return problems;
}

const TYPES = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".svg": "image/svg+xml",
  ".woff2": "font/woff2",
  ".woff": "font/woff",
};

/** Origins seen on stream requests (the page must send its own). */
const streamOrigins = [];

/**
 * Stub of serve's event stream endpoint: POST /graphql with Accept:
 * text/event-stream and the exact page Origin gets one next event and the
 * complete event; any other Origin gets 403 (serve's Origin check).
 */
function serveStream(req, res, allowedOrigin) {
  const origin = req.headers.origin;
  streamOrigins.push(origin ?? "");
  const accept = String(req.headers.accept ?? "");
  if (req.method !== "POST" || !accept.includes("text/event-stream")) {
    res.writeHead(405).end();
    return;
  }
  if (origin !== allowedOrigin) {
    res
      .writeHead(403, { "Content-Type": "application/json" })
      .end('{"errors":[{"message":"origin not allowed"}]}');
    return;
  }
  req.resume();
  req.on("end", () => {
    res.writeHead(200, {
      "Content-Type": "text/event-stream; charset=utf-8",
      "Cache-Control": "no-store",
      "X-Accel-Buffering": "no",
      "Content-Security-Policy": CSP,
    });
    res.write(":\n\n");
    res.write('event: next\ndata: {"data":{"sessionEvents":{"kind":"REVOKED"}}}\n\n');
    res.end("event: complete\ndata:\n\n");
  });
}

/** State of the fake API; reset at the start of every walk-through. */
const api = { signedIn: false, steppedUp: false };

const CONFIG_REQ = "7d1e0c55-0000-4000-8000-0000000000f1";

function configResult(over = {}) {
  return {
    __typename: "ConfigProcessResult",
    instanceId: "7d1e0c55-0000-4000-8000-0000000000e1",
    kind: "SERVE",
    hostname: "serve-1a2b",
    version: "1.4.0",
    processStartedAt: new Date(Date.now() - 86_400_000).toISOString(),
    finishedAt: new Date(Date.now() - 3_600_000).toISOString(),
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

function configRequest(results) {
  return {
    __typename: "ConfigReloadRequest",
    id: CONFIG_REQ,
    requestedAt: new Date().toISOString(),
    trigger: "UI",
    listeningProcesses: 2,
    expectedProcesses: 2,
    results,
  };
}

function resetApi() {
  api.signedIn = false;
  api.steppedUp = false;
}

const ME = {
  __typename: "User",
  id: "6f1c3e0a-0000-4000-8000-000000000001",
  email: "ada@example.com",
  displayName: "Ada Lovelace",
  role: "ADMIN",
  passwordChangeRequired: false,
  twoFactorRequired: true,
  twoFactorEnrollmentDeadline: new Date(Date.now() + 5 * 86_400_000).toISOString(),
};

/** A QR document in exactly the shape of internal/auth/qrsvg.go. */
function qrSvg() {
  const size = 29;
  let d = "";
  for (let y = 4; y < 25; y++) d += `M4 ${y}h${(y % 7) + 1}v1h-${(y % 7) + 1}z`;
  return (
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${size} ${size}" shape-rendering="crispEdges" role="img" aria-label="QR code for your authenticator app">` +
    `<rect width="${size}" height="${size}" fill="#ffffff"/><path fill="#000000" d="${d}"/></svg>`
  );
}

const err = (code, message) => ({ data: null, errors: [{ message, extensions: { code } }] });

/** Connections and destinations pages (tasks 6.3, 6.4). */
const INFRA_CONNECTION = {
  __typename: "Connection",
  id: "6f1c3e0a-0000-4000-8000-0000000000c1",
  name: "orders-db",
  engine: "POSTGRES",
  versionKey: "17",
  legacyVersion: false,
  host: "db.internal.example",
  port: 5432,
  tlsMode: "PINNED",
  insecureTransport: false,
  pinStatus: "MISSING",
  pinExpiresAt: null,
  sshEnabled: false,
  ready: false,
  notReadyReason: "no confirmed certificate pin",
  maxConcurrentRuns: null,
  effectiveMaxConcurrentRuns: 2,
  createdAt: "2026-10-01T10:00:00Z",
  updatedAt: "2026-10-02T10:00:00Z",
  lastTest: {
    __typename: "ConnectionLastTest",
    at: "2026-10-02T11:00:00Z",
    ok: false,
    code: "TLS_PIN_MISMATCH",
    stale: true,
    privileges: {
      __typename: "TargetPrivileges",
      status: "PRIVILEGED",
      reasons: ["superuser"],
      acknowledgementRequired: true,
    },
  },
  settings: {
    __typename: "ConnectionSettings",
    imageKey: "17",
    username: "backup",
    hasPassword: true,
    clientCertificatePem: null,
    tlsPin: null,
    backupPin: null,
    ssh: null,
  },
};

const INFRA_CERT = {
  __typename: "ChainCertificate",
  pem: "-----BEGIN CERTIFICATE-----",
  notBefore: "2026-01-01T00:00:00Z",
  notAfter: "2030-01-01T00:00:00Z",
  ipAddresses: [],
  spkiSha256Hex: "ab42df",
};

const INFRA_OPERATIONS = {
  ConnectionsList: () => ({ data: { connections: [INFRA_CONNECTION] } }),
  ConnectionDetail: () => ({ data: { connection: INFRA_CONNECTION } }),
  FetchConnectionCertificate: () => ({
    data: {
      fetchCertificate: {
        __typename: "CertificateChain",
        suggestedAnchorIndex: 1,
        leafSpkiSha256: "q0Lf1kPq3m4T2aH7yX9vZ1bC8dE5fG6hJ7kL8mN9oP0=",
        certificates: [
          {
            ...INFRA_CERT,
            subject: "CN=db.internal.example",
            issuer: "CN=Acme Private Root",
            dnsNames: ["db.internal.example"],
            isCa: false,
            selfSigned: false,
            spkiSha256: "q0Lf1kPq3m4T2aH7yX9vZ1bC8dE5fG6hJ7kL8mN9oP0=",
            fingerprintSha256: "aa11",
          },
          {
            ...INFRA_CERT,
            subject: "CN=Acme Private Root",
            issuer: "CN=Acme Private Root",
            dnsNames: [],
            isCa: true,
            selfSigned: true,
            spkiSha256: "Zm9vYmFy",
            fingerprintSha256: "bb22",
          },
        ],
      },
    },
  }),
  DestinationsList: () => ({ data: { destinations: [] } }),
};

/* Restore wizard (6.7) and calendar (6.11): just enough data to walk them. */
const RC_IDS = {
  artifact: "a0000000-0000-4000-8000-000000000001",
  job: "b0000000-0000-4000-8000-000000000001",
  run: "d0000000-0000-4000-8000-000000000001",
  target: "c0000000-0000-4000-8000-0000000000c1",
  restore: "e0000000-0000-4000-8000-000000000001",
};
const RC_PRIVILEGED = {
  __typename: "TargetPrivileges",
  status: "PRIVILEGED",
  reasons: ["superuser"],
  acknowledgementRequired: true,
};
const RC_SOURCE = {
  __typename: "RestoreSource",
  artifactId: RC_IDS.artifact,
  jobId: RC_IDS.job,
  runId: RC_IDS.run,
  kind: "DATABASE",
  database: "shop",
  engine: "POSTGRES",
  versionKey: "17",
  serverVersion: "17.2",
  needsIdentity: true,
};
const RC_RESTORE = {
  __typename: "Restore",
  id: RC_IDS.restore,
  job: {
    __typename: "RunJobRef",
    id: RC_IDS.job,
    name: "Shop nightly",
    slug: "shop",
    deleted: false,
  },
  artifactId: RC_IDS.artifact,
  runId: RC_IDS.run,
  kind: "DATABASE",
  sourceDatabase: "shop",
  target: {
    __typename: "RestoreTargetRef",
    id: RC_IDS.target,
    name: "pg-main",
    engine: "POSTGRES",
    temporary: false,
    deleted: false,
  },
  targetDatabase: "shop_copy",
  mode: "NEW_DB",
  keepOwners: false,
  status: "SUCCEEDED",
  requestedBy: null,
  createdAt: new Date().toISOString(),
  startedAt: new Date().toISOString(),
  finishedAt: new Date().toISOString(),
  durationSeconds: 42,
  stagedBytes: 1_200_000,
  errorCode: null,
  errorMessage: null,
  warnings: [
    {
      __typename: "RestoreOutcomeWarning",
      code: "secdef_neutralized",
      message: "SECURITY DEFINER functions were switched to SECURITY INVOKER.",
    },
  ],
  logLines: 1,
  logLinesDropped: 0,
  updatedAt: new Date().toISOString(),
};
const rcEvent = (over) => ({
  __typename: "CalendarEvent",
  jobId: RC_IDS.job,
  jobName: "Shop nightly",
  kind: "PLANNED",
  start: new Date().toISOString(),
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
});

const RESTORE_CALENDAR_OPERATIONS = {
  RestoreTargets: () => ({
    data: {
      restoreTargets: {
        __typename: "RestoreTargets",
        source: RC_SOURCE,
        targets: [
          {
            __typename: "RestoreTarget",
            id: RC_IDS.target,
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
          },
        ],
      },
    },
  }),
  RestorePreview: () => ({
    data: {
      restorePreview: {
        __typename: "RestorePreview",
        mode: "NEW_DB",
        targetDatabase: "shop_copy",
        warnings: [
          {
            __typename: "RestoreWarning",
            code: "privileged_target",
            message: "The target role is privileged.",
          },
          {
            __typename: "RestoreWarning",
            code: "client_version",
            message: "Restored with the newer client.",
          },
          {
            __typename: "RestoreWarning",
            code: "restore_hardening",
            message: "bekup hardens the restored database.",
          },
        ],
        confirmText: null,
        crossSource: true,
        targetPrivileges: RC_PRIVILEGED,
        target: {
          __typename: "RestoreTarget",
          id: RC_IDS.target,
          name: "pg-main",
          engine: "POSTGRES",
          temporary: false,
          insecureTransport: false,
        },
        source: RC_SOURCE,
      },
    },
  }),
  RestoreFactorStatus: () => ({
    data: {
      myTwoFactorStatus: { __typename: "TwoFactorStatus", totpEnabled: true },
      myPasskeys: [],
    },
  }),
  RestoreArtifact: () => ({ data: { restoreArtifact: RC_RESTORE } }),
  RestoreDetail: () => ({ data: { restore: RC_RESTORE } }),
  RestoreLogs: () => ({
    data: {
      restoreLogs: {
        __typename: "RestoreLogPage",
        restoreId: RC_IDS.restore,
        lines: [
          {
            __typename: "RunLogLine",
            id: "1",
            time: new Date().toISOString(),
            level: "WARN",
            stream: "WORKER",
            line: "switched to SECURITY INVOKER: public.esc()",
          },
        ],
        endId: "1",
        hasMore: false,
        dropped: 0,
      },
    },
  }),
  RestoreJobs: () => ({
    data: {
      jobs: {
        __typename: "JobConnection",
        edges: [],
        pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: null },
      },
    },
  }),
  RestoreHistory: () => ({
    data: {
      restores: {
        __typename: "RestoreConnection",
        edges: [{ __typename: "RestoreEdge", node: RC_RESTORE }],
        pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: null },
      },
    },
  }),
  CalendarJobs: () => ({
    data: {
      jobs: {
        __typename: "JobConnection",
        edges: [],
        pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: null },
      },
    },
  }),
  Calendar: () => ({
    data: {
      calendar: {
        __typename: "Calendar",
        events: [
          rcEvent({}),
          rcEvent({ kind: "PLANNED", paused: true }),
          rcEvent({ kind: "RUN", status: "RUNNING", runId: RC_IDS.run }),
          rcEvent({ kind: "SKIPPED", detail: "job disabled" }),
          rcEvent({ kind: "EXPIRY", projected: true, runId: RC_IDS.run }),
        ],
        truncated: true,
        from: new Date().toISOString(),
        to: new Date().toISOString(),
        timezone: "UTC",
      },
    },
  }),
};

/** Fake API of the job and run pages (tasks 6.5, 6.6). */
const JR_IDS = {
  job: "0b6c3f7e-0000-4000-8000-0000000000b1",
  run: "0b6c3f7e-0000-4000-8000-0000000000b2",
  conn: "0b6c3f7e-0000-4000-8000-0000000000b3",
  dest: "0b6c3f7e-0000-4000-8000-0000000000b4",
  art: "0b6c3f7e-0000-4000-8000-0000000000b5",
};
const jrSoon = (h) => new Date(Date.now() + h * 3_600_000).toISOString();
const JR_SCHEDULE = {
  __typename: "Schedule",
  kind: "INTERVAL",
  every: 1,
  unit: "DAYS",
  anchor: "2026-10-01T03:00",
  weekday: null,
  dayOfMonth: null,
  cron: null,
  timezone: "UTC",
};
const JR_WARNING = (code, message, database = null) => ({
  __typename: "RunWarning",
  code,
  message,
  database,
  destinationId: null,
});
const JR_JOB = {
  __typename: "Job",
  id: JR_IDS.job,
  name: "Billing DB",
  slug: "billing-db",
  description: "Nightly backup of the billing cluster.",
  enabled: true,
  nextRunAt: jrSoon(6),
  lastScheduledAt: jrSoon(-18),
  scheduleDescription: "Every day at 03:00 (UTC)",
  schedule: JR_SCHEDULE,
  connection: {
    __typename: "JobConnectionRef",
    id: JR_IDS.conn,
    name: "billing-pg",
    engine: "POSTGRES",
    versionKey: "17",
    deleted: false,
  },
  dbMode: "ALL",
  databases: [],
  exclude: ["scratch"],
  includeGlobals: true,
  retention: { __typename: "JobRetention", keepLast: 7, keepDays: 30 },
  timeoutSeconds: null,
  verification: "DEEP",
  destinations: [
    {
      __typename: "JobDestinationRef",
      id: JR_IDS.dest,
      name: "vault-s3",
      type: "S3",
      position: 0,
      deleted: false,
    },
  ],
  encryption: {
    __typename: "JobEncryption",
    configured: true,
    mode: "PASSPHRASE",
    optedOut: false,
    optedOutAt: null,
    optOutReason: null,
    passphrase: { __typename: "JobPassphraseInfo", version: 1, createdAt: jrSoon(-400) },
    recipient: null,
    updatedAt: jrSoon(-400),
  },
  owners: [
    {
      __typename: "JobOwner",
      userId: "6f1c3e0a-0000-4000-8000-0000000000c9",
      email: "olive@example.com",
      displayName: "Olive Owner",
      role: "USER",
      disabled: false,
      addedAt: jrSoon(-300),
    },
  ],
  lastRun: {
    __typename: "Run",
    id: JR_IDS.run,
    status: "PARTIAL",
    queuedAt: jrSoon(-18),
    finishedAt: jrSoon(-17.9),
    warnings: [{ __typename: "RunWarning", code: "insufficient_privileges" }],
  },
  testRestore: {
    __typename: "JobTestRestore",
    enabled: true,
    scheduleDescription: "Every week on Sunday at 04:00 (UTC)",
    schedule: {
      __typename: "Schedule",
      kind: "CRON",
      every: null,
      unit: null,
      anchor: null,
      weekday: null,
      dayOfMonth: null,
      cron: "0 4 * * 0",
      timezone: "UTC",
    },
    nextAt: jrSoon(72),
    target: {
      __typename: "JobConnectionRef",
      id: JR_IDS.dest.replace("b4", "ba"),
      name: "scratch-pg",
      engine: "POSTGRES",
      deleted: false,
    },
    targetPrivileges: {
      __typename: "TargetPrivileges",
      status: "UNKNOWN",
      reasons: ["roles_granted"],
      acknowledgementRequired: true,
    },
    privilegedTargetAcknowledgedAt: jrSoon(-200),
  },
  createdAt: jrSoon(-400),
  updatedAt: jrSoon(-400),
};
const JR_ARTIFACT = {
  __typename: "Artifact",
  id: JR_IDS.art,
  kind: "DATABASE",
  state: "PRESENT",
  verified: true,
  signed: true,
  size: 262144,
  sha256: "cd".repeat(32),
  objectKey: "billing-db/20261028T030002Z/billing.dump.zst.age",
  lockUntil: null,
  stateChangedAt: null,
  encryption: {
    __typename: "ArtifactEncryption",
    mode: "RECIPIENT",
    passphraseVersion: null,
    recipientVersion: 1,
  },
  destination: {
    __typename: "ArtifactDestinationRef",
    id: JR_IDS.dest,
    name: "vault-s3",
    type: "S3",
    deleted: false,
  },
};
const JR_TEST_RESTORE = {
  __typename: "TestRestore",
  id: "0b6c3f7e-0000-4000-8000-0000000000b8",
  runId: JR_IDS.run,
  status: "WARNINGS",
  trigger: "SCHEDULE",
  requestedBy: null,
  target: {
    __typename: "RestoreTargetRef",
    id: JR_IDS.dest.replace("b4", "ba"),
    name: "scratch-pg",
    engine: "POSTGRES",
    temporary: false,
    deleted: false,
  },
  scheduledFor: jrSoon(-10.3),
  createdAt: jrSoon(-10.3),
  startedAt: jrSoon(-10.2),
  finishedAt: jrSoon(-10),
  durationSeconds: 720,
  errorCode: null,
  errorMessage: null,
  databaseCount: 1,
  passed: 0,
  warned: 1,
  failed: 0,
};
const JR_RUN = {
  __typename: "Run",
  id: JR_IDS.run,
  status: "PARTIAL",
  trigger: "EARLY",
  triggeredBy: {
    __typename: "RunActor",
    userId: "u",
    displayName: "Ada Lovelace",
    email: "ada@example.com",
  },
  scheduledFor: jrSoon(6),
  attempt: 1,
  queuedAt: jrSoon(-18),
  startedAt: jrSoon(-18),
  finishedAt: jrSoon(-17.9),
  durationSeconds: 238,
  storageTimestamp: jrSoon(-18),
  errorCode: null,
  errorMessage: null,
  warnings: [JR_WARNING("insufficient_privileges", "2 tables could not be read", "billing")],
  notices: [JR_WARNING("databases_excluded", "1 database left out by the exclude list")],
  bytesRaw: 1048576,
  bytesStored: 262144,
  cancelRequested: false,
  cancelRequestedAt: null,
  logLines: 3,
  logLinesDropped: 0,
  updatedAt: jrSoon(-17.9),
  job: {
    __typename: "RunJobRef",
    id: JR_IDS.job,
    name: "Billing DB",
    slug: "billing-db",
    deleted: false,
  },
  items: [
    {
      __typename: "RunItem",
      id: "0b6c3f7e-0000-4000-8000-0000000000b6",
      kind: "DATABASE",
      database: "billing",
      position: 0,
      status: "SUCCESS",
      rawSize: 1048576,
      storedSize: 262144,
      sha256: "ab".repeat(32),
      verified: true,
      startedAt: jrSoon(-18),
      finishedAt: jrSoon(-17.9),
      durationSeconds: 238,
      errorCode: null,
      errorMessage: null,
      warnings: [JR_WARNING("insufficient_privileges", "2 tables could not be read", "billing")],
      artifacts: [JR_ARTIFACT],
    },
  ],
  manifest: {
    __typename: "RunManifest",
    signed: true,
    signingKeyId: "k1",
    coveredItems: 1,
    totalItems: 1,
    copies: [],
  },
};
const JR_PREVIEW = (kind = "INTERVAL") => ({
  data: {
    previewSchedule: {
      __typename: "SchedulePreview",
      description: "Every day at 03:00 (UTC)",
      next: [6, 30, 54, 78, 102].map(jrSoon),
      schedule: { __typename: "Schedule", kind, timezone: "UTC" },
    },
  },
});

const JOBS_RUNS_OPERATIONS = {
  JobsList: () => ({
    data: {
      jobs: {
        __typename: "JobConnection",
        edges: [
          { __typename: "JobEdge", cursor: "c1", node: JR_JOB },
          {
            __typename: "JobEdge",
            cursor: "c2",
            node: {
              ...JR_JOB,
              id: "0b6c3f7e-0000-4000-8000-0000000000b7",
              name: "Legacy CRM",
              slug: "legacy-crm",
              enabled: false,
              encryption: { ...JR_JOB.encryption, mode: "NONE", optedOut: true },
              lastRun: null,
            },
          },
        ],
        pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: null },
      },
    },
  }),
  JobFilterConnections: () => ({
    data: {
      connections: [
        {
          __typename: "Connection",
          id: JR_IDS.conn,
          name: "billing-pg",
          engine: "POSTGRES",
          temporary: false,
        },
      ],
    },
  }),
  JobDetail: () => ({ data: { job: JR_JOB } }),
  JobSharedChannelAttachments: () => ({
    data: {
      job: {
        __typename: "Job",
        id: JR_IDS.job,
        sharedChannelAttachments: [
          {
            __typename: "JobSharedChannelAttachment",
            channelId: "0b6c3f7e-0000-4000-8000-0000000000c9",
            name: "Ops chat",
            type: "WEBHOOK",
            displayUrl: "https://hooks.example.com:443",
            enabled: false,
            groups: ["BACKUP"],
            events: [],
          },
        ],
      },
    },
  }),
  LatestTestRestore: () => ({
    data: {
      testRestores: {
        __typename: "TestRestoreConnection",
        edges: [
          {
            __typename: "TestRestoreEdge",
            node: {
              __typename: "TestRestore",
              id: "0b6c3f7e-0000-4000-8000-0000000000b8",
              runId: JR_IDS.run,
              status: "WARNINGS",
              finishedAt: jrSoon(-10),
              createdAt: jrSoon(-10),
              databaseCount: 1,
              passed: 0,
              warned: 1,
              failed: 0,
              errorMessage: null,
            },
          },
        ],
      },
    },
  }),
  JobEditorOptions: () => ({
    data: {
      connections: [
        {
          __typename: "Connection",
          id: JR_IDS.conn,
          name: "billing-pg",
          engine: "POSTGRES",
          versionKey: "17",
          temporary: false,
        },
      ],
      destinations: [
        {
          __typename: "Destination",
          id: JR_IDS.dest,
          name: "vault-s3",
          type: "S3",
          ready: true,
          notReadyReason: null,
        },
      ],
    },
  }),
  PreviewSchedule: () => JR_PREVIEW(),
  JobNextOccurrences: () => JR_PREVIEW(),
  UserPickerCandidates: () => ({
    data: {
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
    },
  }),
  TestRestoreTargets: () => ({
    data: {
      connections: [
        {
          __typename: "Connection",
          id: JR_IDS.dest.replace("b4", "ba"),
          name: "scratch-pg",
          engine: "POSTGRES",
          temporary: false,
          ready: true,
          lastTest: {
            __typename: "ConnectionLastTest",
            at: jrSoon(-5),
            ok: true,
            stale: false,
            privileges: {
              __typename: "TargetPrivileges",
              status: "UNKNOWN",
              reasons: ["roles_granted"],
              acknowledgementRequired: true,
            },
          },
        },
      ],
    },
  }),
  JobTestRestores: () => ({
    data: {
      testRestores: {
        __typename: "TestRestoreConnection",
        edges: [{ __typename: "TestRestoreEdge", cursor: "t1", node: JR_TEST_RESTORE }],
        pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: null },
      },
    },
  }),
  TestRestoreDetail: () => ({
    data: {
      testRestore: {
        ...JR_TEST_RESTORE,
        job: {
          __typename: "RunJobRef",
          id: JR_IDS.job,
          name: "Billing DB",
          slug: "billing-db",
          deleted: false,
        },
        databases: [
          {
            __typename: "TestRestoreDatabase",
            id: "0b6c3f7e-0000-4000-8000-0000000000b9",
            runItemId: "0b6c3f7e-0000-4000-8000-0000000000bb",
            database: "billing",
            restoreId: null,
            status: "WARNINGS",
            errorCode: null,
            errorMessage: null,
            scratchDropped: true,
            findingsTotal: 1,
            tablesCompared: 42,
            exactTables: 40,
            restoreStatus: null,
            restoreErrorCode: null,
            startedAt: jrSoon(-10.2),
            finishedAt: jrSoon(-10),
            findings: [
              {
                __typename: "TestRestoreFinding",
                severity: "WARN",
                code: "row_count_delta",
                object: "public.invoices",
                source: 1200,
                restored: 1199,
                delta: -1,
              },
            ],
          },
        ],
      },
    },
  }),
  RunItemStats: () => ({
    data: {
      run: {
        __typename: "Run",
        id: JR_IDS.run,
        items: JR_RUN.items.map((item) => ({
          __typename: "RunItem",
          id: item.id,
          kind: item.kind,
          database: item.database,
          stats: {
            __typename: "RunItemStats",
            level: "DEEP",
            consistency: "SNAPSHOT",
            objects: [
              { __typename: "StatsObjectCount", kind: "tables", count: 42 },
              { __typename: "StatsObjectCount", kind: "views", count: 3 },
            ],
            tables: [
              {
                __typename: "TableStats",
                name: "public.ledger_entries",
                rows: 1200,
                count: "EXACT",
                exact: true,
                estimate: false,
                unknown: false,
                approximate: false,
              },
              {
                __typename: "TableStats",
                name: "audit.trail",
                rows: null,
                count: "UNKNOWN",
                exact: false,
                estimate: false,
                unknown: true,
                approximate: false,
              },
            ],
            tablesTotal: 42,
            tablesTruncated: true,
            warnings: [
              {
                __typename: "StatsFinding",
                code: "insufficient_privileges",
                count: 2,
                objects: ["private.t1", "private.t2"],
                truncated: false,
              },
            ],
            notices: [
              {
                __typename: "StatsFinding",
                code: "databases_excluded",
                count: 1,
                objects: ["scratch"],
                truncated: false,
              },
            ],
            recordedAt: jrSoon(-17.95),
          },
        })),
      },
    },
  }),
  RunsList: () => ({
    data: {
      runs: {
        __typename: "RunConnection",
        edges: [{ __typename: "RunEdge", cursor: "r1", node: JR_RUN }],
        pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: null },
      },
    },
  }),
  RunDetail: () => ({ data: { run: JR_RUN } }),
  RunJobContext: () => ({
    data: {
      job: {
        __typename: "Job",
        id: JR_IDS.job,
        verification: "DEEP",
        connection: { __typename: "JobConnectionRef", id: JR_IDS.conn, engine: "POSTGRES" },
      },
    },
  }),
  RunTestRestores: () => ({
    data: {
      testRestores: {
        __typename: "TestRestoreConnection",
        edges: [
          {
            __typename: "TestRestoreEdge",
            node: {
              __typename: "TestRestore",
              id: "0b6c3f7e-0000-4000-8000-0000000000b8",
              runId: JR_IDS.run,
              status: "WARNINGS",
              finishedAt: jrSoon(-10),
            },
          },
        ],
      },
    },
  }),
  TestRestoreFindings: () => ({
    data: {
      testRestore: {
        __typename: "TestRestore",
        id: "0b6c3f7e-0000-4000-8000-0000000000b8",
        status: "WARNINGS",
        finishedAt: jrSoon(-10),
        errorMessage: null,
        databaseCount: 1,
        passed: 0,
        warned: 1,
        failed: 0,
        databases: [
          {
            __typename: "TestRestoreDatabase",
            id: "0b6c3f7e-0000-4000-8000-0000000000b9",
            database: "billing",
            status: "WARNINGS",
            errorMessage: null,
            tablesCompared: 42,
            exactTables: 40,
            findingsTotal: 1,
            findings: [
              {
                __typename: "TestRestoreFinding",
                severity: "WARN",
                code: "row_count_delta",
                object: "public.invoices",
                source: 1200,
                restored: 1199,
                delta: -1,
              },
            ],
          },
        ],
      },
    },
  }),
  RunLogs: () => ({
    data: {
      runLogs: {
        __typename: "RunLogPage",
        runId: JR_IDS.run,
        endId: "3",
        hasMore: false,
        dropped: 0,
        lines: [
          ["1", "INFO", "WORKER", "run started"],
          ["2", "WARN", "RUNNER", "2 tables could not be read"],
          ["3", "INFO", "WORKER", "run finished"],
        ].map(([id, level, stream, line]) => ({
          __typename: "RunLogLine",
          id,
          time: jrSoon(-18),
          level,
          stream,
          line,
        })),
      },
    },
  }),
};

/** Dashboard (task 6.2): every tile with data, incl. the SVG charts and hazard hatch. */
const dashAgo = (ms) => new Date(Date.now() - ms).toISOString();
const DASH_JOB = (id, name, extra = {}) => ({
  __typename: "Job",
  id,
  name,
  enabled: true,
  nextRunAt: dashAgo(-2 * 3_600_000),
  scheduleDescription: "Every day at 03:00 (UTC)",
  missed: false,
  missedSince: null,
  connection: { __typename: "JobConnectionRef", id: "c1", name: "billing-pg", deleted: false },
  encryption: {
    __typename: "JobEncryption",
    configured: true,
    mode: "PASSPHRASE",
    optedOut: false,
  },
  lastRun: {
    __typename: "Run",
    id: `${id.slice(0, -2)}r9`,
    status: "SUCCESS",
    queuedAt: dashAgo(3_600_000),
    finishedAt: dashAgo(3_500_000),
    warnings: [],
  },
  ...extra,
});
const DASH_RUN = (id, status, extra = {}) => ({
  __typename: "Run",
  id,
  status,
  trigger: "SCHEDULE",
  queuedAt: dashAgo(600_000),
  startedAt: dashAgo(590_000),
  finishedAt: null,
  errorCode: null,
  errorMessage: null,
  job: { __typename: "RunJobRef", id: "0b6c3f7e-0000-4000-8000-0000000000e1", name: "Billing DB" },
  ...extra,
});
const dashRuns = (nodes) => ({
  __typename: "RunConnection",
  edges: nodes.map((node) => ({ __typename: "RunEdge", node })),
  pageInfo: { __typename: "PageInfo", hasNextPage: false },
});

const DASHBOARD_OPERATIONS = {
  DashboardJobs: () => ({
    data: {
      jobs: {
        __typename: "JobConnection",
        edges: [
          DASH_JOB("0b6c3f7e-0000-4000-8000-0000000000e1", "Billing DB", {
            lastRun: {
              ...DASH_JOB("x-0").lastRun,
              id: "0b6c3f7e-0000-4000-8000-0000000000e9",
              status: "RUNNING",
            },
          }),
          DASH_JOB("0b6c3f7e-0000-4000-8000-0000000000e2", "Shop DB", {
            lastRun: {
              ...DASH_JOB("x-0").lastRun,
              id: "0b6c3f7e-0000-4000-8000-0000000000e8",
              status: "FAILED",
            },
          }),
          DASH_JOB("0b6c3f7e-0000-4000-8000-0000000000e3", "Legacy CRM", {
            encryption: {
              __typename: "JobEncryption",
              configured: true,
              mode: "NONE",
              optedOut: true,
            },
            nextRunAt: dashAgo(3_600_000),
          }),
          DASH_JOB("0b6c3f7e-0000-4000-8000-0000000000e4", "Archive", {
            enabled: false,
            lastRun: null,
          }),
        ].map((node) => ({ __typename: "JobEdge", node })),
        pageInfo: { __typename: "PageInfo", hasNextPage: false },
      },
    },
  }),
  DashboardRuns: () => ({
    data: {
      running: dashRuns([DASH_RUN("0b6c3f7e-0000-4000-8000-0000000000e9", "RUNNING")]),
      queued: dashRuns([]),
      failed: dashRuns([
        DASH_RUN("0b6c3f7e-0000-4000-8000-0000000000e8", "FAILED", {
          finishedAt: dashAgo(7_200_000),
          errorCode: "dump_failed",
          errorMessage: "The dump failed.",
          job: {
            __typename: "RunJobRef",
            id: "0b6c3f7e-0000-4000-8000-0000000000e2",
            name: "Shop DB",
          },
        }),
      ]),
    },
  }),
  DashboardCalendar: () => ({
    data: {
      calendar: {
        __typename: "Calendar",
        truncated: false,
        events: [
          ["SKIPPED", dashAgo(5_400_000), "still_running"],
          ["PLANNED", dashAgo(-3 * 3_600_000), null],
          ["PLANNED", dashAgo(-15 * 3_600_000), null],
        ].map(([kind, start, detail]) => ({
          __typename: "CalendarEvent",
          jobId: "0b6c3f7e-0000-4000-8000-0000000000e1",
          jobName: "Billing DB",
          kind,
          start,
          status: null,
          runId: null,
          paused: false,
          startedEarly: false,
          overdue: false,
          detail,
        })),
      },
    },
  }),
  DashboardRestores: () => ({
    data: {
      restores: {
        __typename: "RestoreConnection",
        edges: [
          {
            __typename: "RestoreEdge",
            node: {
              __typename: "Restore",
              id: "0b6c3f7e-0000-4000-8000-0000000000f1",
              status: "RESTORING",
              createdAt: dashAgo(120_000),
              startedAt: dashAgo(100_000),
              sourceDatabase: "billing",
              targetDatabase: "billing_copy",
              kind: "DATABASE",
              job: {
                __typename: "RunJobRef",
                id: "0b6c3f7e-0000-4000-8000-0000000000e1",
                name: "Billing DB",
              },
              target: { __typename: "RestoreTargetRef", id: "c1", name: "billing-pg" },
            },
          },
        ],
      },
    },
  }),
  DashboardAlerts: () => ({
    data: {
      unreadNotificationCount: 2,
      notifications: {
        __typename: "NotificationConnection",
        edges: [
          ["CRITICAL", "Certificate pin mismatch on billing-pg", "/connections"],
          ["WARNING", "Backup of Shop DB failed", null],
        ].map(([severity, title, link], i) => ({
          __typename: "NotificationEdge",
          node: {
            __typename: "Notification",
            id: `0b6c3f7e-0000-4000-8000-0000000000n${i}`,
            type: "backup.failed",
            severity,
            title,
            link,
            createdAt: dashAgo(900_000),
          },
        })),
      },
    },
  }),
  DashboardInfra: () => ({
    data: {
      connections: [
        {
          __typename: "Connection",
          id: "c1",
          name: "billing-pg",
          tlsMode: "UNVERIFIED",
          insecureTransport: true,
          pinStatus: "NOT_REQUIRED",
          pinExpiresAt: null,
          lastTest: {
            __typename: "ConnectionLastTest",
            at: dashAgo(3_600_000),
            ok: true,
            code: null,
            stale: false,
            privileges: { __typename: "TargetPrivileges", status: "PRIVILEGED" },
          },
        },
        {
          __typename: "Connection",
          id: "c2",
          name: "shop-mysql",
          tlsMode: "PINNED",
          insecureTransport: false,
          pinStatus: "EXPIRED",
          pinExpiresAt: dashAgo(86_400_000),
          lastTest: null,
        },
      ],
      destinations: [
        {
          __typename: "Destination",
          id: "d1",
          name: "vault-disk",
          type: "DISK",
          ready: true,
          lastTest: null,
        },
        {
          __typename: "Destination",
          id: "d2",
          name: "offsite-s3",
          type: "S3",
          ready: true,
          lastTest: null,
        },
      ],
    },
  }),
  DashboardStorage: () => ({
    data: {
      destinationUsage: [
        ["d1", "vault-disk", "DISK", 3e9, 42],
        ["d2", "offsite-s3", "S3", 1.2e9, 17],
      ].map(([destinationId, name, type, storedBytes, artifactCount]) => ({
        __typename: "DestinationUsage",
        destinationId,
        name,
        type,
        storedBytes,
        artifactCount,
      })),
    },
  }),
  DashboardSystemHealth: () => ({
    data: {
      systemHealth: {
        __typename: "SystemHealth",
        checkedAt: new Date().toISOString(),
        runner: {
          __typename: "RunnerHealth",
          up: true,
          dockerOk: true,
          version: "1.0.0",
          activeRuns: 1,
          maxConcurrentRuns: 4,
        },
        certificates: [
          {
            __typename: "CertificateExpiry",
            component: "runner_ca",
            notAfter: dashAgo(-400 * 86_400_000),
          },
          {
            __typename: "CertificateExpiry",
            component: "runner_client",
            notAfter: dashAgo(-5 * 86_400_000),
          },
        ],
        queue: [
          { __typename: "QueueDepth", queue: "backup", state: "running", jobs: 1 },
          { __typename: "QueueDepth", queue: "notify", state: "retryable", jobs: 2 },
        ],
        earliestPinNotAfter: dashAgo(-20 * 86_400_000),
        oldestLastSuccess: dashAgo(3_600_000),
        schedulerEnabled: true,
        lastSchedulerTick: dashAgo(30_000),
      },
    },
  }),
};

/* Notification center and settings (6.10): bell, inbox, matrix, webhooks, verify-email. */
const NT_IDS = {
  n1: "6f1c3e0a-0000-4000-8000-0000000000e1",
  n2: "6f1c3e0a-0000-4000-8000-0000000000e2",
  hook: "6f1c3e0a-0000-4000-8000-0000000000f1",
  job: "6f1c3e0a-0000-4000-8000-0000000000b1",
};

function ntNotification(id, title, severity, readAt) {
  return {
    __typename: "Notification",
    id,
    type: "backup.failed",
    group: "BACKUP",
    severity,
    title,
    body: 'The backup of job "Billing DB" failed. Reason: dump_failed.',
    link: `http://127.0.0.1/jobs/${NT_IDS.job}`,
    createdAt: new Date(Date.now() - 600_000).toISOString(),
    readAt,
  };
}

function ntInbox() {
  const nodes = [
    ntNotification(NT_IDS.n1, "Backup failed: Billing DB", "ERROR", null),
    ntNotification(NT_IDS.n2, "Security alert", "CRITICAL", new Date().toISOString()),
  ];
  return {
    unreadNotificationCount: 1,
    notifications: {
      __typename: "NotificationConnection",
      edges: nodes.map((node) => ({ __typename: "NotificationEdge", cursor: node.id, node })),
      pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: null },
    },
  };
}

function ntRow(type, label, group, mandatory) {
  return {
    __typename: "NotificationEventPreference",
    event: {
      __typename: "NotificationEventInfo",
      type,
      label,
      group,
      problem: type.endsWith("failed") || mandatory,
      mandatory,
      priority: type.endsWith("started") ? "LOW" : "NORMAL",
      severity: mandatory ? "CRITICAL" : "ERROR",
      perJob: !mandatory,
    },
    channels: ["INBOX", "EMAIL", "WEBHOOK"].map((channel) => ({
      __typename: "NotificationChannelPreference",
      channel,
      enabled:
        channel === "INBOX" || (channel === "EMAIL" && (mandatory || type.endsWith("failed"))),
      source: mandatory && channel !== "WEBHOOK" ? "MANDATORY" : "DEFAULT",
      locked: mandatory && channel !== "WEBHOOK",
    })),
    jobOverrides: mandatory
      ? []
      : [
          {
            __typename: "NotificationJobOverride",
            jobId: NT_IDS.job,
            jobName: "Billing DB",
            channel: "WEBHOOK",
            enabled: true,
          },
        ],
  };
}

function ntPrefs() {
  const group = (g, label, events) => ({
    __typename: "NotificationPreferenceGroup",
    group: g,
    label,
    events,
  });
  return {
    __typename: "NotificationPreferences",
    emailVerified: false,
    activeWebhooks: 1,
    groups: [
      group("BACKUP", "Backups", [
        ntRow("backup.started", "Backup started", "BACKUP", false),
        ntRow("backup.failed", "Backup failed", "BACKUP", false),
      ]),
      group("SECURITY", "Security", [
        ntRow("security.account", "Security alert", "SECURITY", true),
      ]),
    ],
  };
}

const NT_WEBHOOK = {
  __typename: "PersonalWebhook",
  id: NT_IDS.hook,
  label: "Team Slack",
  displayUrl: "https://hooks.slack.com/…",
  createdAt: "2026-10-01T10:00:00Z",
  state: {
    __typename: "NotificationChannelState",
    enabled: false,
    disabledAt: "2026-10-27T10:00:00Z",
    disabledReason: "permanent_failures",
    disabledMessage: "Disabled after 5 permanent failures.",
    consecutiveFailures: 0,
    permanentFailures: 5,
    lastFailureAt: "2026-10-27T10:00:00Z",
    lastFailureCode: "http_status",
  },
};

const NOTIFICATIONS_OPERATIONS = {
  NotificationBell: () => ({ data: ntInbox() }),
  NotificationsInbox: () => ({ data: ntInbox() }),
  MyNotificationPreferences: () => ({ data: { myNotificationPreferences: ntPrefs() } }),
  MyNotificationChannels: () => ({
    data: {
      myNotificationChannels: {
        __typename: "MyNotificationChannels",
        email: "ada@example.com",
        emailVerified: false,
        emailVerifiedAt: null,
        maxWebhooks: 5,
        webhooks: [NT_WEBHOOK],
      },
    },
  }),
  NotificationJobOptions: () => ({
    data: {
      jobs: {
        __typename: "JobConnection",
        edges: [
          {
            __typename: "JobEdge",
            node: { __typename: "Job", id: NT_IDS.job, name: "Billing DB" },
          },
        ],
      },
    },
  }),
  TestMyWebhook: () => ({
    data: {
      testMyWebhook: {
        __typename: "NotificationTestResult",
        ok: false,
        code: "not_allowed",
      },
    },
  }),
  AddMyWebhook: () => ({
    data: {
      addMyWebhook: {
        __typename: "PersonalWebhookWithKey",
        webhook: {
          __typename: "PersonalWebhook",
          id: NT_IDS.hook,
          label: "Ops hook",
          displayUrl: "https://hooks.example.com/…",
        },
        signingKey: "bkwh_9Qx3kLm2Vt7Rz1Np5Hs8Fd4Gc6Bj0Aw",
      },
    },
  }),
  VerifyEmail: () => ({
    data: {
      verifyEmail: {
        __typename: "User",
        id: ME.id,
        emailVerified: true,
        emailVerifiedAt: new Date().toISOString(),
      },
    },
  }),
};

/* Admin settings (6.9): SMTP, shared channels, audit log, alerts, recovery. */
const AD_IDS = {
  channel: "f0000000-0000-4000-8000-0000000000c1",
  job: "f0000000-0000-4000-8000-0000000000j1",
};
const AD_CHANNEL = {
  __typename: "SharedChannel",
  id: AD_IDS.channel,
  name: "Ops chat",
  type: "WEBHOOK",
  addresses: [],
  displayUrl: "https://hooks.example.com:443",
  state: {
    __typename: "NotificationChannelState",
    enabled: false,
    disabledAt: new Date().toISOString(),
    disabledReason: "repeated_failures",
    disabledMessage: "Turned off after repeated delivery failures.",
    consecutiveFailures: 5,
    permanentFailures: 0,
    lastFailureAt: new Date().toISOString(),
    lastFailureCode: "http_status",
  },
  attachments: [
    {
      __typename: "SharedChannelAttachment",
      jobId: AD_IDS.job,
      jobName: "Billing DB",
      groups: ["BACKUP"],
      events: [],
    },
  ],
  createdAt: "2026-10-01T10:00:00Z",
  updatedAt: "2026-10-02T10:00:00Z",
};
const adEvent = (type, label, group, extra = {}) => ({
  __typename: "NotificationEventInfo",
  type,
  label,
  group,
  mandatory: false,
  perJob: true,
  severity: "INFO",
  ...extra,
});
const ADMIN_OPERATIONS = {
  AdminSmtpSettings: () => ({
    data: {
      smtpSettings: {
        __typename: "SmtpSettings",
        configured: true,
        enabled: true,
        host: "smtp.example.com",
        port: 587,
        mode: "AUTO",
        username: "mailer",
        hasPassword: true,
        fromAddress: "bekup@example.com",
        fromName: "bekup",
        caBundlePem: null,
        connectTimeoutSeconds: 10,
        sendTimeoutSeconds: 60,
        mayBeUnencrypted: true,
        updatedAt: new Date().toISOString(),
      },
    },
  }),
  AdminSendTestEmail: () => ({
    data: {
      sendTestEmail: {
        __typename: "SmtpTestResult",
        ok: false,
        code: "tls_failed",
        message: "The TLS handshake failed.",
        detail: "certificate signed by unknown authority",
        encrypted: false,
        tlsVersion: null,
      },
    },
  }),
  AdminSharedChannels: () => ({ data: { sharedChannels: [AD_CHANNEL] } }),
  AdminSecurityAlertTypes: () => ({
    data: {
      securityAlertTypes: [
        {
          __typename: "SecurityAlertType",
          kind: "ssrf_allowlist_widened",
          severity: "ERROR",
          description: "The SSRF allowlist widened (entries added under Network)",
          repeat: "every time",
        },
      ],
    },
  }),
  AdminNotificationCatalog: () => ({
    data: {
      notificationCatalog: [
        {
          __typename: "NotificationEventGroup",
          group: "BACKUP",
          label: "Backup",
          events: [
            adEvent("backup.failed", "Backup failed", "BACKUP"),
            adEvent("backup.succeeded", "Backup succeeded", "BACKUP"),
          ],
        },
        {
          __typename: "NotificationEventGroup",
          group: "SECURITY",
          label: "Security",
          events: [
            adEvent("security.system", "Security alert", "SECURITY", {
              mandatory: true,
              perJob: false,
            }),
          ],
        },
      ],
    },
  }),
  AdminJobs: () => ({
    data: {
      jobs: {
        __typename: "JobConnection",
        edges: [
          {
            __typename: "JobEdge",
            cursor: "c1",
            node: {
              __typename: "Job",
              id: AD_IDS.job,
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
    },
  }),
  AdminAuditChain: () => ({
    data: {
      auditChainStatus: {
        __typename: "AuditChainStatus",
        lastVerification: {
          __typename: "AuditChainVerification",
          checkedAt: new Date(Date.now() - 20 * 60_000).toISOString(),
          ok: false,
          full: true,
          finding: "hash_mismatch:2",
          verifiedThroughId: "1",
        },
        headId: "3",
        headHash: "9f2c4be1d0a7aa55",
        headAt: new Date().toISOString(),
        latestSignedHead: {
          __typename: "AuditSignedHead",
          chainId: "2",
          signedAt: new Date(Date.now() - 3_600_000).toISOString(),
          keyId: "0011223344556677",
        },
      },
    },
  }),
  AdminConfigStatus: () => ({
    data: {
      configStatus: {
        __typename: "ConfigStatus",
        liveKeys: ["logging.level", "retention.dry_run", "workers.max_concurrent_runs"],
        listeningProcesses: 3,
        processes: [
          configResult({
            status: "RESTART_REQUIRED",
            requestId: "7d1e0c55-0000-4000-8000-0000000000aa",
            trigger: "SIGNAL",
            restartKeys: ["server.listen"],
            appliedKeys: ["logging.level"],
            weakenedKeys: ["logging.level"],
          }),
          configResult({
            instanceId: "7d1e0c55-0000-4000-8000-0000000000e2",
            kind: "WORKER",
            hostname: "worker-9f00",
          }),
          configResult({
            instanceId: "7d1e0c55-0000-4000-8000-0000000000e3",
            kind: "WORKER",
            hostname: "worker-old",
            listening: false,
            requestId: "7d1e0c55-0000-4000-8000-0000000000aa",
            trigger: "UI",
            status: "RESTART_REQUIRED",
            restartKeys: ["server.listen"],
          }),
        ],
        lastRequest: null,
      },
    },
  }),
  AdminReloadConfig: () =>
    api.steppedUp
      ? {
          data: {
            reloadConfig: configRequest([
              configResult({
                status: "APPLIED",
                requestId: CONFIG_REQ,
                appliedKeys: ["logging.level"],
              }),
            ]),
          },
        }
      : err("STEP_UP_REQUIRED", "please confirm your identity again"),
  AdminConfigReloadRequest: () => ({
    data: {
      configReloadRequest: configRequest([
        configResult({ status: "APPLIED", requestId: CONFIG_REQ, appliedKeys: ["logging.level"] }),
        configResult({
          instanceId: "7d1e0c55-0000-4000-8000-0000000000e2",
          kind: "WORKER",
          hostname: "worker-9f00",
          requestId: CONFIG_REQ,
          status: "REJECTED",
          errorKind: "INVALID",
          errorKeys: ["server.unknown_key"],
        }),
      ]),
    },
  }),
  AdminSsrfAllowlist: () => ({
    data: {
      ssrfAllowlist: {
        __typename: "SsrfAllowlist",
        cidrEntries: [
          {
            __typename: "SsrfAllowlistEntry",
            value: "10.20.0.0/16",
            scopes: ["DESTINATIONS", "SMTP", "SHARED_WEBHOOKS", "PERSONAL_WEBHOOKS"],
          },
        ],
        hostEntries: [
          {
            __typename: "SsrfAllowlistEntry",
            value: "minio.internal.example",
            scopes: ["DESTINATIONS"],
          },
        ],
        configScopes: ["DESTINATIONS", "SMTP", "SHARED_WEBHOOKS"],
        defaultScopes: ["DESTINATIONS", "SMTP", "SHARED_WEBHOOKS"],
        personalWebhooksWidened: true,
        configCidrs: ["192.168.10.0/24"],
        configHosts: ["smtp.internal.example"],
        loopbackAllowed: false,
        ignoredEntries: 1,
        maxEntries: 256,
        updatedAt: new Date(Date.now() - 86_400_000).toISOString(),
      },
    },
  }),
  AdminAuditLog: () => ({
    data: {
      auditLog: {
        __typename: "AuditLogConnection",
        edges: ["user.created", "security.opt_out", "auth.login.succeeded"].map((action, i) => ({
          __typename: "AuditLogEdge",
          cursor: `a${i}`,
          node: {
            __typename: "AuditLogEntry",
            id: String(i + 1),
            ts: new Date(Date.now() - i * 60_000).toISOString(),
            actorKind: i === 1 ? "CLI" : "USER",
            actorUserId: i === 1 ? null : ME.id,
            action,
            targetType: "user",
            targetId: "u-1",
            ip: "192.0.2.10",
            details: '{"role":"MAINTAINER"}',
            hash: `9f2c4be1d0a7${i}`,
          },
        })),
        pageInfo: { __typename: "PageInfo", hasNextPage: true, endCursor: "a2" },
      },
    },
  }),
  AdminSecurityInbox: () => ({
    data: {
      notifications: {
        __typename: "NotificationConnection",
        edges: [
          {
            __typename: "NotificationEdge",
            cursor: "n1",
            node: {
              __typename: "Notification",
              id: "f0000000-0000-4000-8000-0000000000n1",
              type: "security.system",
              group: "SECURITY",
              severity: "CRITICAL",
              title: "Certificate pin mismatch on billing-pg",
              body: "The server presented a certificate that does not match the pin.",
              link: "https://bekup.example.com/connections",
              createdAt: new Date().toISOString(),
              readAt: null,
            },
          },
        ],
        pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: "n1" },
      },
    },
  }),
};

const OPERATIONS = {
  ...ADMIN_OPERATIONS,
  ...NOTIFICATIONS_OPERATIONS,
  ...DASHBOARD_OPERATIONS,
  ...JOBS_RUNS_OPERATIONS,
  ...INFRA_OPERATIONS,
  ...RESTORE_CALENDAR_OPERATIONS,
  ServerInfo: () => ({
    data: {
      serverInfo: {
        __typename: "ServerInfo",
        version: "0.0.0",
        insecureTransport: true,
        devMode: true,
      },
    },
  }),
  Me: () =>
    api.signedIn ? { data: { me: ME } } : err("UNAUTHENTICATED", "authentication required"),
  Login: () => ({
    data: {
      login: {
        __typename: "LoginResult",
        secondFactorRequired: true,
        availableFactors: ["TOTP", "RECOVERY_CODE"],
        passwordChangeRequired: false,
        twoFactorEnrollmentRequired: false,
        user: null,
      },
    },
  }),
  CompleteSecondFactor: () => {
    api.signedIn = true;
    return {
      data: {
        completeSecondFactor: {
          __typename: "LoginResult",
          secondFactorRequired: false,
          passwordChangeRequired: false,
          twoFactorEnrollmentRequired: false,
          user: { __typename: "User", id: ME.id },
        },
      },
    };
  },
  Logout: () => {
    api.signedIn = false;
    return { data: { logout: true } };
  },
  ChangePassword: () =>
    api.steppedUp
      ? { data: { changePassword: true } }
      : err("STEP_UP_REQUIRED", "please confirm your identity again"),
  BeginStepUp: () => ({
    data: {
      beginStepUp: {
        __typename: "StepUpChallenge",
        methods: ["TOTP", "RECOVERY_CODE"],
        passkeyOptions: null,
      },
    },
  }),
  CompleteStepUp: () => {
    api.steppedUp = true;
    const now = new Date().toISOString();
    return {
      data: { completeStepUp: { __typename: "StepUpResult", stepUpAt: now, aal2At: now, aal: 2 } },
    };
  },
  TwoFactorOverview: () => ({
    data: {
      myTwoFactorStatus: {
        __typename: "TwoFactorStatus",
        totpEnabled: false,
        totpEnabledAt: null,
        totpEnrollmentPending: false,
        recoveryCodesRemaining: 0,
      },
      webauthnStatus: {
        __typename: "WebAuthnStatus",
        webauthnAvailable: false,
        reason: "server.base_url is plain http on a host other than localhost",
        rpId: null,
      },
      myPasskeys: [],
    },
  }),
  BeginTotpEnrollment: () => ({
    data: {
      beginTotpEnrollment: {
        __typename: "TotpEnrollment",
        qrCodeSvg: qrSvg(),
        manualEntryKey: "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP",
        expiresAt: new Date(Date.now() + 600_000).toISOString(),
      },
    },
  }),
  ConfirmTotpEnrollment: () => ({
    data: {
      confirmTotpEnrollment: {
        __typename: "RecoveryCodes",
        codes: Array.from({ length: 10 }, (_, i) => `k7q${i}-x2mf-9tnc`),
      },
    },
  }),
  MySessions: () => ({
    data: {
      mySessions: [
        {
          __typename: "Session",
          id: "6f1c3e0a-0000-4000-8000-0000000000a1",
          createdAt: new Date(Date.now() - 3_600_000).toISOString(),
          lastSeenAt: new Date().toISOString(),
          idleExpiresAt: new Date(Date.now() + 3_600_000).toISOString(),
          absoluteExpiresAt: new Date(Date.now() + 86_400_000).toISOString(),
          ip: "127.0.0.1",
          userAgent: "Mozilla/5.0 (X11; Linux x86_64) Chrome/130.0 Safari/537.36",
          aal: 2,
          current: true,
        },
      ],
    },
  }),
};

/** The fake JSON GraphQL endpoint (same Origin rule as the stream). */
function serveOperation(req, res, allowedOrigin) {
  if (req.headers.origin !== allowedOrigin) {
    res
      .writeHead(403, { "Content-Type": "application/json" })
      .end('{"errors":[{"message":"origin"}]}');
    return;
  }
  let body = "";
  req.setEncoding("utf8");
  req.on("data", (chunk) => {
    body += chunk;
  });
  req.on("end", () => {
    let name = "";
    try {
      name = JSON.parse(body).operationName ?? "";
    } catch {
      /* answered below */
    }
    const handler = OPERATIONS[name];
    const result = handler ? handler() : err("INTERNAL", `unmocked operation ${name}`);
    res
      .writeHead(200, { "Content-Type": "application/json", "Cache-Control": "no-store" })
      .end(JSON.stringify(result));
  });
}

function serve() {
  const server = createServer((req, res) => {
    const url = new URL(req.url ?? "/", "http://localhost");
    if (url.pathname === "/graphql") {
      const origin = `http://127.0.0.1:${server.address().port}`;
      if (
        req.method === "POST" &&
        !String(req.headers.accept ?? "").includes("text/event-stream")
      ) {
        serveOperation(req, res, origin);
        return;
      }
      serveStream(req, res, origin);
      return;
    }
    let file = normalize(join(outDir, decodeURIComponent(url.pathname)));
    if (!file.startsWith(outDir)) {
      res.writeHead(400).end();
      return;
    }
    if (!existsSync(file) || statSync(file).isDirectory()) file = join(outDir, "index.html");
    res.writeHead(200, {
      "Content-Type": TYPES[extname(file)] ?? "application/octet-stream",
      "Content-Security-Policy": CSP,
      "X-Content-Type-Options": "nosniff",
      "Referrer-Policy": "no-referrer",
    });
    res.end(readFileSync(file));
  });
  return new Promise((ok) => {
    server.listen(0, "127.0.0.1", () => {
      ok(server);
    });
  });
}

async function launch() {
  const executablePath = process.env.CHROME_PATH;
  return chromium.launch(executablePath ? { executablePath } : { channel: "chrome" });
}

async function exercise(page, base, colorScheme) {
  await page.emulateMedia({ colorScheme });
  resetApi();

  // A signed-out visit lands on login with the return path.
  await page.goto(`${base}/profile`);
  await page.getByRole("heading", { name: "Welcome back" }).waitFor();
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByText("Enter your e-mail address.").waitFor();
  await page.getByLabel("E-mail address").fill("ada@example.com");
  await page.getByLabel("Password", { exact: true }).fill("correct horse battery staple");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();

  // Second factor, then back to the requested page.
  await page.getByRole("heading", { name: "Confirm it is you" }).waitFor();
  await page.getByRole("radio", { name: "Recovery code" }).check({ force: true });
  await page.getByRole("radio", { name: "Authenticator app" }).check({ force: true });
  await page.getByLabel("6-digit code").fill("123456");
  await page.getByRole("button", { name: "Verify" }).click();
  await page.getByRole("heading", { name: "Profile and security" }).waitFor();
  await page.getByText(/Two-factor authentication is required for your role/).waitFor();

  // Step-up dialog on a password change, then the retried operation.
  await page.getByLabel("Current password").fill("old-password-1");
  await page.getByLabel("New password", { exact: true }).fill("violet-anchor-meadow-41");
  await page.getByLabel("Repeat the new password").fill("violet-anchor-meadow-41");
  await page.getByRole("button", { name: "Change password" }).click();
  const stepUp = page.getByRole("dialog", { name: "Confirm your identity" });
  await stepUp.waitFor();
  // The static replacement for react-remove-scroll's injected styles works.
  const stepUpLocked = await page.evaluate(
    () =>
      document.body.hasAttribute("data-scroll-locked") &&
      getComputedStyle(document.body).overflow === "hidden",
  );
  if (!stepUpLocked) throw new Error("body scroll lock not applied while a dialog is open");
  await stepUp.getByLabel("6-digit code").fill("654321");
  await stepUp.getByRole("button", { name: "Confirm" }).click();
  await page.getByText("Password changed. Your other sessions were signed out.").first().waitFor();

  // TOTP enrollment: the inline QR code and the one-time recovery codes.
  await page.getByRole("link", { name: "Two-factor" }).click();
  await page.getByRole("button", { name: "Set up an authenticator app" }).click();
  await page.getByRole("img", { name: "Scan this code with your authenticator app" }).waitFor();
  await page.getByLabel("Code from the app").fill("123456");
  await page.getByRole("button", { name: "Confirm and turn on" }).click();
  const codes = page.getByRole("dialog", { name: "Save your recovery codes" });
  await codes.waitFor();
  await codes.getByLabel("I have saved these codes").check();
  await codes.getByRole("button", { name: "Done" }).click();

  await page.getByRole("link", { name: "Sessions" }).click();
  await page.getByText("This session").waitFor();

  await page.getByRole("button", { name: "Account menu" }).click();
  await page.getByRole("menu").waitFor();
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "Notifications" }).hover();
  await page.getByRole("tooltip").waitFor();
  // Dashboard (6.2): every tile, its SVG charts and the hazard hatch.
  await page.goto(`${base}/`);
  await page.getByTestId("tile-system").waitFor();
  await page.getByTestId("security-finding").first().waitFor();
  await page.getByTestId("storage-row").first().waitFor();
  await page.getByTestId("upcoming-run").first().waitFor();
  await page.getByTestId("active-restore").waitFor();
  await page.getByText("Certificate pin mismatch on billing-pg").waitFor();
  // Connections (6.3): list, detail and the certificate pinning dialog;
  // destinations (6.4): the empty list and the form with its switches.
  await page.goto(`${base}/connections`);
  await page.getByRole("link", { name: "orders-db" }).click();
  await page.getByRole("button", { name: "Pin certificate" }).click();
  const pin = page.getByRole("dialog", { name: "Pin the server certificate" });
  await pin.getByRole("button", { name: "Fetch certificate" }).click();
  await pin.getByText("Suggested anchor").waitFor();
  await page.keyboard.press("Escape");
  await page.goto(`${base}/destinations`);
  await page.getByText("No destinations yet").waitFor();
  await page.goto(`${base}/destinations/new`);
  await page.getByRole("switch", { name: "Lock every upload" }).click();
  await page.getByLabel("Retention (days)").waitFor();
  // Restore wizard (6.7): target, identity, the privileged-target preview,
  // the AAL2 step-up and the result; the history; the calendar (6.11).
  await page.goto(`${base}/restore?artifactId=${RC_IDS.artifact}`);
  await page.getByText("Needs the age identity").waitFor();
  await page.getByRole("button", { name: "Continue" }).click();
  await page.getByRole("radio", { name: /pg-main/ }).check();
  await page.getByLabel("New database name").fill("shop_copy");
  await page.getByRole("button", { name: "Continue" }).click();
  await page
    .getByLabel("Age identity")
    .fill("AGE-SECRET-KEY-1QYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQQ");
  await page.getByRole("button", { name: "Continue" }).click();
  await page.getByRole("checkbox", { name: /I trust this backup's source/ }).check();
  await page.getByRole("button", { name: "Continue" }).click();
  await page.getByRole("button", { name: "Verify and start the restore" }).click();
  const restoreStepUp = page.getByRole("dialog", { name: "Confirm your identity" });
  await restoreStepUp.getByLabel("6-digit code").fill("654321");
  await restoreStepUp.getByRole("button", { name: "Confirm" }).click();
  await page.getByText("Restore complete").waitFor();
  await page.getByText("public.esc()").first().waitFor();
  await page.goto(`${base}/restores`);
  await page
    .getByRole("link", { name: /Details/ })
    .first()
    .waitFor();
  await page.goto(`${base}/calendar`);
  await page.getByText("Some events are not shown").waitFor();
  await page.locator('[role="gridcell"][tabindex="0"]').focus();
  await page.keyboard.press("ArrowRight");
  await page.getByRole("button", { name: "Week", exact: true }).click();
  await page.getByRole("button", { name: "Day", exact: true }).click();
  await page.getByText("23:00").waitFor();
  await page.goto(`${base}/jobs`);
  await page.getByRole("heading", { name: "Jobs" }).waitFor();
  // Jobs and runs (tasks 6.5, 6.6): list, toggle dialog, detail with the
  // start-early ledger, the editor's schedule preview, the run page with
  // evidence, log viewer and the download dialog.
  await page.getByText("Unencrypted").first().waitFor();
  await page.getByRole("switch", { name: "Scheduled runs of Billing DB" }).click();
  await page.getByRole("dialog", { name: "Disable Billing DB?" }).waitFor();
  await page.keyboard.press("Escape");
  await page.getByRole("link", { name: "Billing DB" }).first().click();
  await page.getByRole("button", { name: "Start next planned run early" }).click();
  await page.getByTestId("early-following").waitFor();
  await page.keyboard.press("Escape");
  await page.getByText("Latest verdict").waitFor();
  await page.getByRole("list", { name: "Differences" }).waitFor();
  // Test-restore history and detail (6.12 follow-up).
  await page.getByRole("link", { name: "All test restores" }).click();
  await page.getByRole("link", { name: /^Details:/ }).click();
  await page.getByTestId("test-restore-db").first().waitFor();
  await page.goto(`${base}/jobs/${JR_IDS.job}/edit`);
  // The job's schedule builder comes first; the test-restore settings
  // have their own (cron) builder further down.
  await page.getByTestId("schedule-description").first().waitFor();
  await page.getByRole("radio", { name: /Cron/ }).first().check({ force: true });
  await page.getByTestId("target-verdict").waitFor();
  await page.getByRole("list", { name: "Add backup owners" }).getByText("Uma User").waitFor();
  await page.getByTestId("job-shared-channel").first().waitFor();
  await page.getByRole("button", { name: "Set recipient" }).click();
  await page.getByRole("dialog", { name: "Set an age recipient" }).waitFor();
  await page.keyboard.press("Escape");
  await page.goto(`${base}/runs/${JR_IDS.run}`);
  await page.getByRole("region", { name: /^Notices/ }).waitFor();
  await page.getByTestId("item-stats").first().waitFor();
  await page.getByText("run finished").waitFor();
  await page.getByText("public.invoices").waitFor();
  await page.getByRole("button", { name: "Download" }).click();
  await page.getByLabel("age private key").waitFor();
  await page.keyboard.press("Escape");
  await page.goto(`${base}/runs`);
  await page.getByTestId("run-row").waitFor();
  // Notification center and settings (6.10).
  await page.goto(`${base}/notifications`);
  await page.getByTestId("inbox-row").first().waitFor();
  await page.getByRole("button", { name: /^Notifications, 1 unread$/ }).click();
  await page.getByTestId("bell-item").first().waitFor();
  await page.keyboard.press("Escape");
  await page.goto(`${base}/profile/notifications`);
  await page.getByRole("switch", { name: "Backup failed: E-mail", exact: true }).waitFor();
  await page.getByText("Disabled after 5 permanent failures.").waitFor();
  await page.getByRole("button", { name: "Send test" }).click();
  await page.getByTestId("webhook-test-result").waitFor();
  await page.getByRole("button", { name: "Billing DB" }).click();
  await page.getByRole("region", { name: "Overrides for Billing DB" }).waitFor();
  await page.getByRole("button", { name: "Add a webhook" }).click();
  const addHook = page.getByRole("dialog", { name: "Add a webhook" });
  await addHook.getByLabel("Label").fill("Ops hook");
  await addHook.getByLabel("URL").fill("https://user:pw@hooks.example.com/x");
  await addHook.getByRole("button", { name: "Add webhook" }).click();
  await addHook.getByText("Remove the user name and password from the URL.").waitFor();
  await addHook.getByLabel("URL").fill("https://hooks.example.com/x");
  await addHook.getByRole("button", { name: "Add webhook" }).click();
  const keyDialog = page.getByRole("dialog", { name: "Ops hook is ready" });
  await keyDialog.getByTestId("signing-key").waitFor();
  await keyDialog.getByLabel("I have stored the key").check();
  await keyDialog.getByRole("button", { name: "Done" }).click();
  await page.getByRole("button", { name: "Reset to defaults" }).click();
  await page.getByRole("dialog", { name: "Reset all notification preferences?" }).waitFor();
  await page.keyboard.press("Escape");
  await page.goto(`${base}/verify-email#code=Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MGFiY2RlZg`);
  await page.getByRole("heading", { name: "Address verified" }).waitFor();
  if (page.url().includes("#") || page.url().includes("code")) {
    throw new Error("verify-email left the code in the URL");
  }

  // Admin settings (6.9): SMTP with the unencrypted badge and a failed
  // test, alerts, allowlist, the recovery export warning, shared channels
  // with the attach dialog and the audit log drawer.
  await page.goto(`${base}/admin/settings`);
  await page.getByTestId("smtp-unencrypted-badge").first().waitFor();
  await page.getByRole("button", { name: "Send test e-mail" }).click();
  await page.getByTestId("smtp-test-result").waitFor();
  await page.getByRole("link", { name: /Security alerts/ }).click();
  await page.getByTestId("security-alert").waitFor();
  await page.getByRole("link", { name: /Outbound allowlist/ }).click();
  await page.getByTestId("allowlist-config-cidrs").waitFor();
  await page.getByRole("button", { name: "Add CIDR" }).click();
  await page.getByLabel("CIDR 2").fill("10.30.0.0/16");
  await page.getByTestId("allowlist-widening").waitFor();
  await page.getByTestId("personal-widened-badge").waitFor();
  await page
    .getByRole("group", { name: "Uses of CIDR 2" })
    .getByRole("checkbox", { name: /Personal webhooks/ })
    .check();
  await page.getByTestId("allowlist-personal-widening").waitFor();
  // Runtime config reload (6.13): processes, live keys, reload + poll.
  await page.getByRole("link", { name: /Configuration/ }).click();
  await page.getByTestId("config-process").first().waitFor();
  await page.getByText(/Show the 3 live keys/).click();
  await page.getByTestId("config-live-keys").getByText("workers.max_concurrent_runs").waitFor();
  await page.getByRole("button", { name: "Reload configuration" }).click();
  await page
    .getByRole("dialog", { name: "Reload the configuration?" })
    .getByRole("button", { name: "Reload", exact: true })
    .click();
  await page.getByText("2 of 2 processes answered").waitFor();
  await page.getByTestId("config-reload-panel").getByText("server.unknown_key").waitFor();
  await page.getByRole("link", { name: /Recovery/ }).click();
  await page.getByRole("button", { name: "Export the passphrases of Billing DB" }).click();
  await page.getByRole("dialog", { name: /Export the passphrases/ }).waitFor();
  await page.keyboard.press("Escape");
  await page.goto(`${base}/admin/channels`);
  await page.getByTestId("shared-channel").waitFor();
  await page.getByRole("button", { name: "Attach to a job" }).click();
  await page.getByTestId("event-filter").waitFor();
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "New channel" }).click();
  await page.getByRole("dialog", { name: "New shared channel" }).waitFor();
  await page.keyboard.press("Escape");
  await page.goto(`${base}/admin/audit`);
  await page.getByTestId("audit-chain-finding").waitFor();
  await page.getByRole("button", { name: /Details of user\.created/ }).click();
  await page.getByTestId("audit-details").waitFor();
  await page.keyboard.press("Escape");

  await page.goto(`${base}/does-not-exist`);
  await page.getByText("Nothing filed here").waitFor();

  // Sign out ends on the login page.
  await page.getByRole("button", { name: "Account menu" }).click();
  await page.getByRole("menuitem", { name: "Sign out" }).click();
  await page.getByRole("heading", { name: "Welcome back" }).waitFor();

  await page.goto(`${base}/design`);
  await page.getByRole("radio", { name: "Dark" }).first().check({ force: true });
  await page.getByTestId("ds-dialog").click();
  await page.getByRole("dialog").waitFor();
  // The static replacement for react-remove-scroll's injected styles works.
  const locked = await page.evaluate(
    () =>
      document.body.hasAttribute("data-scroll-locked") &&
      getComputedStyle(document.body).overflow === "hidden",
  );
  if (!locked) throw new Error("body scroll lock not applied while a dialog is open");
  await page.keyboard.press("Escape");
  await page.getByTestId("ds-menu").click();
  await page.getByRole("menu").waitFor();
  await page.keyboard.press("Escape");
  await page.getByTestId("ds-select").click();
  await page.getByRole("listbox").waitFor();
  await page.keyboard.press("Escape");
  await page.getByTestId("ds-tooltip").hover();
  await page.getByRole("tooltip").waitFor();
  await page.getByTestId("ds-toast").click();
  await page.locator("[data-sonner-toast]").first().waitFor();
  await page.getByRole("tab", { name: "Cards" }).click();
  await page.getByRole("radio", { name: "Light" }).first().check({ force: true });
  await page.waitForTimeout(300);
}

/**
 * Opens an event stream from the page like graphql-sse (fetch, POST, JSON
 * body, Accept: text/event-stream, credentials same-origin) and reads it
 * to the end; reports whether a next event arrived and whether a
 * connect-src violation fired.
 */
function openEventStream(page, url) {
  return page.evaluate(
    (target) =>
      new Promise((ok) => {
        let violated = false;
        document.addEventListener("securitypolicyviolation", (e) => {
          if (e.effectiveDirective === "connect-src") violated = true;
        });
        const done = (open, status) => setTimeout(() => ok({ open, status, violated }), 200);
        setTimeout(() => ok({ open: false, status: 0, violated }), 3000);
        fetch(target, {
          method: "POST",
          credentials: "same-origin",
          headers: {
            "content-type": "application/json; charset=utf-8",
            accept: "text/event-stream",
          },
          body: JSON.stringify({ query: "subscription { sessionEvents { kind } }" }),
        })
          .then(async (res) => {
            if (!res.ok || !res.body) return done(false, res.status);
            const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
            let text = "";
            for (;;) {
              const { value, done: end } = await reader.read();
              if (end) break;
              text += value;
            }
            done(text.includes("event: next") && text.includes("event: complete"), res.status);
          })
          .catch(() => done(false, 0));
      }),
    url,
  );
}

async function main() {
  build();
  const problems = assertNoInlineCode();
  const server = await serve();
  const { port } = server.address();
  const base = `http://127.0.0.1:${port}`;
  const browser = await launch();
  const violations = [];
  try {
    for (const scheme of ["light", "dark"]) {
      const page = await browser.newPage();
      page.setDefaultTimeout(STEP_TIMEOUT_MS);
      await page.addInitScript(() => {
        window.__csp = [];
        document.addEventListener("securitypolicyviolation", (e) => {
          window.__csp.push(
            `${e.effectiveDirective} blocked ${e.blockedURI || "inline"} at ${e.sourceFile}:${e.lineNumber} ${e.sample}`,
          );
        });
      });
      let recording = true;
      page.on("console", (msg) => {
        if (recording && /Content.Security.Policy|Trusted.Type/i.test(msg.text())) {
          violations.push(`[${scheme}] console: ${msg.text()}`);
        }
      });
      page.on("pageerror", (err) => violations.push(`[${scheme}] page error: ${err.message}`));

      try {
        await exercise(page, base, scheme);
      } catch (err) {
        problems.push(
          `[${scheme}] UI walk-through failed at ${page.url()}: ${err.message.split("\n")[0]}`,
        );
      }
      // Same-origin event stream (subscriptions): must open without a
      // violation, carrying the page's Origin.
      streamOrigins.length = 0;
      const sameOrigin = await openEventStream(page, `${base}/graphql`);
      if (!sameOrigin.open || sameOrigin.violated)
        problems.push(
          `[${scheme}] same-origin event stream: open=${sameOrigin.open} status=${sameOrigin.status} violation=${sameOrigin.violated}`,
        );
      if (!streamOrigins.includes(base))
        problems.push(
          `[${scheme}] event stream request without the page Origin: ${streamOrigins.join(", ")}`,
        );
      const seen = await page.evaluate(() => window.__csp);
      violations.push(...seen.map((v) => `[${scheme}] ${v}`));

      // Positive control: the harness must notice a violation when one happens.
      recording = false;
      const control = await page.evaluate(
        () =>
          new Promise((ok) => {
            document.addEventListener("securitypolicyviolation", () => ok(true), { once: true });
            const s = document.createElement("style");
            s.append("body{}");
            document.head.append(s);
            setTimeout(() => ok(false), 2000);
          }),
      );
      if (!control)
        problems.push(`[${scheme}] positive control: injected <style> was NOT reported`);
      // Positive control: a cross-origin event stream (other host name) is
      // blocked by connect-src before it reaches any server.
      streamOrigins.length = 0;
      const crossOrigin = await openEventStream(page, `http://localhost:${port}/graphql`);
      if (crossOrigin.open || !crossOrigin.violated || streamOrigins.length > 0)
        problems.push(
          `[${scheme}] cross-origin event stream was NOT blocked by connect-src (open=${crossOrigin.open}, reached server=${streamOrigins.length > 0})`,
        );
      await page.close();
    }
    // The stub refuses a stream with a foreign Origin, like serve's Origin
    // check (a non-browser client is not stopped by the CSP).
    const foreign = await fetch(`${base}/graphql`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        accept: "text/event-stream",
        origin: "https://evil.example",
      },
      body: "{}",
    });
    if (foreign.status !== 403)
      problems.push(`cross-origin stream POST was not refused (status ${foreign.status})`);
  } finally {
    await browser.close();
    server.close();
  }

  const unique = [...new Set(violations)];
  console.log(`\nCSP: ${CSP}\n`);
  if (problems.length || unique.length) {
    for (const p of [...problems, ...unique]) console.error(`  ✗ ${p}`);
    console.error(`\ncsp-check FAILED (${problems.length + unique.length} problem(s)).`);
    process.exit(1);
  }
  console.log(
    "csp-check passed: no CSP or Trusted Types violations in light and dark mode; same-origin event stream allowed, cross-origin blocked.",
  );
}

// Importable (e.g. for local screenshot scripts against the fake API).
export { api, resetApi, serve };

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((err) => {
    console.error(err);
    process.exit(1);
  });
}
