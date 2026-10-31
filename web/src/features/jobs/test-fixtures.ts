/** Fixtures for the job and run page tests (shapes of operations.ts). */

export const JOB_ID = "0b6c3f7e-0000-4000-8000-0000000000j1";
export const RUN_ID = "0b6c3f7e-0000-4000-8000-0000000000r1";
export const CONN_ID = "0b6c3f7e-0000-4000-8000-0000000000c1";
export const DEST_ID = "0b6c3f7e-0000-4000-8000-0000000000d1";
export const DEST2_ID = "0b6c3f7e-0000-4000-8000-0000000000d2";
export const ART_ID = "0b6c3f7e-0000-4000-8000-0000000000a1";

const schedule = {
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

export function jobListNode(overrides: Record<string, unknown> = {}) {
  return {
    __typename: "Job",
    id: JOB_ID,
    name: "Billing DB",
    slug: "billing-db",
    enabled: true,
    nextRunAt: "2026-10-29T03:00:00Z",
    scheduleDescription: "Every day at 03:00 (UTC)",
    connection: {
      __typename: "JobConnectionRef",
      id: CONN_ID,
      name: "billing-pg",
      engine: "POSTGRES",
      deleted: false,
    },
    encryption: {
      __typename: "JobEncryption",
      configured: true,
      mode: "PASSPHRASE",
      optedOut: false,
    },
    owners: [
      {
        __typename: "JobOwner",
        userId: "u-1",
        displayName: "Olive Owner",
        email: "olive@example.com",
        role: "USER",
        disabled: false,
      },
    ],
    lastRun: {
      __typename: "Run",
      id: RUN_ID,
      status: "SUCCESS",
      finishedAt: "2026-10-28T03:04:00Z",
      queuedAt: "2026-10-28T03:00:00Z",
      warnings: [],
    },
    ...overrides,
  };
}

export function jobsList(nodes: ReturnType<typeof jobListNode>[]) {
  return {
    jobs: {
      __typename: "JobConnection",
      edges: nodes.map((node) => ({ __typename: "JobEdge", cursor: node.id, node })),
      pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: null },
    },
  };
}

