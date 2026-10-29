import { graphql } from "@/graphql/gen";

/*
 * Operation names carry a Restore prefix: names are global across the app
 * (codegen) and other pages have their own connection and job operations.
 */

const RESTORE_FIELDS = graphql(`
  fragment RestoreFields on Restore {
    id
    job {
      id
      name
      slug
      deleted
    }
    artifactId
    runId
    kind
    sourceDatabase
    target {
      id
      name
      engine
      temporary
      deleted
    }
    targetDatabase
    mode
    keepOwners
    status
    requestedBy {
      userId
      displayName
      email
    }
    createdAt
    startedAt
    finishedAt
    durationSeconds
    stagedBytes
    errorCode
    errorMessage
    warnings {
      code
      message
    }
    logLines
    logLinesDropped
    updatedAt
  }
`);

export { RESTORE_FIELDS as RestoreFieldsFragment };

/* ---- Step 1: the backup ------------------------------------------------ */

export const RestoreJobsQuery = graphql(`
  query RestoreJobs($after: String) {
    jobs(first: 200, after: $after) {
      edges {
        node {
          id
          name
          slug
          connection {
            id
            name
            engine
            versionKey
          }
        }
      }
      pageInfo {
        hasNextPage
        endCursor
      }
    }
  }
`);

export const RestoreRunsQuery = graphql(`
  query RestoreRuns($jobId: UUID!, $after: String) {
    runs(filter: { jobId: $jobId }, first: 25, after: $after) {
      edges {
        node {
          id
          status
          trigger
          startedAt
          finishedAt
          queuedAt
          storageTimestamp
          items {
            id
            kind
            database
            status
            artifacts {
              id
              kind
              database
              size
              verified
              signed
              state
              createdAt
              destination {
                id
                name
                type
                deleted
              }
              encryption {
                mode
              }
            }
          }
        }
      }
      pageInfo {
        hasNextPage
        endCursor
      }
    }
  }
`);

/* ---- Step 2: the target -------------------------------------------------- */

export const RestoreTargetsQuery = graphql(`
  query RestoreTargets($artifactId: UUID!) {
    restoreTargets(artifactId: $artifactId) {
      source {
        artifactId
        jobId
        runId
        kind
        database
        engine
        versionKey
        serverVersion
        needsIdentity
      }
      targets {
        id
        name
        engine
        versionKey
        original
        temporary
        expiresAt
        ready
        notReadyReason
        insecureTransport
        newDatabaseAllowed
        existingRestricted
      }
    }
  }
`);

/** Single root: the server reads the databases through the runner. */
export const RestoreTargetDatabasesQuery = graphql(`
  query RestoreTargetDatabases($artifactId: UUID!, $targetConnectionId: UUID!) {
    restoreTargetDatabases(artifactId: $artifactId, targetConnectionId: $targetConnectionId) {
      databases
      warnings
      listedAt
      targetPrivileges {
        status
        reasons
        acknowledgementRequired
      }
    }
  }
`);

/* ---- Temporary restore connections (admins, task 4.4) -------------------- */

const TEMP_CONNECTION_FIELDS = graphql(`
  fragment RestoreTempConnectionFields on Connection {
    id
    name
    engine
    versionKey
    host
    port
    tlsMode
    insecureTransport
    pinStatus
    sshEnabled
    ready
    notReadyReason
    temporary
    expiresAt
    settings {
      tlsPin {
        subject
        notAfter
      }
      ssh {
        host
        port
        hostKey {
          fingerprintSha256
          confirmedAt
        }
      }
    }
  }
`);

export { TEMP_CONNECTION_FIELDS as RestoreTempConnectionFieldsFragment };

export const RestoreEngineVersionsQuery = graphql(`
  query RestoreEngineVersions($engine: DatabaseEngine) {
    engineVersions(engine: $engine) {
      engine
      key
      clientVersion
      legacy
    }
  }
`);

export const RestoreCreateTemporaryConnectionMutation = graphql(`
  mutation RestoreCreateTemporaryConnection($input: CreateTemporaryConnectionInput!) {
    createTemporaryConnection(input: $input) {
      ...RestoreTempConnectionFields
    }
  }
`);

export const RestoreDeleteTemporaryConnectionMutation = graphql(`
  mutation RestoreDeleteTemporaryConnection($id: UUID!) {
    deleteTemporaryConnection(id: $id)
  }
`);

