import { graphql } from "@/graphql/gen";

export const RunsListQuery = graphql(`
  query RunsList($filter: RunFilter, $first: Int, $after: String) {
    runs(filter: $filter, first: $first, after: $after) {
      edges {
        cursor
        node {
          id
          status
          trigger
          scheduledFor
          queuedAt
          startedAt
          finishedAt
          durationSeconds
          bytesStored
          attempt
          job {
            id
            name
            deleted
          }
          warnings {
            code
          }
          items {
            id
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

export const RunDetailQuery = graphql(`
  query RunDetail($id: UUID!) {
    run(id: $id) {
      id
      status
      trigger
      triggeredBy {
        userId
        displayName
        email
      }
      scheduledFor
      attempt
      queuedAt
      startedAt
      finishedAt
      durationSeconds
      storageTimestamp
      errorCode
      errorMessage
      warnings {
        code
        message
        database
        destinationId
      }
      notices {
        code
        message
        database
        destinationId
      }
      bytesRaw
      bytesStored
      cancelRequested
      cancelRequestedAt
      logLines
      logLinesDropped
      updatedAt
      job {
        id
        name
        slug
        deleted
      }
      items {
        id
        kind
        database
        position
        status
        schemaVersion
        schemaVersionStatus
        rawSize
        storedSize
        sha256
        verified
        startedAt
        finishedAt
        durationSeconds
        errorCode
        errorMessage
        warnings {
          code
          message
          database
          destinationId
        }
        artifacts {
          id
          kind
          state
          verified
          signed
          size
          sha256
          objectKey
          lockUntil
          stateChangedAt
          encryption {
            mode
            passphraseVersion
            recipientVersion
          }
          destination {
            id
            name
            type
            deleted
          }
        }
      }
      manifest {
        signed
        signingKeyId
        coveredItems
        totalItems
        copies {
          id
          state
          destination {
            id
            name
          }
        }
      }
    }
  }
`);

/** The job facts the completeness panel needs (engine, verification level). */
/**
 * The run page's one stream (D-197): changes of the run with every field
 * of RunDetail (the cache merges them) and its new log lines after
 * afterLogId. Completes after the done log batch and the final run state.
 */
export const RunActivitySubscription = graphql(`
  subscription RunActivity($runId: UUID!, $afterLogId: ID) {
    runActivity(runId: $runId, afterLogId: $afterLogId) {
      run {
        id
        status
        trigger
        triggeredBy {
          userId
          displayName
          email
        }
        scheduledFor
        attempt
        queuedAt
        startedAt
        finishedAt
        durationSeconds
        storageTimestamp
        errorCode
        errorMessage
        warnings {
          code
          message
          database
          destinationId
        }
        notices {
          code
          message
          database
          destinationId
        }
        bytesRaw
        bytesStored
        cancelRequested
        cancelRequestedAt
        logLines
        logLinesDropped
        updatedAt
        job {
          id
          name
          slug
          deleted
        }
        items {
          id
          kind
          database
          position
          status
          schemaVersion
          schemaVersionStatus
          rawSize
          storedSize
          sha256
          verified
          startedAt
          finishedAt
          durationSeconds
          errorCode
          errorMessage
          warnings {
            code
            message
            database
            destinationId
          }
          artifacts {
            id
            kind
            state
            verified
            signed
            size
            sha256
            objectKey
            lockUntil
            stateChangedAt
            encryption {
              mode
              passphraseVersion
              recipientVersion
            }
            destination {
              id
              name
              type
              deleted
            }
          }
        }
        manifest {
          signed
          signingKeyId
          coveredItems
          totalItems
          copies {
            id
            state
            destination {
              id
              name
            }
          }
        }
      }
      logs {
        runId
        endId
        done
        lines {
          id
          time
          level
          stream
          line
        }
      }
    }
  }
`);

export const RunJobContextQuery = graphql(`
  query RunJobContext($id: UUID!) {
    job(id: $id) {
      id
      verification
      connection {
        id
        engine
      }
    }
  }
`);

/**
 * The completeness evidence of each item (RunItem.stats, D-187): object
 * counts, per-table rows with their exactness, findings. Its own request,
 * loaded once the run is no longer active (stats exist only for complete,
 * verified dumps).
 */
export const RunItemStatsQuery = graphql(`
  query RunItemStats($id: UUID!) {
    run(id: $id) {
      id
      items {
        id
        kind
        database
        stats {
          level
          consistency
          objects {
            kind
            count
          }
          tables {
            name
            rows
            count
            exact
            estimate
            unknown
            approximate
          }
          tablesTotal
          tablesTruncated
          warnings {
            code
            count
            objects
            truncated
          }
          notices {
            code
            count
            objects
            truncated
          }
          recordedAt
        }
      }
    }
  }
`);

export const RunTestRestoresQuery = graphql(`
  query RunTestRestores($jobId: UUID!) {
    testRestores(jobId: $jobId, first: 50) {
      edges {
        node {
          id
          runId
          status
          finishedAt
        }
      }
    }
  }
`);

export const TestRestoreFindingsQuery = graphql(`
  query TestRestoreFindings($id: UUID!) {
    testRestore(id: $id) {
      id
      status
      finishedAt
      errorMessage
      databaseCount
      passed
      warned
      failed
      databases {
        id
        database
        status
        errorMessage
        tablesCompared
        exactTables
        findingsTotal
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

export const RunLogsQuery = graphql(`
  query RunLogs($runId: UUID!, $afterId: ID, $first: Int) {
    runLogs(runId: $runId, afterId: $afterId, first: $first) {
      runId
      endId
      hasMore
      dropped
      lines {
        id
        time
        level
        stream
        line
      }
    }
  }
`);

/**
 * Live run changes. One stream per page (D-149, stream cap): the run page
 * filters by runId, list pages by jobId or not at all.
 */
export const RunUpdatedSubscription = graphql(`
  subscription RunUpdated($jobId: UUID, $runId: UUID) {
    runUpdated(jobId: $jobId, runId: $runId) {
      id
      status
      startedAt
      finishedAt
      durationSeconds
      bytesRaw
      bytesStored
      cancelRequested
      logLines
      updatedAt
      job {
        id
      }
    }
  }
`);

export const CancelRunMutation = graphql(`
  mutation CancelRun($id: UUID!) {
    cancelRun(id: $id) {
      runId
      status
      retryCanceled
    }
  }
`);

export const CreateDownloadTicketMutation = graphql(`
  mutation CreateDownloadTicket($artifactId: UUID!, $mode: DownloadMode!, $ageIdentity: String) {
    createDownloadTicket(artifactId: $artifactId, mode: $mode, ageIdentity: $ageIdentity) {
      url
      expiresAt
      mode
      fileName
      format
      restoreCommand
    }
  }
`);
