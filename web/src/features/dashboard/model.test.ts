import { describe, expect, it } from "vitest";

import {
  applyRunEvent,
  attentionItems,
  DAY_MS,
  expiryLevel,
  HOUR_MS,
  internalLink,
  jobHealth,
  mergeRestores,
  queueSummary,
  securityFindings,
  sortHealth,
  storageByDestination,
  tickState,
  upcomingRuns,
  type CalendarEventInput,
  type ConnectionInput,
  type JobInput,
  type LiveRun,
} from "./model";

const NOW = Date.parse("2026-10-28T12:00:00Z");
const iso = (offsetMs: number) => new Date(NOW + offsetMs).toISOString();

function job(overrides: Partial<JobInput> = {}): JobInput {
  return {
    id: "j1",
    name: "Billing",
    enabled: true,
    nextRunAt: iso(HOUR_MS),
    scheduleDescription: "Every hour",
    encryption: { configured: true, mode: "PASSPHRASE", optedOut: false },
    lastRun: { id: "r1", status: "SUCCESS", queuedAt: iso(-HOUR_MS), warnings: [] },
    missed: false,
    missedSince: null,
    ...overrides,
  };
}

function conn(overrides: Partial<ConnectionInput> = {}): ConnectionInput {
  return {
    id: "c1",
    name: "pg-main",
    tlsMode: "PINNED",
    insecureTransport: false,
    pinStatus: "VALID",
    lastTest: null,
    ...overrides,
  };
}

function event(overrides: Partial<CalendarEventInput> = {}): CalendarEventInput {
  return {
    jobId: "j1",
    jobName: "Billing",
    kind: "PLANNED",
    start: iso(HOUR_MS),
    paused: false,
    startedEarly: false,
    overdue: false,
    ...overrides,
  };
}

describe("jobHealth", () => {
  it.each([
    ["healthy", job(), "healthy"],
    ["paused", job({ enabled: false }), "paused"],
    ["never run", job({ lastRun: null }), "never"],
    [
      "failing",
      job({ lastRun: { id: "r1", status: "FAILED", queuedAt: iso(-HOUR_MS) } }),
      "failing",
    ],
    [
      "partial",
      job({ lastRun: { id: "r1", status: "PARTIAL", queuedAt: iso(-HOUR_MS) } }),
      "warning",
    ],
    [
      "warnings",
      job({
        lastRun: {
          id: "r1",
          status: "SUCCESS",
          queuedAt: iso(-HOUR_MS),
          warnings: [{ code: "x" }],
        },
      }),
      "warning",
    ],
    ["overdue", job({ nextRunAt: iso(-HOUR_MS) }), "overdue"],
  ] as const)("%s", (_, input, state) => {
    expect(jobHealth(input, { now: NOW }).state).toBe(state);
  });

  it("is not overdue within the grace period", () => {
    expect(jobHealth(job({ nextRunAt: iso(-60_000) }), { now: NOW }).overdue).toBe(false);
  });

  it("uses the calendar's overdue flag", () => {
    const h = jobHealth(job(), { now: NOW, overdueJobs: new Set(["j1"]) });
    expect(h.state).toBe("overdue");
  });

  it("takes missed from the server's missed-backup check", () => {
    const failedRun = {
      id: "r1",
      status: "SKIPPED" as const,
      queuedAt: iso(-3 * HOUR_MS),
      finishedAt: iso(-3 * HOUR_MS),
    };
    const h = jobHealth(job({ missed: true, missedSince: iso(-2 * HOUR_MS), lastRun: failedRun }), {
      now: NOW,
    });
    expect(h.missed).toBe(true);
    expect(h.missedSince).toBe(iso(-2 * HOUR_MS));
    expect(h.state).toBe("missed");
    expect(jobHealth(job({ missed: false, lastRun: failedRun }), { now: NOW }).missed).toBe(false);
    // Paused jobs are never shown as missed.
    expect(
      jobHealth(job({ enabled: false, missed: true, missedSince: iso(-HOUR_MS) }), { now: NOW })
        .missed,
    ).toBe(false);
  });

  it("clears missed once a successful run is seen after the outage began", () => {
    const live = new Map([
      [
        "j1",
        {
          id: "r9",
          status: "SUCCESS" as const,
          queuedAt: iso(-10 * 60_000),
          finishedAt: iso(-5 * 60_000),
        },
      ],
    ]);
    const before = job({
      missed: true,
      missedSince: iso(-2 * HOUR_MS),
      lastRun: { id: "r1", status: "FAILED", queuedAt: iso(-3 * HOUR_MS) },
    });
    expect(jobHealth(before, { now: NOW }).state).toBe("failing");
    const h = jobHealth(before, { now: NOW, live });
    expect(h.missed).toBe(false);
    expect(h.state).toBe("healthy");
  });

  it("prefers a newer live run over the loaded lastRun", () => {
    const live = new Map([
      ["j1", { id: "r2", status: "RUNNING" as const, queuedAt: iso(-60_000) }],
    ]);
    const h = jobHealth(job(), { now: NOW, live });
    expect(h.state).toBe("running");
    expect(h.last?.id).toBe("r2");
  });

  it("sorts the most urgent first", () => {
    const rows = sortHealth([
      jobHealth(job({ id: "a", name: "A" }), { now: NOW }),
      jobHealth(
        job({ id: "b", name: "B", lastRun: { id: "x", status: "FAILED", queuedAt: iso(0) } }),
        { now: NOW },
      ),
    ]);
    expect(rows.map((r) => r.id)).toEqual(["b", "a"]);
  });
});