export function jobDetail(overrides: Record<string, unknown> = {}) {
  return {
    __typename: "Job",
    id: JOB_ID,
    name: "Billing DB",
    slug: "billing-db",
    description: "",
    enabled: true,
    nextRunAt: "2026-10-29T03:00:00Z",
    lastScheduledAt: "2026-10-28T03:00:00Z",
    scheduleDescription: "Every day at 03:00 (UTC)",
    schedule,
    connection: {
      __typename: "JobConnectionRef",
      id: CONN_ID,
      name: "billing-pg",
      engine: "POSTGRES",
      versionKey: "17",
      deleted: false,
    },
    dbMode: "ALL",
    databases: [],
    exclude: ["scratch"],
    includeGlobals: true,
    retention: { __typename: "JobRetention", keepLast: 7, keepDays: null },
    timeoutSeconds: null,
    verification: "STANDARD",
    destinations: [
      {
        __typename: "JobDestinationRef",
        id: DEST_ID,
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
      passphrase: {
        __typename: "JobPassphraseInfo",
        version: 1,
        createdAt: "2026-10-01T00:00:00Z",
      },
      recipient: null,
      updatedAt: "2026-10-01T00:00:00Z",
    },
    owners: [
      {
        __typename: "JobOwner",
        userId: "u-1",
        email: "olive@example.com",
        displayName: "Olive Owner",
        role: "USER",
        disabled: false,
        addedAt: "2026-10-02T00:00:00Z",
      },
    ],
    lastRun: {
      __typename: "Run",
      id: RUN_ID,
      status: "SUCCESS",
      queuedAt: "2026-10-28T03:00:00Z",
      finishedAt: "2026-10-28T03:04:00Z",
      items: [],
    },
    schemaVersion: {
      __typename: "JobSchemaVersion",
      mode: "NONE",
      preset: null,
      presetLabel: null,
      schema: "public",
      customSql: null,
    },
    retentionHolds: [],
    testRestore: {
      __typename: "JobTestRestore",
      enabled: false,
      scheduleDescription: "Manual",
      schedule: { ...schedule, kind: "MANUAL", every: null, unit: null, anchor: null },
      nextAt: null,
      target: null,
      targetPrivileges: null,
      privilegedTargetAcknowledgedAt: null,
    },
    createdAt: "2026-10-01T00:00:00Z",
    updatedAt: "2026-10-01T00:00:00Z",
    ...overrides,
  };
}

/** A Job.retentionHolds entry (D-209). */
export function retentionHold(overrides: Record<string, unknown> = {}) {
  return {
    __typename: "RetentionHold",
    destinationId: DEST_ID,
    destinationName: "vault-s3",
    destinationType: "S3",
    destinationDeleted: false,
    reason: "NO_COMPLETE_RUN",
    heldRuns: 3,
    since: "2026-10-20T03:00:00Z",
    newestCompleteAt: null,
    checkedAt: "2026-10-28T03:05:00Z",
    ...overrides,
  };
}

export const emptyRuns = {
  runs: {
    __typename: "RunConnection",
    edges: [],
    pageInfo: { __typename: "PageInfo", hasNextPage: false, endCursor: null },
  },
};

export const TR_ID = "0b6c3f7e-0000-4000-8000-0000000000t1";

/** A test restore (list row); `detail` adds the databases with findings. */
export function testRestoreNode(overrides: Record<string, unknown> = {}) {
  return {
    __typename: "TestRestore",
    id: TR_ID,
    runId: RUN_ID,
    status: "FAILED",
    trigger: "SCHEDULE",
    requestedBy: null,
    target: {
      __typename: "RestoreTargetRef",
      id: CONN_ID,
      name: "scratch-pg",
      engine: "POSTGRES",
      temporary: false,
      deleted: false,
    },
    scheduledFor: "2026-10-27T04:00:00Z",
    createdAt: "2026-10-27T04:00:00Z",
    startedAt: "2026-10-27T04:00:05Z",
    finishedAt: "2026-10-27T04:03:00Z",
    durationSeconds: 175,
    errorCode: null,
    errorMessage: null,
    databaseCount: 2,
    passed: 1,
    warned: 0,
    failed: 1,
    ...overrides,
  };
}

export function testRestoreDetail(overrides: Record<string, unknown> = {}) {
  const finding = (
    severity: string,
    code: string,
    object: string | null,
    source: number | null,
    restored: number | null,
  ) => ({
    __typename: "TestRestoreFinding",
    severity,
    code,
    object,
    source,
    restored,
    delta: source != null && restored != null ? restored - source : null,
  });
  const db = (id: string, database: string, status: string, findings: unknown[]) => ({
    __typename: "TestRestoreDatabase",
    id,
    runItemId: `item-${id}`,
    database,
    restoreId: `r-${id}`,
    status,
    errorCode: null,
    errorMessage: null,
    scratchDropped: true,
    findingsTotal: findings.length,
    tablesCompared: 12,
    exactTables: 12,
    restoreStatus: null,
    restoreErrorCode: null,
    startedAt: "2026-10-27T04:00:05Z",
    finishedAt: "2026-10-27T04:03:00Z",
    findings,
  });
  return {
    ...testRestoreNode(),
    job: {
      __typename: "RunJobRef",
      id: JOB_ID,
      name: "Billing DB",
      slug: "billing-db",
      deleted: false,
    },
    databases: [
      db("d1", "billing", "FAILED", [
        finding("FAIL", "row_count_mismatch", "public.invoices", 1200, 1100),
        finding("FAIL", "table_missing", "public.ledger", null, null),
        finding("WARN", "row_count_delta", "public.events", 50, 52),
      ]),
      db("d2", "crm", "PASSED", []),
    ],
    ...overrides,
  };
}

export const noTestRestores = { testRestores: { __typename: "TestRestoreConnection", edges: [] } };

function warning(code: string, message: string, database: string | null = null) {
  return { __typename: "RunWarning", code, message, database, destinationId: null };
}

export function runDetail(overrides: Record<string, unknown> = {}) {
  return {
    __typename: "Run",
    id: RUN_ID,
    status: "SUCCESS",
    trigger: "SCHEDULE",
    triggeredBy: null,
    scheduledFor: "2026-10-28T03:00:00Z",
    attempt: 1,
    queuedAt: "2026-10-28T03:00:00Z",
    startedAt: "2026-10-28T03:00:02Z",
    finishedAt: "2026-10-28T03:04:00Z",
    durationSeconds: 238,
    storageTimestamp: "2026-10-28T03:00:02Z",
    errorCode: null,
    errorMessage: null,
    warnings: [warning("insufficient_privileges", "2 tables could not be read", "billing")],
    notices: [warning("databases_excluded", "1 database excluded by the job")],
    bytesRaw: 1048576,
    bytesStored: 262144,
    cancelRequested: false,
    cancelRequestedAt: null,
    logLines: 2,
    logLinesDropped: 0,
    updatedAt: "2026-10-28T03:04:00Z",
    job: {
      __typename: "RunJobRef",
      id: JOB_ID,
      name: "Billing DB",
      slug: "billing-db",
      deleted: false,
    },
    items: [
      {
        __typename: "RunItem",
        id: "item-1",
        kind: "DATABASE",
        database: "billing",
        position: 0,
        status: "SUCCESS",
        schemaVersion: "20241001120000",
        schemaVersionStatus: "OK",
        rawSize: 1048576,
        storedSize: 262144,
        sha256: "ab".repeat(32),
        verified: true,
        startedAt: "2026-10-28T03:00:02Z",
        finishedAt: "2026-10-28T03:04:00Z",
        durationSeconds: 238,
        errorCode: null,
        errorMessage: null,
        warnings: [warning("insufficient_privileges", "2 tables could not be read", "billing")],
        artifacts: [
          {
            __typename: "Artifact",
            id: ART_ID,
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
              mode: "PASSPHRASE",
              passphraseVersion: 1,
              recipientVersion: null,
            },
            destination: {
              __typename: "ArtifactDestinationRef",
              id: DEST_ID,
              name: "vault-s3",
              type: "S3",
              deleted: false,
            },
          },
        ],
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
    ...overrides,
  };
}

/** RunItem.stats (D-187) of the fixture run: one database, a few tables. */
export function runItemStats(statsOverrides: Record<string, unknown> | null = {}) {
  const finding = (code: string, count: number, objects: string[], truncated = false) => ({
    __typename: "StatsFinding",
    code,
    count,
    objects,
    truncated,
  });
  const table = (name: string, rows: number | null, count: string, approximate = false) => ({
    __typename: "TableStats",
    name,
    rows,
    count,
    exact: count === "EXACT",
    estimate: count === "ESTIMATE",
    unknown: count === "UNKNOWN",
    approximate,
  });
  return {
    run: {
      __typename: "Run",
      id: RUN_ID,
      items: [
        {
          __typename: "RunItem",
          id: "item-1",
          kind: "DATABASE",
          database: "billing",
          stats:
            statsOverrides === null
              ? null
              : {
                  __typename: "RunItemStats",
                  level: "DEEP",
                  consistency: "SNAPSHOT",
                  objects: [
                    { __typename: "StatsObjectCount", kind: "tables", count: 3 },
                    { __typename: "StatsObjectCount", kind: "materialized_views", count: 1 },
                  ],
                  tables: [
                    table("public.invoices", 12500, "EXACT"),
                    table("public.events", 98000, "ESTIMATE"),
                    table("audit.log", null, "UNKNOWN"),
                  ],
                  tablesTotal: 5,
                  tablesTruncated: true,
                  warnings: [finding("insufficient_privileges", 2, ["private.t1", "hidden.t2"])],
                  notices: [finding("databases_excluded", 1, ["scratch"], true)],
                  recordedAt: "2026-10-28T03:00:03Z",
                  ...statsOverrides,
                },
        },
      ],
    },
  };
}

export const runJobContext = {
  job: {
    __typename: "Job",
    id: JOB_ID,
    verification: "STANDARD",
    connection: { __typename: "JobConnectionRef", id: CONN_ID, engine: "POSTGRES" },
  },
};

export const emptyLogs = {
  runLogs: {
    __typename: "RunLogPage",
    runId: RUN_ID,
    endId: null,
    hasMore: false,
    dropped: 0,
    lines: [],
  },
};

export const stepUpHandlers = (onComplete: () => void) => ({
  BeginStepUp: () => ({
    data: {
      beginStepUp: { __typename: "StepUpChallenge", methods: ["TOTP"], passkeyOptions: null },
    },
  }),
  CompleteStepUp: () => {
    onComplete();
    return {
      data: {
        completeStepUp: {
          __typename: "StepUpResult",
          stepUpAt: "2026-10-28T10:00:00Z",
          aal2At: "2026-10-28T10:00:00Z",
          aal: 2,
        },
      },
    };
  },
});
