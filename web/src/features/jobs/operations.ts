import { graphql } from "@/graphql/gen";

export const JobsListQuery = graphql(`
  query JobsList($filter: JobFilter, $first: Int, $after: String) {
    jobs(filter: $filter, first: $first, after: $after) {
      edges {
        cursor
        node {
          id
          name
          slug
          enabled
          nextRunAt
          scheduleDescription
          connection {
            id
            name
            engine
            deleted
          }
          encryption {
            configured
            mode
            optedOut
          }
          owners {
            userId
            displayName
            email
            role
            disabled
          }
          lastRun {
            id
            status
            finishedAt
            queuedAt
            warnings {
              code
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

export const JobFilterConnectionsQuery = graphql(`
  query JobFilterConnections {
    connections {
      id
      name
      engine
      temporary
    }
  }
`);

export const JobDetailQuery = graphql(`
  query JobDetail($id: UUID!) {
    job(id: $id) {
      id
      name
      slug
      description
      enabled
      nextRunAt
      lastScheduledAt
      scheduleDescription
      schedule {
        kind
        every
        unit
        anchor
        weekday
        dayOfMonth
        cron
        timezone
      }
      connection {
        id
        name
        engine
        versionKey
        deleted
      }
      dbMode
      databases
      exclude
      includeGlobals
      retention {
        keepLast
        keepDays
      }
      timeoutSeconds
      verification
      destinations {
        id
        name
        type
        position
        deleted
      }
      encryption {
        configured
        mode
        optedOut
        optedOutAt
        optOutReason
        passphrase {
          version
          createdAt
        }
        recipient {
          version
          recipient
          fingerprint
          label
          generated
          createdAt
        }
        updatedAt
      }
      owners {
        userId
        email
        displayName
        role
        disabled
        addedAt
      }
      lastRun {
        id
        status
        queuedAt
        finishedAt
        items {
          id
          kind
          database
          position
          schemaVersion
          schemaVersionStatus
        }
      }
      schemaVersion {
        mode
        preset
        presetLabel
        schema
        customSql
      }
      retentionHolds {
        destinationId
        destinationName
        destinationType
        destinationDeleted
        reason
        heldRuns
        since
        newestCompleteAt
        checkedAt
      }
      testRestore {
        enabled
        scheduleDescription
        schedule {
          kind
          every
          unit
          anchor
          weekday
          dayOfMonth
          cron
          timezone
        }
        nextAt
        target {
          id
          name
          engine
          deleted
        }
        targetPrivileges {
          status
          reasons
          acknowledgementRequired
        }
        privilegedTargetAcknowledgedAt
      }
      createdAt
      updatedAt
    }
  }
`);

export const LatestTestRestoreQuery = graphql(`
  query LatestTestRestore($jobId: UUID!) {
    testRestores(jobId: $jobId, first: 1) {
      edges {
        node {
          id
          runId
          status
          finishedAt
          createdAt
          databaseCount
          passed
          warned
          failed
          errorMessage
        }
      }
    }
  }
`);

/** A job's test restores, newest first (list rows; no databases). */
export const TestRestoresQuery = graphql(`
  query JobTestRestores($jobId: UUID!, $first: Int, $after: String) {
    testRestores(jobId: $jobId, first: $first, after: $after) {
      edges {
        cursor
        node {
          id
          runId
          status
          trigger
          requestedBy {
            userId
            displayName
            email
          }
          target {
            id
            name
            deleted
          }
          scheduledFor
          createdAt
          startedAt
          finishedAt
          durationSeconds
          errorCode
          errorMessage
          databaseCount
          passed
          warned
          failed
        }
      }
      pageInfo {
        hasNextPage
        endCursor
      }
    }
  }
`);

/** One test restore with its databases and the comparison findings (the diff). */
export const TestRestoreDetailQuery = graphql(`
  query TestRestoreDetail($id: UUID!) {
    testRestore(id: $id) {
      id
      job {
        id
        name
        slug
        deleted
      }
      runId
      status
      trigger
      requestedBy {
        userId
        displayName
        email
      }
      target {
        id
        name
        engine
        temporary
        deleted
      }
      scheduledFor
      createdAt
      startedAt
      finishedAt
      durationSeconds
      errorCode
      errorMessage
      databaseCount
      passed
      warned
      failed
      databases {
        id
        runItemId
        database
        restoreId
        status
        errorCode
        errorMessage
        scratchDropped
        findingsTotal
        tablesCompared
        exactTables
        restoreStatus
        restoreErrorCode
        startedAt
        finishedAt
        findings {
          severity
          code
          object
          source
          restored
          delta
        }
      }
    }
  }
`);

/** Scratch-target candidates with their stored privilege verdict (D-187). */
export const TestRestoreTargetsQuery = graphql(`
  query TestRestoreTargets {
    connections {
      id
      name
      engine
      temporary
      ready
      lastTest {
        at
        ok
        stale
        privileges {
          status
          reasons
          acknowledgementRequired
        }
      }
    }
  }
`);

export const SetJobTestRestoreMutation = graphql(`
  mutation SetJobTestRestore($jobId: UUID!, $input: JobTestRestoreInput!) {
    setJobTestRestore(jobId: $jobId, input: $input) {
      id
      testRestore {
        enabled
        scheduleDescription
        schedule {
          kind
          every
          unit
          anchor
          weekday
          dayOfMonth
          cron
          timezone
        }
        nextAt
        target {
          id
          name
          engine
          deleted
        }
        targetPrivileges {
          status
          reasons
          acknowledgementRequired
        }
        privilegedTargetAcknowledgedAt
      }
    }
  }
`);

/** The schema-version presets (D-210): the server's list, staff only. */
export const SchemaVersionPresetsQuery = graphql(`
  query SchemaVersionPresets {
    schemaVersionPresets {
      name
      label
      description
      postgresTable
      mysqlTable
      engines
    }
  }
`);

export const SetJobSchemaVersionMutation = graphql(`
  mutation SetJobSchemaVersion($jobId: UUID!, $input: JobSchemaVersionInput!) {
    setJobSchemaVersion(jobId: $jobId, input: $input) {
      id
      schemaVersion {
        mode
        preset
        presetLabel
        schema
        customSql
      }
    }
  }
`);

export const RunTestRestoreNowMutation = graphql(`
  mutation RunTestRestoreNow($jobId: UUID!) {
    runTestRestoreNow(jobId: $jobId) {
      id
      status
    }
  }
`);

export const JobEditorOptionsQuery = graphql(`
  query JobEditorOptions {
    connections {
      id
      name
      engine
      versionKey
      temporary
    }
    destinations {
      id
      name
      type
      ready
      notReadyReason
    }
  }
`);

export const PreviewScheduleQuery = graphql(`
  query PreviewSchedule($input: ScheduleInput!, $count: Int) {
    previewSchedule(input: $input, count: $count) {
      description
      next
      schedule {
        kind
        timezone
      }
    }
  }
`);

/**
 * The steady-state number of backups each destination keeps for a schedule
 * and retention draft (D-209). Its own request so retention edits do not
 * refetch the schedule preview.
 */
export const PreviewRetentionQuery = graphql(`
  query PreviewRetention($input: ScheduleInput!, $retention: JobRetentionInput!) {
    previewSchedule(input: $input, count: 1, retention: $retention) {
      retention {
        kept
        byKeepLast
        byKeepDays
        atLeast
      }
    }
  }
`);

export const CreateJobMutation = graphql(`
  mutation CreateJob($input: CreateJobInput!) {
    createJob(input: $input) {
      job {
        id
        name
        slug
      }
      generatedPassphrase
      generatedKeypair {
        version
        recipient
        fingerprint
        identity
        identityFile
        fileName
      }
    }
  }
`);

export const UpdateJobMutation = graphql(`
  mutation UpdateJob($id: UUID!, $input: UpdateJobInput!) {
    updateJob(id: $id, input: $input) {
      id
      name
      updatedAt
    }
  }
`);

export const DeleteJobMutation = graphql(`
  mutation DeleteJob($id: UUID!) {
    deleteJob(id: $id)
  }
`);

export const EnableJobMutation = graphql(`
  mutation EnableJob($id: UUID!) {
    enableJob(id: $id) {
      id
      enabled
      nextRunAt
    }
  }
`);

export const DisableJobMutation = graphql(`
  mutation DisableJob($id: UUID!) {
    disableJob(id: $id) {
      id
      enabled
      nextRunAt
    }
  }
`);

export const RunJobNowMutation = graphql(`
  mutation RunJobNow($id: UUID!) {
    runJobNow(id: $id) {
      runId
      queuedAt
      job {
        id
        nextRunAt
      }
    }
  }
`);

export const StartNextRunEarlyMutation = graphql(`
  mutation StartNextRunEarly($id: UUID!) {
    startNextRunEarly(id: $id) {
      runId
      queuedAt
      job {
        id
        nextRunAt
      }
    }
  }
`);

/** The occurrence after the next one, for the start-early dialog. */
export const NextOccurrencesQuery = graphql(`
  query JobNextOccurrences($input: ScheduleInput!) {
    previewSchedule(input: $input, count: 3) {
      next
    }
  }
`);

export const GenerateJobPassphraseMutation = graphql(`
  mutation GenerateJobPassphrase($jobId: UUID!) {
    generateJobPassphrase(jobId: $jobId) {
      version
      passphrase
    }
  }
`);

export const SetJobPassphraseMutation = graphql(`
  mutation SetJobPassphrase($jobId: UUID!, $passphrase: String!) {
    setJobPassphrase(jobId: $jobId, passphrase: $passphrase) {
      mode
    }
  }
`);

export const GenerateAgeKeypairMutation = graphql(`
  mutation GenerateAgeKeypair($jobId: UUID!, $label: String) {
    generateAgeKeypair(jobId: $jobId, label: $label) {
      version
      recipient
      fingerprint
      identity
      identityFile
      fileName
    }
  }
`);

export const SetJobRecipientMutation = graphql(`
  mutation SetJobRecipient($jobId: UUID!, $recipient: String!, $label: String) {
    setJobRecipient(jobId: $jobId, recipient: $recipient, label: $label) {
      mode
    }
  }
`);

export const EnableJobEncryptionMutation = graphql(`
  mutation EnableJobEncryption($jobId: UUID!, $mode: JobKeyMode!) {
    enableJobEncryption(jobId: $jobId, mode: $mode) {
      mode
    }
  }
`);

export const DisableJobEncryptionMutation = graphql(`
  mutation DisableJobEncryption($jobId: UUID!, $reason: String!) {
    disableJobEncryption(jobId: $jobId, reason: $reason) {
      mode
      optedOut
    }
  }
`);

/**
 * The shared channels attached to a job (D-198): admins and maintainers
 * only (null for backup owners). Webhook URLs come masked (scheme, host
 * and port).
 */
export const JobSharedChannelAttachmentsQuery = graphql(`
  query JobSharedChannelAttachments($id: UUID!) {
    job(id: $id) {
      id
      sharedChannelAttachments {
        channelId
        name
        type
        displayUrl
        enabled
        groups
        events
      }
    }
  }
`);