export const RestoreFetchCertificateMutation = graphql(`
  mutation RestoreFetchCertificate($id: UUID!) {
    fetchCertificate(id: $id) {
      certificates {
        subject
        issuer
        dnsNames
        ipAddresses
        notBefore
        notAfter
        isCa
        selfSigned
        spkiSha256
        spkiSha256Hex
        fingerprintSha256
      }
      suggestedAnchorIndex
      leafSpkiSha256
    }
  }
`);

export const RestoreConfirmCertificatePinMutation = graphql(`
  mutation RestoreConfirmCertificatePin($id: UUID!, $input: CertificatePinInput!) {
    confirmCertificatePin(id: $id, input: $input) {
      ...RestoreTempConnectionFields
    }
  }
`);

export const RestoreFetchSshHostKeyMutation = graphql(`
  mutation RestoreFetchSshHostKey($id: UUID!) {
    fetchSshHostKey(id: $id) {
      type
      fingerprintSha256
      authorizedKey
      host
      port
    }
  }
`);

export const RestoreConfirmSshHostKeyMutation = graphql(`
  mutation RestoreConfirmSshHostKey(
    $id: UUID!
    $authorizedKey: String!
    $expectedHost: String!
    $expectedPort: Int!
  ) {
    confirmSshHostKey(
      id: $id
      authorizedKey: $authorizedKey
      expectedHost: $expectedHost
      expectedPort: $expectedPort
    ) {
      ...RestoreTempConnectionFields
    }
  }
`);

export const RestoreTestConnectionMutation = graphql(`
  mutation RestoreTestConnection($id: UUID!) {
    testConnection(id: $id) {
      ok
      code
      message
      serverVersion
      compatible
      warnings
      privileges {
        status
        reasons
        acknowledgementRequired
      }
    }
  }
`);

/* ---- Steps 4-5: preview and submit ---------------------------------------- */

/** Single root: the server reads the target through the runner. */
export const RestorePreviewQuery = graphql(`
  query RestorePreview($input: RestorePreviewInput!) {
    restorePreview(input: $input) {
      mode
      targetDatabase
      warnings {
        code
        message
      }
      confirmText
      crossSource
      targetPrivileges {
        status
        reasons
        acknowledgementRequired
      }
      target {
        id
        name
        engine
        temporary
        insecureTransport
      }
      source {
        artifactId
        kind
        database
        engine
        versionKey
        serverVersion
        needsIdentity
      }
    }
  }
`);

/** Whether the account has a factor that reaches AAL2 (TOTP or a passkey; D-116). */
export const RestoreFactorStatusQuery = graphql(`
  query RestoreFactorStatus {
    myTwoFactorStatus {
      totpEnabled
    }
    myPasskeys {
      id
    }
  }
`);

/** Single root: it may carry the age identity. */
export const RestoreArtifactMutation = graphql(`
  mutation RestoreArtifact($input: RestoreArtifactInput!) {
    restoreArtifact(input: $input) {
      ...RestoreFields
    }
  }
`);

export const RestoreGlobalsMutation = graphql(`
  mutation RestoreGlobals($input: RestoreGlobalsInput!) {
    restoreGlobals(input: $input) {
      ...RestoreFields
    }
  }
`);

/* ---- Step 6: progress, history ------------------------------------------- */

export const RestoreDetailQuery = graphql(`
  query RestoreDetail($id: UUID!) {
    restore(id: $id) {
      ...RestoreFields
    }
  }
`);

export const RestoreUpdatedSubscription = graphql(`
  subscription RestoreUpdated($restoreId: UUID!) {
    restoreUpdated(restoreId: $restoreId) {
      ...RestoreFields
    }
  }
`);

export const RestoreLogsQuery = graphql(`
  query RestoreLogs($restoreId: UUID!, $afterId: ID) {
    restoreLogs(restoreId: $restoreId, afterId: $afterId, first: 500) {
      restoreId
      lines {
        id
        time
        level
        stream
        line
      }
      endId
      hasMore
      dropped
    }
  }
`);

export const RestoreCancelMutation = graphql(`
  mutation RestoreCancel($id: UUID!) {
    cancelRestore(id: $id) {
      restoreId
      status
      requestedAt
    }
  }
`);

export const RestoreHistoryQuery = graphql(`
  query RestoreHistory($filter: RestoreFilter, $after: String) {
    restores(filter: $filter, first: 50, after: $after) {
      edges {
        node {
          ...RestoreFields
        }
      }
      pageInfo {
        hasNextPage
        endCursor
      }
    }
  }
`);