describe("live runs", () => {
  const run = (id: string, status: LiveRun["status"]): LiveRun => ({
    id,
    status,
    queuedAt: iso(0),
    job: { id: "j1", name: "Billing" },
  });

  it("adds active runs and drops finished ones", () => {
    let m = applyRunEvent(new Map<string, LiveRun>(), run("r1", "QUEUED"));
    m = applyRunEvent(m, run("r1", "RUNNING"));
    expect(m.get("r1")?.status).toBe("RUNNING");
    m = applyRunEvent(m, run("r1", "SUCCESS"));
    expect(m.size).toBe(0);
  });
});

describe("attentionItems", () => {
  it("merges failures of the last 7 days with ongoing missed jobs, newest first", () => {
    const failure: LiveRun = {
      id: "r1",
      status: "FAILED",
      queuedAt: iso(-2 * HOUR_MS),
      finishedAt: iso(-HOUR_MS),
      job: { id: "j1", name: "Billing" },
    };
    const old: LiveRun = { ...failure, id: "r0", finishedAt: iso(-8 * DAY_MS) };
    const health = [
      jobHealth(job({ id: "j2", name: "Recent", missed: true, missedSince: iso(-30 * 60_000) }), {
        now: NOW,
      }),
      jobHealth(
        job({
          id: "j3",
          name: "Long",
          missed: true,
          missedSince: iso(-20 * DAY_MS),
          lastRun: { id: "r3", status: "FAILED", queuedAt: iso(-21 * DAY_MS) },
        }),
        { now: NOW },
      ),
      jobHealth(job({ id: "j4", name: "Fine" }), { now: NOW }),
    ];
    const items = attentionItems([failure, failure, old], health, NOW);
    expect(items.map((i) => i.kind)).toEqual(["missed", "failed", "missed"]);
    const last = items[2];
    expect(last?.kind === "missed" ? last.job.name : null).toBe("Long");
  });

  it("adds one retention item per job and destination, ordered by the oldest held backup", () => {
    const hold = (destinationId: string, since: string) => ({
      destinationId,
      destinationName: destinationId,
      destinationDeleted: false,
      reason: "NO_COMPLETE_RUN" as const,
      heldRuns: 2,
      since,
    });
    const health = [
      jobHealth(
        job({
          id: "j5",
          name: "Held",
          enabled: false,
          retentionHolds: [hold("d1", iso(-3 * DAY_MS)), hold("d2", iso(-HOUR_MS))],
        }),
        { now: NOW },
      ),
      jobHealth(job({ id: "j6", name: "Fine" }), { now: NOW }),
    ];
    const failure: LiveRun = {
      id: "r1",
      status: "FAILED",
      queuedAt: iso(-3 * HOUR_MS),
      finishedAt: iso(-2 * HOUR_MS),
      job: { id: "j1", name: "Billing" },
    };
    const items = attentionItems([failure], health, NOW);
    expect(items.map((i) => (i.kind === "retention" ? i.hold.destinationId : i.kind))).toEqual([
      "d2",
      "failed",
      "d1",
    ]);
    // A paused job still shows its holds; health has no state for them.
    expect(health[0]?.state).toBe("paused");
    expect(health[0]?.holds).toHaveLength(2);
  });
});

describe("mergeRestores", () => {
  it("lets stream events win over the loaded snapshot and adds new restores", () => {
    const loaded = [
      { id: "a", status: "STAGING" },
      { id: "b", status: "QUEUED" },
    ];
    const live = new Map([
      ["a", { id: "a", status: "SUCCEEDED" }],
      ["c", { id: "c", status: "QUEUED" }],
    ]);
    expect(mergeRestores(loaded, live)).toEqual([
      { id: "a", status: "SUCCEEDED" },
      { id: "b", status: "QUEUED" },
      { id: "c", status: "QUEUED" },
    ]);
  });
});

describe("upcomingRuns", () => {
  it("keeps unpaused planned runs of the next 24 hours", () => {
    const runs = upcomingRuns(
      [
        event({ start: iso(2 * HOUR_MS) }),
        event({ start: iso(HOUR_MS), jobId: "j2" }),
        event({ start: iso(3 * HOUR_MS), paused: true }),
        event({ start: iso(30 * HOUR_MS) }),
        event({ kind: "RUN", start: iso(HOUR_MS) }),
      ],
      NOW,
    );
    expect(runs.map((r) => r.jobId)).toEqual(["j2", "j1"]);
    expect(runs[0]?.offset).toBeCloseTo(1 / 24);
  });
});

