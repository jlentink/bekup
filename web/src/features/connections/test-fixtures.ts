import { ok, type Handler } from "@/test/mock-api";

/** Fixtures for the connection and destination page tests (never shipped). */

export const CONN_ID = "6f1c3e0a-0000-4000-8000-0000000000c1";
export const DEST_ID = "6f1c3e0a-0000-4000-8000-0000000000d1";

export function connectionFixture(overrides: Record<string, unknown> = {}, admin = true) {
  return {
    __typename: "Connection",
    id: CONN_ID,
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
    lastTest: null,
    settings: admin
      ? {
          __typename: "ConnectionSettings",
          imageKey: "17",
          username: "backup",
          hasPassword: true,
          clientCertificatePem: null,
          tlsPin: null,
          backupPin: null,
          ssh: null,
        }
      : null,
    ...overrides,
  };
}

/** A stored last test (D-187) of a connection. */
export function connectionLastTest(overrides: Record<string, unknown> = {}) {
  return {
    __typename: "ConnectionLastTest",
    at: "2026-10-27T09:00:00Z",
    ok: true,
    code: null,
    stale: false,
    privileges: {
      __typename: "TargetPrivileges",
      status: "NOT_PRIVILEGED",
      reasons: [],
      acknowledgementRequired: false,
    },
    ...overrides,
  };
}

export function sshSettings(overrides: Record<string, unknown> = {}) {
  return {
    __typename: "ConnectionSshSettings",
    host: "bastion.example",
    port: 22,
    user: "tunnel",
    authMethod: "PASSWORD",
    hasPassword: true,
    hasPrivateKey: false,
    hasPassphrase: false,
    publicKey: null,
    hostKey: null,
    ...overrides,
  };
}

export const engineVersionsHandler: Handler = () =>
  ok({
    engineVersions: [
      {
        __typename: "EngineVersion",
        engine: "POSTGRES",
        key: "16",
        clientVersion: "16.6",
        legacy: false,
      },
      {
        __typename: "EngineVersion",
        engine: "POSTGRES",
        key: "17",
        clientVersion: "17.2",
        legacy: false,
      },
      {
        __typename: "EngineVersion",
        engine: "POSTGRES",
        key: "13",
        clientVersion: "13.18",
        legacy: true,
      },
    ],
  });

export function chainFixture() {
  const base = {
    __typename: "ChainCertificate",
    pem: "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----",
    notBefore: "2026-01-01T00:00:00Z",
    notAfter: "2030-01-01T00:00:00Z",
    ipAddresses: [],
  };
  return {
    __typename: "CertificateChain",
    suggestedAnchorIndex: 1,
    leafSpkiSha256: "q0Lf1kPq3m4T2aH7yX9vZ1bC8dE5fG6hJ7kL8mN9oP0=",
    certificates: [
      {
        ...base,
        subject: "CN=db.internal.example",
        issuer: "CN=Acme Private Root",
        dnsNames: ["db.internal.example"],
        isCa: false,
        selfSigned: false,
        spkiSha256: "q0Lf1kPq3m4T2aH7yX9vZ1bC8dE5fG6hJ7kL8mN9oP0=",
        spkiSha256Hex: "ab42df",
        fingerprintSha256: "aa11",
      },
      {
        ...base,
        subject: "CN=Acme Private Root",
        issuer: "CN=Acme Private Root",
        dnsNames: [],
        isCa: true,
        selfSigned: true,
        spkiSha256: "Zm9vYmFy",
        spkiSha256Hex: "666f6f",
        fingerprintSha256: "bb22",
      },
    ],
  };
}

export function destinationFixture(
  type: "S3" | "SFTP" | "DISK" = "S3",
  overrides: Record<string, unknown> = {},
  admin = true,
) {
  const settings = {
    __typename: "DestinationSettings",
    disk: type === "DISK" ? { __typename: "DiskDestinationSettings", path: "nightly" } : null,
    s3:
      type === "S3"
        ? {
            __typename: "S3DestinationSettings",
            endpoint: null,
            region: "eu-central-1",
            bucket: "acme-backups",
            prefix: "bekup",
            pathStyle: false,
            encryption: "SSE_S3",
            kmsKeyId: null,
            storageClass: null,
            checksumMode: "WHEN_SUPPORTED",
            conditionalWrites: true,
            accessKeyId: "AKIAEXAMPLE",
            hasSecretAccessKey: true,
            caBundlePem: null,
          }
        : null,
    sftp:
      type === "SFTP"
        ? {
            __typename: "SftpDestinationSettings",
            host: "files.example",
            port: 22,
            user: "bekup",
            basePath: "/srv/backups",
            dir: "",
            authMethod: "PASSWORD",
            hasPassword: true,
            hasPrivateKey: false,
            hasPassphrase: false,
            publicKey: null,
            hostKey: null,
          }
        : null,
  };
  return {
    __typename: "Destination",
    id: DEST_ID,
    name: type === "S3" ? "offsite-s3" : type === "SFTP" ? "nas-sftp" : "local-disk",
    type,
    noDelete: false,
    objectLockMode: type === "S3" ? "COMPLIANCE" : null,
    objectLockRetentionDays: type === "S3" ? 30 : null,
    ready: true,
    notReadyReason: null,
    createdAt: "2026-10-01T10:00:00Z",
    updatedAt: "2026-10-02T10:00:00Z",
    lastTest: null,
    settings: admin ? settings : null,
    ...overrides,
  };
}

/** Step-up handlers: TOTP 654321 completes it; `state.stepped` turns true. */
export function stepUpHandlers(state: { stepped: boolean }): Record<string, Handler> {
  return {
    BeginStepUp: () =>
      ok({
        beginStepUp: {
          __typename: "StepUpChallenge",
          methods: ["TOTP"],
          passkeyOptions: null,
        },
      }),
    CompleteStepUp: (vars) => {
      state.stepped = vars["code"] === "654321";
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
}

/** jsdom has no ResizeObserver; Radix Switch measures its thumb with one. */
export function stubResizeObserver() {
  if ("ResizeObserver" in globalThis) return;
  class ResizeObserverStub {
    observe() {
      /* no layout in jsdom */
    }
    unobserve() {
      /* no layout in jsdom */
    }
    disconnect() {
      /* no layout in jsdom */
    }
  }
  Object.defineProperty(globalThis, "ResizeObserver", {
    value: ResizeObserverStub,
    configurable: true,
  });
}