describe("securityFindings", () => {
  it("is empty for a secure setup", () => {
    expect(securityFindings([job()], [conn()])).toEqual([]);
  });

  it("flags every weakened setting, critical first", () => {
    const findings = securityFindings(
      [
        job({ id: "j1", encryption: { configured: true, mode: "NONE", optedOut: true } }),
        job({ id: "j2", name: "Other" }),
      ],
      [
        conn({ id: "c1", name: "plain", tlsMode: "DISABLED", insecureTransport: true }),
        conn({
          id: "c2",
          name: "root",
          lastTest: {
            at: iso(0),
            ok: true,
            stale: false,
            privileges: { status: "PRIVILEGED" },
          },
        }),
        conn({
          id: "c3",
          name: "unknown",
          lastTest: { at: iso(0), ok: true, stale: true, privileges: { status: "UNKNOWN" } },
        }),
        conn({ id: "c4", name: "soon", pinStatus: "EXPIRES_WITHIN_7_DAYS" }),
        conn({ id: "c5", name: "late", pinStatus: "EXPIRED" }),
        conn({
          id: "c6",
          name: "mitm",
          lastTest: { at: iso(0), ok: false, stale: false, code: "TLS_PIN_MISMATCH" },
        }),
        conn({
          id: "c7",
          name: "old-mismatch",
          lastTest: { at: iso(0), ok: false, stale: true, code: "SSH_HOST_KEY_MISMATCH" },
        }),
      ],
    );
    expect(findings.map((f) => f.kind)).toEqual([
      "identity-mismatch",
      "pin-expired",
      "unencrypted",
      "insecure-transport",
      "privileged-target",
      "pin-expiring",
    ]);
    const byKind = Object.fromEntries(findings.map((f) => [f.kind, f]));
    expect(byKind["identity-mismatch"]?.items.map((i) => i.name)).toEqual(["mitm"]);
    expect(byKind["unencrypted"]?.items).toEqual([
      { id: "j1", name: "Billing", to: "/jobs/j1", detail: "Encryption opted out" },
    ]);
    expect(byKind["insecure-transport"]?.items[0]?.detail).toBe("TLS disabled");
    expect(byKind["privileged-target"]?.items.map((i) => i.name)).toEqual(["root", "unknown"]);
    expect(byKind["privileged-target"]?.items[1]?.detail).toMatch(/stale/);
    expect(byKind["pin-expired"]?.severity).toBe("critical");
  });
});

describe("storageByDestination", () => {
  it("maps the server aggregate and scales bars to the largest", () => {
    const rows = storageByDestination([
      { destinationId: "d1", name: "disk", type: "DISK", storedBytes: 100, artifactCount: 1 },
      { destinationId: "d2", name: "s3", type: "S3", storedBytes: 400, artifactCount: 2 },
      { destinationId: "d3", name: "empty", type: "SFTP", storedBytes: 0, artifactCount: 0 },
    ]);
    expect(rows.map((r) => [r.id, r.bytes, r.objects, r.share])).toEqual([
      ["d2", 400, 2, 1],
      ["d1", 100, 1, 0.25],
      ["d3", 0, 0, 0],
    ]);
  });
});

describe("system health helpers", () => {
  it("grades expiries", () => {
    expect(expiryLevel(iso(3 * DAY_MS), NOW)).toBe("critical");
    expect(expiryLevel(iso(20 * DAY_MS), NOW)).toBe("warning");
    expect(expiryLevel(iso(90 * DAY_MS), NOW)).toBe("ok");
  });

  it("detects a stale scheduler tick", () => {
    expect(tickState(false, null, NOW)).toBe("off");
    expect(tickState(true, null, NOW)).toBe("none");
    expect(tickState(true, iso(-60_000), NOW)).toBe("ok");
    expect(tickState(true, iso(-10 * 60_000), NOW)).toBe("stale");
  });

  it("summarizes queue depth and ignores finished states", () => {
    expect(
      queueSummary([
        { queue: "notify", state: "available", jobs: 2 },
        { queue: "backup", state: "running", jobs: 1 },
        { queue: "backup", state: "retryable", jobs: 3 },
        { queue: "backup", state: "completed", jobs: 99 },
      ]),
    ).toEqual([
      { queue: "backup", waiting: 0, running: 1, retrying: 3 },
      { queue: "notify", waiting: 2, running: 0, retrying: 0 },
    ]);
  });
});

describe("internalLink", () => {
  it.each([
    ["/runs/1", "/runs/1"],
    ["https://evil.example", null],
    ["//evil.example", null],
    ["/\\evil.example", null],
    ["javascript:alert(1)", null],
    ["/a b", null],
    [null, null],
  ])("%s", (input, expected) => {
    expect(internalLink(input)).toBe(expected);
  });
});
